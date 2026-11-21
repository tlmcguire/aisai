package deps

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tlmcguire/aisai/internal/model"
	"github.com/tlmcguire/aisai/internal/pypi"
)

// newResolver returns a resolver wired to the given test servers.
func newResolver(depsURL, pypiURL string) *Resolver {
	return NewResolver(Options{
		BaseURL:   depsURL,
		Timeout:   5 * time.Second,
		UserAgent: "aisai-test/1.0",
		PyPI:      pypi.NewClient(pypi.ClientOptions{BaseURL: pypiURL, Timeout: 5 * time.Second}),
	})
}

// findPackage returns the package with the given name and version.
//
// The name comparison is case-insensitive because PyPI names are.
func findPackage(packages []model.Package, name, version string) *model.Package {
	for i := range packages {
		if strings.EqualFold(packages[i].Name, name) && packages[i].Version == version {
			return &packages[i]
		}
	}
	return nil
}

// hasEdge reports whether the edge exists in the resolution.
func hasEdge(edges []model.GraphEdge, source, target model.PURL) bool {
	for _, e := range edges {
		if e.Source == source && e.Target == target {
			return true
		}
	}
	return false
}

// TestResolveFromDepsDev verifies the primary resolution path.
func TestResolveFromDepsDev(t *testing.T) {
	depsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/v3/systems/PYPI/packages/flask/versions/2.0.0:dependencies"; got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "nodes": [
    {
      "versionKey": {"system": "PYPI", "name": "flask", "version": "2.0.0"},
      "nodes": [
        {"versionKey": {"system": "PYPI", "name": "werkzeug", "version": "2.0.0"}, "requirement": "Werkzeug"},
        {"versionKey": {"system": "PYPI", "name": "click", "version": "8.0.0"}, "requirement": "click"},
        {"versionKey": {"system": "PYPI", "name": "click", "version": "7.0.0"}, "requirement": "click"}
      ],
      "edges": [
        {"from": {"versionKey": {"system": "PYPI", "name": "flask", "version": "2.0.0"}},
         "to": {"versionKey": {"system": "PYPI", "name": "werkzeug", "version": "2.0.0"}},
         "requirement": "Werkzeug"}
      ]
    },
    {
      "versionKey": {"system": "PYPI", "name": "werkzeug", "version": "2.0.0"},
      "nodes": [
        {"versionKey": {"system": "PYPI", "name": "markupsafe", "version": "2.0.0"}, "requirement": "MarkupSafe"}
      ],
      "edges": []
    }
  ]
}`))
	}))
	defer depsSrv.Close()

	pypiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer pypiSrv.Close()

	res, err := newResolver(depsSrv.URL, pypiSrv.URL).Resolve(context.Background(), "flask", "2.0.0")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Source != SourceDepsDev {
		t.Fatalf("source = %q, want %q", res.Source, SourceDepsDev)
	}

	root := findPackage(res.Packages, "flask", "2.0.0")
	if root == nil {
		t.Fatalf("the root package is missing: %+v", res.Packages)
	}
	if !root.Direct {
		t.Fatalf("the root package is not marked as a direct dependency")
	}
	if root.Ecosystem != "pypi" {
		t.Fatalf("ecosystem = %q, want pypi", root.Ecosystem)
	}
	if root.MaxSeverity != model.SeverityInfo {
		t.Fatalf("max severity = %q, want %q", root.MaxSeverity, model.SeverityInfo)
	}
	if root.PURL != "pkg:pypi/flask@2.0.0" {
		t.Fatalf("unexpected PURL %q", root.PURL)
	}

	for _, dep := range []struct{ name, version string }{
		{"werkzeug", "2.0.0"},
		{"markupsafe", "2.0.0"},
		{"click", "8.0.0"},
		{"click", "7.0.0"},
	} {
		pkg := findPackage(res.Packages, dep.name, dep.version)
		if pkg == nil {
			t.Fatalf("dependency %s@%s is missing", dep.name, dep.version)
		}
		if pkg.Direct {
			t.Fatalf("dependency %s@%s is marked as direct", dep.name, dep.version)
		}
		if pkg.PURL != model.PURL(fmt.Sprintf("pkg:pypi/%s@%s", dep.name, dep.version)) {
			t.Fatalf("unexpected PURL %q", pkg.PURL)
		}
	}

	if !hasEdge(res.Edges, "pkg:pypi/flask@2.0.0", "pkg:pypi/werkzeug@2.0.0") {
		t.Fatalf("the flask -> werkzeug edge is missing: %+v", res.Edges)
	}
	if !hasEdge(res.Edges, "pkg:pypi/werkzeug@2.0.0", "pkg:pypi/markupsafe@2.0.0") {
		t.Fatalf("the werkzeug -> markupsafe edge is missing: %+v", res.Edges)
	}

	if len(res.Conflicts) != 1 || !strings.Contains(res.Conflicts[0], "click") {
		t.Fatalf("the version conflict was not reported: %v", res.Conflicts)
	}
}

// TestResolveFallsBackToPyPIMetadata verifies the fallback resolution path.
func TestResolveFallsBackToPyPIMetadata(t *testing.T) {
	depsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer depsSrv.Close()

	pypiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch strings.ToLower(r.URL.Path) {
		case "/pypi/flask/2.0.0/json":
			_, _ = w.Write([]byte(`{
  "info": {"name": "flask", "version": "2.0.0", "summary": "A web framework",
           "license": "BSD-3-Clause", "home_page": "https://flask.test/",
           "requires_dist": ["Werkzeug (>=2.0)", "click>=8.0", "six ; python_version < \"3.9\"",
                             "rich[jupyter] ; extra == \"jupyter\""]},
  "urls": [], "releases": {"2.0.0": []}
}`))
		case "/pypi/werkzeug/json":
			_, _ = w.Write([]byte(`{"info": {"name": "werkzeug"}, "releases": {"2.0.0": [], "2.1.0": []}}`))
		case "/pypi/werkzeug/2.1.0/json":
			_, _ = w.Write([]byte(`{"info": {"name": "werkzeug", "version": "2.1.0", "requires_dist": ["MarkupSafe>=2.0"]},
  "urls": [], "releases": {"2.1.0": []}}`))
		case "/pypi/click/json":
			_, _ = w.Write([]byte(`{"info": {"name": "click"}, "releases": {"8.0.0": [], "8.1.0": []}}`))
		case "/pypi/click/8.1.0/json":
			_, _ = w.Write([]byte(`{"info": {"name": "click", "version": "8.1.0", "requires_dist": []}, "urls": [], "releases": {"8.1.0": []}}`))
		case "/pypi/markupsafe/json":
			_, _ = w.Write([]byte(`{"info": {"name": "markupsafe"}, "releases": {"2.1.0": []}}`))
		case "/pypi/markupsafe/2.1.0/json":
			_, _ = w.Write([]byte(`{"info": {"name": "markupsafe", "version": "2.1.0", "requires_dist": []}, "urls": [], "releases": {"2.1.0": []}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer pypiSrv.Close()

	res, err := newResolver(depsSrv.URL, pypiSrv.URL).Resolve(context.Background(), "flask", "2.0.0")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Source != SourcePyPIMetadata {
		t.Fatalf("source = %q, want %q", res.Source, SourcePyPIMetadata)
	}
	root := findPackage(res.Packages, "flask", "2.0.0")
	if root == nil || !root.Direct {
		t.Fatalf("the root package is missing: %+v", res.Packages)
	}
	if root.Summary != "A web framework" || root.License != "BSD-3-Clause" {
		t.Fatalf("the root metadata is missing: %+v", root)
	}
	if findPackage(res.Packages, "werkzeug", "2.1.0") == nil {
		t.Fatalf("werkzeug was not resolved: %+v", res.Packages)
	}
	if findPackage(res.Packages, "click", "8.1.0") == nil {
		t.Fatalf("click was not resolved: %+v", res.Packages)
	}
	if findPackage(res.Packages, "markupsafe", "2.1.0") == nil {
		t.Fatalf("markupsafe was not resolved: %+v", res.Packages)
	}
	if findPackage(res.Packages, "six", "") != nil {
		t.Fatalf("a requirement with a false marker was resolved: %+v", res.Packages)
	}
	if !hasEdge(res.Edges, "pkg:pypi/flask@2.0.0", "pkg:pypi/werkzeug@2.1.0") {
		t.Fatalf("the flask -> werkzeug edge is missing: %+v", res.Edges)
	}
	if !hasEdge(res.Edges, "pkg:pypi/werkzeug@2.1.0", "pkg:pypi/markupsafe@2.1.0") {
		t.Fatalf("the werkzeug -> markupsafe edge is missing: %+v", res.Edges)
	}
}

// TestResolveFailsWhenBothSourcesFail verifies that the resolver reports
// both failures instead of returning a partial tree.
func TestResolveFailsWhenBothSourcesFail(t *testing.T) {
	depsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer depsSrv.Close()

	pypiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer pypiSrv.Close()

	res, err := newResolver(depsSrv.URL, pypiSrv.URL).Resolve(context.Background(), "flask", "2.0.0")
	if err == nil {
		t.Fatalf("expected an error, got %+v", res)
	}
	if res != nil {
		t.Fatalf("a partial resolution was returned: %+v", res)
	}
	if !strings.Contains(err.Error(), "deps.dev") || !strings.Contains(err.Error(), "pypi metadata") {
		t.Fatalf("the error does not describe both failures: %v", err)
	}
}

// TestResolveRequiresName verifies the input validation.
func TestResolveRequiresName(t *testing.T) {
	r := NewResolver(Options{})
	if _, err := r.Resolve(context.Background(), "  ", "1.0"); err == nil {
		t.Fatalf("expected an error for an empty package name")
	}
}
