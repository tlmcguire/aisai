package advisory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tlmcguire/aisai/internal/model"
)

// offlineRecord is a record in the offline database.
//
// The offline database is a directory of per-package JSON files, laid out as
// <db>/PyPI/<package>.json, each holding an object with a "vulns" array of
// OSV records. The fixture below has the shape of a real OSV record for
// requests 2.30.0.
const offlineRecord = `{
  "vulns": [
    {
      "id": "GHSA-j8r5-328x-9v2m",
      "summary": "Unintended leak of Proxy-Authorization header in requests",
      "details": "Requests before 2.31.0 leaks the Proxy-Authorization header.",
      "aliases": ["CVE-2023-32681"],
      "severity": [
        {"type": "CVSS_V3", "score": "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:N/A:N"}
      ],
      "database_specific": {"severity": "MODERATE", "cwe_ids": ["CWE-200"]},
      "affected": [
        {
          "package": {"name": "requests", "ecosystem": "PyPI"},
          "ranges": [
            {"type": "ECOSYSTEM", "events": [{"introduced": "0"}, {"fixed": "2.31.0"}]}
          ]
        }
      ],
      "references": [
        {"type": "ADVISORY", "url": "https://github.com/psf/requests/security/advisories/GHSA-j8r5-328x-9v2m"},
        {"type": "FIX", "url": "https://github.com/psf/requests/pull/6432"}
      ]
    },
    {
      "id": "PYSEC-2023-74",
      "summary": "requests is vulnerable to an unrelated issue",
      "aliases": ["CVE-2022-99999"],
      "affected": [
        {
          "package": {"name": "requests", "ecosystem": "PyPI"},
          "versions": ["9.9.9"]
        }
      ]
    }
  ]
}`

// writeOfflineDatabase builds an offline OSV database in a temporary
// directory and returns its root.
//
// The layout is the directory form of the published database: one JSON file
// per package under <root>/PyPI.
func writeOfflineDatabase(t *testing.T, packages map[string]string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "osv")
	if err := os.MkdirAll(filepath.Join(root, "PyPI"), 0o755); err != nil {
		t.Fatalf("create database directory: %v", err)
	}
	for name, body := range packages {
		path := filepath.Join(root, "PyPI", name+".json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return root
}

// osvResponseFixture is a recorded OSV query response.
//
// It holds two records for the same vulnerability: a GHSA record and a PYSEC
// record, both aliased to CVE-2021-44228.
const osvResponseFixture = `{
  "vulns": [
    {
      "id": "GHSA-jfh8-c2jp-5v3q",
      "summary": "Remote code execution in Apache Log4j",
      "details": "Log4j2 JNDI features do not protect against attacker controlled LDAP.",
      "aliases": ["CVE-2021-44228", "GHSA-jfh8-c2jp-5v3q"],
      "severity": [
        {"type": "CVSS_V3", "score": "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"}
      ],
      "database_specific": {"severity": "CRITICAL", "cwe_ids": ["CWE-502", "CWE-400"]},
      "affected": [
        {
          "package": {"name": "log4j", "ecosystem": "PyPI"},
          "ranges": [
            {"type": "ECOSYSTEM", "events": [{"introduced": "0"}, {"fixed": "2.15.0"}]}
          ]
        }
      ],
      "references": [
        {"type": "ADVISORY", "url": "https://github.com/advisories/GHSA-jfh8-c2jp-5v3q"}
      ]
    },
    {
      "id": "PYSEC-2021-835",
      "summary": "Apache Log4j2 RCE via JNDI",
      "aliases": ["CVE-2021-44228"],
      "severity": [
        {"type": "CVSS_V3", "score": "CVSS:3.1/AV:N/AC:L/PR:N/UI:R/S:U/C:L/I:N/A:N"}
      ],
      "affected": [
        {
          "package": {"name": "log4j", "ecosystem": "PyPI"},
          "ranges": [
            {"type": "ECOSYSTEM", "events": [{"introduced": "2.0"}, {"fixed": "2.15.0"}]}
          ]
        }
      ],
      "references": [
        {"type": "WEB", "url": "https://nvd.nist.gov/vuln/detail/CVE-2021-44228"}
      ]
    }
  ]
}`

func TestNewAppliesTheDefaultOptions(t *testing.T) {
	c := New(Options{})
	if c.opts.BaseURL != "https://api.osv.dev" {
		t.Errorf("BaseURL = %q", c.opts.BaseURL)
	}
	if c.opts.Timeout != 30*time.Second {
		t.Errorf("Timeout = %v", c.opts.Timeout)
	}
	if c.opts.UserAgent != "AISAI/0.1" {
		t.Errorf("UserAgent = %q", c.opts.UserAgent)
	}
}

func TestOfflineModeIsAPropertyOfTheConfiguration(t *testing.T) {
	db := writeOfflineDatabase(t, map[string]string{"requests": offlineRecord})

	if !New(Options{OfflineDBPath: db}).Offline() {
		t.Error("Offline() must be true when a database path is configured")
	}

	// A configured but missing database must NOT fall back to the network.
	//
	// The mode used to be "a path was configured AND the directory exists",
	// so a missing or mistyped database path was not reported as an error
	// and the client quietly started querying api.osv.dev. That is exactly
	// the leak the offline mode exists to prevent: the operator asked for a
	// local snapshot, and the tool reached out to the internet without
	// saying so.
	missing := filepath.Join(t.TempDir(), "no-such-db")
	if !New(Options{OfflineDBPath: missing}).Offline() {
		t.Error("Offline() must stay true when the configured database is missing")
	}
	if New(Options{}).Offline() {
		t.Error("Offline() must be false when no database path is configured")
	}
}

// TestAMissingOfflineDatabaseIsAnError pins that a configured but missing
// database fails loudly instead of answering "no known vulnerabilities".
//
// An empty result is a claim: it says the package is unaffected. A missing
// database says nothing at all, and the difference matters to whoever reads
// the report.
func TestAMissingOfflineDatabaseIsAnError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-db")
	c := New(Options{OfflineDBPath: missing})

	if _, err := c.Query(context.Background(), "requests", "2.31.0"); err == nil {
		t.Fatal("a query against a missing offline database must fail")
	}

	st := c.Status()
	if st.Mode != "offline" {
		t.Errorf("Mode = %q, want %q", st.Mode, "offline")
	}
	if st.Available {
		t.Error("a missing offline database must not be reported as available")
	}
	if st.Error == "" {
		t.Error("the status must say why the source is not available")
	}
}

