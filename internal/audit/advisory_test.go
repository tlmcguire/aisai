package audit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tlmcguire/aisai/internal/advisory"
	"github.com/tlmcguire/aisai/internal/config"
	"github.com/tlmcguire/aisai/internal/findings"
	"github.com/tlmcguire/aisai/internal/model"
)

// newTestManagerWithPackages builds a manager holding one audit with the
// given packages, bypassing the network-bound Run phase.
func newTestManagerWithPackages(t *testing.T, adv *advisory.Client, pkgs ...model.Package) (*Manager, string) {
	t.Helper()

	m := NewManager(Deps{
		Config:   &config.Config{Workspace: t.TempDir()},
		Advisory: adv,
		Store:    nil,
	})

	id := "test-audit"
	m.audits[id] = &Audit{
		ID:       id,
		Package:  pkgs[0].Name,
		Version:  pkgs[0].Version,
		status:   model.AuditReady,
		packages: pkgs,
	}
	return m, id
}

// osvServer returns a stub OSV API that answers /v1/query with a fixed set
// of advisories for the requested package name.
func osvServer(t *testing.T, byPackage map[string][]map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Package struct {
				Name string `json:"name"`
			} `json:"package"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode osv request: %v", err)
		}
		vulns := byPackage[req.Package.Name]
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"vulns": vulns})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestFetchAdvisoriesPopulatesGraph(t *testing.T) {
	srv := osvServer(t, map[string][]map[string]any{
		"flask": {
			{
				"id":      "GHSA-xxxx-yyyy-zzzz",
				"summary": "Flask session cookie disclosure",
				"aliases": []string{"CVE-2023-30861"},
				"database_specific": map[string]any{
					"severity": "HIGH",
					"cwe_ids":  []string{"CWE-200"},
				},
			},
			{
				"id":      "GHSA-aaaa-bbbb-cccc",
				"summary": "Flask debug mode leak",
				"database_specific": map[string]any{
					"severity": "MODERATE",
				},
			},
		},
		"jinja2": {},
	})

	adv := advisory.New(advisory.Options{BaseURL: srv.URL})

	pkgs := []model.Package{
		{PURL: "pkg:pypi/flask@2.0.0", Name: "flask", Version: "2.0.0", Ecosystem: "PyPI", Direct: true},
		{PURL: "pkg:pypi/jinja2@3.1.6", Name: "jinja2", Version: "3.1.6", Ecosystem: "PyPI"},
	}
	m, id := newTestManagerWithPackages(t, adv, pkgs...)

	ctx := context.Background()
	list, err := m.FetchAdvisories(ctx, id, false)
	if err != nil {
		t.Fatalf("FetchAdvisories: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("got %d advisories, want 2: %+v", len(list), list)
	}

	// The CVE alias is preferred over the GHSA id.
	if list[0].ID != "CVE-2023-30861" {
		t.Errorf("advisory id = %q, want the CVE alias", list[0].ID)
	}
	// The highest severity sorts first.
	if list[0].Severity != model.SeverityHigh {
		t.Errorf("advisory severity = %q, want high", list[0].Severity)
	}
	if len(list[0].CWE) != 1 || list[0].CWE[0] != "CWE-200" {
		t.Errorf("advisory CWE = %v, want [CWE-200]", list[0].CWE)
	}

	// The graph must now report the vulnerability count and severity.
	g, err := m.Graph(ctx, id)
	if err != nil {
		t.Fatalf("Graph: %v", err)
	}
	if len(g.Nodes) != 2 {
		t.Fatalf("got %d graph nodes, want 2", len(g.Nodes))
	}
	byName := map[string]model.GraphNode{}
	for _, n := range g.Nodes {
		byName[n.Name] = n
	}
	if got := byName["flask"].VulnCount; got != 2 {
		t.Errorf("flask vulnCount = %d, want 2", got)
	}
	if got := byName["flask"].MaxSeverity; got != model.SeverityHigh {
		t.Errorf("flask maxSeverity = %q, want high", got)
	}
	if got := byName["jinja2"].VulnCount; got != 0 {
		t.Errorf("jinja2 vulnCount = %d, want 0", got)
	}
}

func TestFetchAdvisoriesIsCached(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"vulns": []any{}})
	}))
	defer srv.Close()

	adv := advisory.New(advisory.Options{BaseURL: srv.URL})
	pkgs := []model.Package{
		{PURL: "pkg:pypi/flask@2.0.0", Name: "flask", Version: "2.0.0", Ecosystem: "PyPI"},
	}
	m, id := newTestManagerWithPackages(t, adv, pkgs...)

	ctx := context.Background()
	if _, err := m.FetchAdvisories(ctx, id, false); err != nil {
		t.Fatalf("first FetchAdvisories: %v", err)
	}
	if _, err := m.FetchAdvisories(ctx, id, false); err != nil {
		t.Fatalf("second FetchAdvisories: %v", err)
	}
	if calls != 1 {
		t.Errorf("OSV was queried %d times, want 1 (results should be cached)", calls)
	}

	// An explicit refresh must re-query.
	if _, err := m.FetchAdvisories(ctx, id, true); err != nil {
		t.Fatalf("refresh FetchAdvisories: %v", err)
	}
	if calls != 2 {
		t.Errorf("OSV was queried %d times after refresh, want 2", calls)
	}
}

// TestFetchAdvisoriesDeduplicatesPerPackage is a regression test.
//
// OSV can return several records for the same vulnerability — a GHSA record
// and a PYSEC record, say, both aliased to the same CVE. The advisory id is
// the CVE when there is one, so those records collapse to one advisory. They
// have to collapse per package as well: otherwise the graph's vulnerability
// count counted the duplicates while the advisory list showed one entry, and
// the two numbers disagreed.
func TestFetchAdvisoriesDeduplicatesPerPackage(t *testing.T) {
	srv := osvServer(t, map[string][]map[string]any{
		"flask": {
			{
				"id":      "GHSA-xxxx-yyyy-zzzz",
				"summary": "Flask session cookie disclosure",
				"aliases": []string{"CVE-2023-30861"},
				"database_specific": map[string]any{
					"severity": "HIGH",
				},
			},
			{
				// The same vulnerability from a second source.
				"id":      "PYSEC-2023-1",
				"summary": "Flask session cookie disclosure",
				"aliases": []string{"CVE-2023-30861"},
				"database_specific": map[string]any{
					"severity": "HIGH",
				},
			},
		},
	})

	adv := advisory.New(advisory.Options{BaseURL: srv.URL})
	pkgs := []model.Package{
		{PURL: "pkg:pypi/flask@2.0.0", Name: "flask", Version: "2.0.0", Ecosystem: "PyPI"},
	}
	m, id := newTestManagerWithPackages(t, adv, pkgs...)

	ctx := context.Background()
	list, err := m.FetchAdvisories(ctx, id, false)
	if err != nil {
		t.Fatalf("FetchAdvisories: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("got %d advisories, want 1 after deduplication: %+v", len(list), list)
	}

	// The graph count must agree with the list.
	g, err := m.Graph(ctx, id)
	if err != nil {
		t.Fatalf("Graph: %v", err)
	}
	if len(g.Nodes) != 1 {
		t.Fatalf("got %d graph nodes, want 1", len(g.Nodes))
	}
	if got := g.Nodes[0].VulnCount; got != len(list) {
		t.Errorf("the graph reports %d vulnerabilities but the advisory list has %d", got, len(list))
	}
}

func TestFetchAdvisoriesWithoutSourceIsAnError(t *testing.T) {
	pkgs := []model.Package{
		{PURL: "pkg:pypi/flask@2.0.0", Name: "flask", Version: "2.0.0", Ecosystem: "PyPI"},
	}
	m, id := newTestManagerWithPackages(t, nil, pkgs...)

	if _, err := m.FetchAdvisories(context.Background(), id, false); err == nil {
		t.Fatal("expected an error when no advisory source is configured")
	}
}

func TestFetchAdvisoriesUnknownAudit(t *testing.T) {
	m := NewManager(Deps{Config: &config.Config{Workspace: t.TempDir()}})
	if _, err := m.FetchAdvisories(context.Background(), "nope", false); err != findings.ErrNotFound {
		t.Fatalf("err = %v, want findings.ErrNotFound", err)
	}
}