func TestOfflineQueryReturnsTheMatchingAdvisory(t *testing.T) {
	db := writeOfflineDatabase(t, map[string]string{"requests": offlineRecord})
	c := New(Options{OfflineDBPath: db})

	advs, err := c.Query(context.Background(), "requests", "2.30.0")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(advs) != 1 {
		t.Fatalf("got %d advisories, want 1: %+v", len(advs), advs)
	}
	adv := advs[0]
	if adv.ID != "CVE-2023-32681" {
		t.Errorf("ID = %q, want the CVE alias %q", adv.ID, "CVE-2023-32681")
	}
	if adv.Source != "osv" {
		t.Errorf("Source = %q, want %q", adv.Source, "osv")
	}
	if !strings.Contains(adv.Summary, "Proxy-Authorization") {
		t.Errorf("Summary = %q", adv.Summary)
	}
	if adv.Severity != model.SeverityMedium {
		t.Errorf("Severity = %q, want %q", adv.Severity, model.SeverityMedium)
	}
	if len(adv.CWE) != 1 || adv.CWE[0] != "CWE-200" {
		t.Errorf("CWE = %v, want [CWE-200]", adv.CWE)
	}
	if len(adv.FixedIn) != 1 || adv.FixedIn[0] != "2.31.0" {
		t.Errorf("FixedIn = %v, want [2.31.0]", adv.FixedIn)
	}
	if adv.URL != "https://github.com/psf/requests/security/advisories/GHSA-j8r5-328x-9v2m" {
		t.Errorf("URL = %q", adv.URL)
	}
}

func TestOfflineQueryExcludesTheFixedVersions(t *testing.T) {
	db := writeOfflineDatabase(t, map[string]string{"requests": offlineRecord})
	c := New(Options{OfflineDBPath: db})

	advs, err := c.Query(context.Background(), "requests", "2.31.0")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(advs) != 0 {
		t.Fatalf("got %d advisories, want 0 (the version is fixed): %+v", len(advs), advs)
	}
}

func TestOfflineQueryReturnsNothingForAnUnknownPackage(t *testing.T) {
	db := writeOfflineDatabase(t, map[string]string{"requests": offlineRecord})
	c := New(Options{OfflineDBPath: db})

	advs, err := c.Query(context.Background(), "no-such-package", "1.0.0")
	if err != nil {
		t.Fatalf("an unknown package must not be an error: %v", err)
	}
	if len(advs) != 0 {
		t.Fatalf("got %d advisories, want 0", len(advs))
	}
}

func TestQueryWithAnEmptyVersionReturnsNothing(t *testing.T) {
	db := writeOfflineDatabase(t, map[string]string{"requests": offlineRecord})
	c := New(Options{OfflineDBPath: db})

	advs, err := c.Query(context.Background(), "requests", "")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(advs) != 0 {
		t.Fatalf("got %d advisories, want 0", len(advs))
	}
}

func TestOfflineStatusReportsTheDatabaseContents(t *testing.T) {
	db := writeOfflineDatabase(t, map[string]string{"requests": offlineRecord})
	c := New(Options{OfflineDBPath: db})

	st := c.Status()
	if st.Mode != "offline" {
		t.Errorf("Mode = %q, want %q", st.Mode, "offline")
	}
	if !st.Available {
		t.Error("Available must be true in offline mode")
	}
	// The count is the number of advisory records, not the number of files.
	if st.Count != 2 {
		t.Errorf("Count = %d, want 2", st.Count)
	}
	if st.UpdatedAt.IsZero() {
		t.Error("UpdatedAt must not be zero in offline mode")
	}
}

func TestOfflineStatusIgnoresAnUnreadableRecord(t *testing.T) {
	db := writeOfflineDatabase(t, map[string]string{
		"requests": offlineRecord,
		"broken":   "{not json",
	})
	c := New(Options{OfflineDBPath: db})

	st := c.Status()
	if st.Count != 2 {
		t.Fatalf("Count = %d, want 2 (the broken record must be skipped)", st.Count)
	}
	if !st.Available {
		t.Error("Available must stay true when one record is unreadable")
	}
}

func TestOnlineStatusReportsTheOnlineMode(t *testing.T) {
	c := New(Options{})

	st := c.Status()
	if st.Mode != "online" {
		t.Errorf("Mode = %q, want %q", st.Mode, "online")
	}
	if !st.Available {
		t.Error("Available must be true in online mode")
	}
	if st.Count != 0 {
		t.Errorf("Count = %d, want 0 in online mode", st.Count)
	}
	if !st.UpdatedAt.IsZero() {
		t.Errorf("UpdatedAt = %v, want the zero time in online mode", st.UpdatedAt)
	}
}

// TestOnlineStatusOfAMissingDatabaseIsOnline pins the behaviour described in
// TestOfflineModeIsAPropertyOfTheConfiguration: a configured but missing
// database leaves the client **offline** and unavailable, so the status
// reports the offline mode and explains itself.
func TestOnlineStatusOfAMissingDatabaseIsOnline(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-db")
	c := New(Options{OfflineDBPath: missing})

	st := c.Status()
	if st.Mode != "offline" {
		t.Fatalf("Mode = %q, want %q", st.Mode, "offline")
	}
	if st.Available {
		t.Error("a missing offline database must not be reported as available")
	}
	if st.Error == "" {
		t.Error("the status must say why the source is not available")
	}
}

func TestOnlineQuerySendsTheOSVRequest(t *testing.T) {
	var gotPath, gotMethod, gotContentType, gotUserAgent string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		gotUserAgent = r.Header.Get("User-Agent")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"vulns":[]}`))
	}))
	defer srv.Close()

	c := New(Options{BaseURL: srv.URL, UserAgent: "AISAI/test"})
	if _, err := c.Query(context.Background(), "requests", "2.30.0"); err != nil {
		t.Fatalf("Query: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/v1/query" {
		t.Errorf("path = %q, want /v1/query", gotPath)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q", gotContentType)
	}
	if gotUserAgent != "AISAI/test" {
		t.Errorf("User-Agent = %q", gotUserAgent)
	}
	pkg, _ := gotBody["package"].(map[string]any)
	if pkg == nil {
		t.Fatalf("the request has no package object: %v", gotBody)
	}
	if pkg["name"] != "requests" {
		t.Errorf("package.name = %v", pkg["name"])
	}
	if pkg["ecosystem"] != "PyPI" {
		t.Errorf("package.ecosystem = %v, want PyPI", pkg["ecosystem"])
	}
	if gotBody["version"] != "2.30.0" {
		t.Errorf("version = %v", gotBody["version"])
	}
}

func TestOnlineQueryParsesAndNormalisesTheResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(osvResponseFixture))
	}))
	defer srv.Close()

	c := New(Options{BaseURL: srv.URL})
	advs, err := c.Query(context.Background(), "log4j", "2.14.1")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(advs) != 2 {
		t.Fatalf("got %d advisories, want 2: %+v", len(advs), advs)
	}

	first := advs[0]
	if first.ID != "CVE-2021-44228" {
		t.Errorf("ID = %q, want the CVE alias %q", first.ID, "CVE-2021-44228")
	}
	if first.Source != "osv" {
		t.Errorf("Source = %q, want %q", first.Source, "osv")
	}
	if first.Severity != model.SeverityCritical {
		t.Errorf("Severity = %q, want %q", first.Severity, model.SeverityCritical)
	}
	if len(first.CWE) != 2 || first.CWE[0] != "CWE-502" || first.CWE[1] != "CWE-400" {
		t.Errorf("CWE = %v, want [CWE-502 CWE-400]", first.CWE)
	}
	if len(first.FixedIn) != 1 || first.FixedIn[0] != "2.15.0" {
		t.Errorf("FixedIn = %v, want [2.15.0]", first.FixedIn)
	}
	if first.URL != "https://github.com/advisories/GHSA-jfh8-c2jp-5v3q" {
		t.Errorf("URL = %q", first.URL)
	}

	second := advs[1]
	if second.ID != "CVE-2021-44228" {
		t.Errorf("ID = %q, want the CVE alias %q", second.ID, "CVE-2021-44228")
	}
	if second.Severity != model.SeverityMedium {
		t.Errorf("Severity = %q, want %q", second.Severity, model.SeverityMedium)
	}
}

// TestOnlineQueryNormalisesAliasedRecordsToTheSameID pins the behaviour the
// dependency graph depends on.
//
// OSV returns the same vulnerability twice: once as a GHSA record and once as
// a PYSEC record, both aliased to the same CVE. The advisory client does not
// merge them — the de-duplication happens one layer up, in
// internal/audit.FetchAdvisories, which drops advisories with an
// already-seen id. What this package guarantees is that both records carry
// the same id, so that the de-duplication actually collapses them and the
// graph's vulnerability count matches the advisory list.
//
// If this test fails, the graph would count the same vulnerability twice.
func TestOnlineQueryNormalisesAliasedRecordsToTheSameID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(osvResponseFixture))
	}))
	defer srv.Close()

	c := New(Options{BaseURL: srv.URL})
	advs, err := c.Query(context.Background(), "log4j", "2.14.1")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(advs) != 2 {
		t.Fatalf("got %d advisories, want 2", len(advs))
	}
	if advs[0].ID != advs[1].ID {
		t.Fatalf("the aliased records have different ids: %q and %q", advs[0].ID, advs[1].ID)
	}
	if advs[0].ID != "CVE-2021-44228" {
		t.Fatalf("ID = %q, want CVE-2021-44228", advs[0].ID)
	}
}

func TestOnlineQueryReportsAnHTTPFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("upstream exploded"))
	}))
	defer srv.Close()

	c := New(Options{BaseURL: srv.URL})
	_, err := c.Query(context.Background(), "requests", "2.30.0")
	if err == nil {
		t.Fatal("Query must fail on a 500 response")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("error = %v, want it to mention the status", err)
	}
}

func TestOnlineQueryReportsAMalformedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{not json"))
	}))
	defer srv.Close()

	c := New(Options{BaseURL: srv.URL})
	if _, err := c.Query(context.Background(), "requests", "2.30.0"); err == nil {
		t.Fatal("Query must fail on a malformed response")
	}
}

func TestConvertKeepsTheOSVIdWhenThereIsNoAlias(t *testing.T) {
	out := convert([]osvVuln{{ID: "GHSA-abcd-1234-5678", Summary: "a problem"}})
	if len(out) != 1 {
		t.Fatalf("got %d advisories, want 1", len(out))
	}
	if out[0].ID != "GHSA-abcd-1234-5678" {
		t.Fatalf("ID = %q", out[0].ID)
	}
}

func TestConvertFallsBackToTheDetailsAndThenToTheID(t *testing.T) {
	out := convert([]osvVuln{{ID: "OSV-1", Details: "the details"}})
	if out[0].Summary != "the details" {
		t.Errorf("Summary = %q, want the details", out[0].Summary)
	}
	out = convert([]osvVuln{{ID: "OSV-1"}})
	if out[0].Summary != "OSV-1" {
		t.Errorf("Summary = %q, want the id", out[0].Summary)
	}
}

func TestSeverityOfNormalisesTheOSVForms(t *testing.T) {
	cases := []struct {
		name string
		vuln osvVuln
		want model.Severity
	}{
		{
			name: "database_specific label",
			vuln: osvVuln{DatabaseSpecific: map[string]any{"severity": "HIGH"}},
			want: model.SeverityHigh,
		},
		{
			name: "database_specific moderate",
			vuln: osvVuln{DatabaseSpecific: map[string]any{"severity": "MODERATE"}},
			want: model.SeverityMedium,
		},
		{
			name: "cvss vector",
			vuln: osvVuln{Severity: []osvSeverity{{Type: "CVSS_V3", Score: "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"}}},
			want: model.SeverityCritical,
		},
		{
			name: "bare score",
			vuln: osvVuln{Severity: []osvSeverity{{Type: "CVSS_V3", Score: "5.0"}}},
			want: model.SeverityMedium,
		},
		{
			name: "nothing at all",
			vuln: osvVuln{},
			want: model.SeverityMedium,
		},
		{
			name: "unknown label falls through to the score",
			vuln: osvVuln{
				DatabaseSpecific: map[string]any{"severity": "IMPORTANT"},
				Severity:         []osvSeverity{{Type: "CVSS_V3", Score: "7.5"}},
			},
			want: model.SeverityHigh,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := severityOf(tc.vuln); got != tc.want {
				t.Fatalf("severityOf = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestParseCVSSScore pins the CVSS v3.1 base score computation.
//
// The specification rounds the base score UP to one decimal place, so the
// vector CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H scores 9.8 and
// CVSS:3.1/AV:N/AC:L/PR:N/UI:R/S:U/C:L/I:N/A:N scores 4.3. Both values are
// the ones the official calculator produces.
//
// The implementation used to round up to five decimal places instead, which
// returned 9.76017 and 4.24766. The difference matters: a vector whose true
// score is 6.96 is a **high** severity vulnerability and 6.9 is a medium
// one, so the rounding decides how a finding is presented.
func TestParseCVSSScore(t *testing.T) {
	cases := []struct {
		score string
		want  float64
		ok    bool
	}{
		{"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H", 9.8, true},
		{"CVSS:3.1/AV:N/AC:L/PR:N/UI:R/S:U/C:L/I:N/A:N", 4.3, true},
		{"CVSS:3.1/AV:L/AC:H/PR:H/UI:R/S:U/C:N/I:N/A:N", 0.0, true},
		{"7.5", 7.5, true},
		{"", 0, false},
		{"not a score", 0, false},
	}
	for _, tc := range cases {
		got, ok := parseCVSSScore(tc.score)
		if ok != tc.ok {
			t.Errorf("parseCVSSScore(%q) ok = %v, want %v", tc.score, ok, tc.ok)
			continue
		}
		if ok && !closeEnough(got, tc.want) {
			t.Errorf("parseCVSSScore(%q) = %v, want %v", tc.score, got, tc.want)
		}
	}
}

// TestCVSSRoundUpNeverLowersTheScore pins the Roundup function itself.
//
// Roundup must never return a value below its input, and it must land on one
// decimal place.
func TestCVSSRoundUpNeverLowersTheScore(t *testing.T) {
	cases := []struct {
		in   float64
		want float64
	}{
		{0, 0},
		{0.0, 0.0},
		{0.01, 0.1},
		{0.04, 0.1},
		{1.0, 1.0},
		{1.05, 1.1},
		{4.24766, 4.3},
		{6.96, 7.0},
		{6.94, 7.0},
		{9.76017, 9.8},
		{9.8, 9.8},
		{10, 10},
	}
	for _, tc := range cases {
		got := roundUp(tc.in)
		if got < tc.in {
			t.Errorf("roundUp(%v) = %v, which is below the input", tc.in, got)
		}
		if !closeEnough(got, tc.want) {
			t.Errorf("roundUp(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// closeEnough compares two scores with a tolerance of one unit in the last
// place of the fifth decimal, which is the precision the implementation
// produces.
func closeEnough(a, b float64) bool {
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff < 1e-5
}

func TestSeverityFromScore(t *testing.T) {
	cases := []struct {
		score float64
		want  model.Severity
	}{
		{9.8, model.SeverityCritical},
		{9.0, model.SeverityCritical},
		{7.0, model.SeverityHigh},
		{6.9, model.SeverityMedium},
		{4.0, model.SeverityMedium},
		{3.9, model.SeverityLow},
		{0.1, model.SeverityLow},
		{0, model.SeverityInfo},
	}
	for _, tc := range cases {
		if got := severityFromScore(tc.score); got != tc.want {
			t.Errorf("severityFromScore(%v) = %q, want %q", tc.score, got, tc.want)
		}
	}
}

func TestAffectsVersion(t *testing.T) {
	withRange := osvVuln{Affected: []osvAffected{{
		Package: osvPackage{Name: "requests", Ecosystem: "PyPI"},
		Ranges: []osvRange{{Type: "ECOSYSTEM", Events: []osvRangeEvent{
			{Introduced: "0"}, {Fixed: "2.31.0"},
		}}},
	}}}
	withVersions := osvVuln{Affected: []osvAffected{{
		Package:  osvPackage{Name: "requests", Ecosystem: "PyPI"},
		Versions: []string{"1.0.0"},
	}}}
	otherRangeType := osvVuln{Affected: []osvAffected{{
		Package: osvPackage{Name: "requests", Ecosystem: "PyPI"},
		Ranges:  []osvRange{{Type: "GIT", Events: []osvRangeEvent{{Introduced: "0"}}}},
	}}}

	cases := []struct {
		name    string
		vuln    osvVuln
		version string
		want    bool
	}{
		{"no affected section matches everything", osvVuln{}, "1.0.0", true},
		{"before the fix", withRange, "2.30.0", true},
		{"at the fix", withRange, "2.31.0", false},
		{"after the fix", withRange, "3.0.0", false},
		{"exact version list", withVersions, "1.0.0", true},
		{"missing from the version list", withVersions, "1.0.1", false},
		{"an unrelated range type never matches", otherRangeType, "1.0.0", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := affectsVersion(tc.vuln, tc.version); got != tc.want {
				t.Fatalf("affectsVersion = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.1", "1.0.0", 1},
		{"1.0.0", "1.0.1", -1},
		{"2.0.0", "1.9.9", 1},
		{"1.0", "1.0.0", 0},
		{"2.14.1", "2.15.0", -1},
		{"1.0.0rc1", "1.0.0", 0},
		{"1!1.0.0", "1.0.0", 0},
		{"1.0.0+local", "1.0.0", 0},
	}
	for _, tc := range cases {
		if got := compareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestReferenceURLPrefersTheAdvisoryReference(t *testing.T) {
	v := osvVuln{References: []osvReference{
		{Type: "WEB", URL: "https://example.com/web"},
		{Type: "ADVISORY", URL: "https://example.com/advisory"},
	}}
	if got := referenceURL(v); got != "https://example.com/web" {
		t.Fatalf("referenceURL = %q, want the first usable reference", got)
	}
	if got := referenceURL(osvVuln{}); got != "" {
		t.Fatalf("referenceURL = %q, want an empty string", got)
	}
}

func TestToStringSlice(t *testing.T) {
	if got := toStringSlice([]any{"a", "b"}); len(got) != 2 || got[0] != "a" {
		t.Fatalf("toStringSlice = %v", got)
	}
	if got := toStringSlice([]string{"a"}); len(got) != 1 || got[0] != "a" {
		t.Fatalf("toStringSlice = %v", got)
	}
	if got := toStringSlice("a"); len(got) != 1 || got[0] != "a" {
		t.Fatalf("toStringSlice = %v", got)
	}
	if got := toStringSlice(42); got != nil {
		t.Fatalf("toStringSlice = %v, want nil", got)
	}
}
