package deps

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tlmcguire/aisai/internal/model"
)

// TestFlatDepsDevShape parses the real shape of the deps.dev
// ":dependencies" response.
//
// This is a regression test. The endpoint returns a FLAT graph: every node
// is listed once with a "relation" of SELF, DIRECT or INDIRECT, and the
// edges are a separate top-level array referring to the endpoints by their
// index in the nodes array. A parser written for the nested graph shape
// silently returns only the root package, because it finds no edges at all.
//
// The fixture below is a verbatim capture of the real response for
// flask 2.0.0.
const realDepsDevResponse = `{
  "nodes": [
    {"versionKey": {"system": "PYPI", "name": "flask", "version": "2.0.0"}, "bundled": false, "relation": "SELF", "errors": []},
    {"versionKey": {"system": "PYPI", "name": "click", "version": "8.5.1"}, "bundled": false, "relation": "DIRECT", "errors": []},
    {"versionKey": {"system": "PYPI", "name": "itsdangerous", "version": "2.2.0"}, "bundled": false, "relation": "DIRECT", "errors": []},
    {"versionKey": {"system": "PYPI", "name": "jinja2", "version": "3.1.6"}, "bundled": false, "relation": "DIRECT", "errors": []},
    {"versionKey": {"system": "PYPI", "name": "markupsafe", "version": "3.0.4"}, "bundled": false, "relation": "INDIRECT", "errors": []},
    {"versionKey": {"system": "PYPI", "name": "werkzeug", "version": "3.1.9"}, "bundled": false, "relation": "DIRECT", "errors": []}
  ],
  "edges": [
    {"fromNode": 0, "toNode": 1, "requirement": ">=7.1.2"},
    {"fromNode": 0, "toNode": 2, "requirement": ">=2.0"},
    {"fromNode": 0, "toNode": 3, "requirement": ">=3.0"},
    {"fromNode": 0, "toNode": 5, "requirement": ">=2.0"},
    {"fromNode": 3, "toNode": 4, "requirement": ">=2.0"},
    {"fromNode": 5, "toNode": 4, "requirement": ">=2.1.1"}
  ],
  "error": ""
}`

func TestFlatDepsDevShapeParsesTransitiveTree(t *testing.T) {
	var payload depsResponse
	if err := json.Unmarshal([]byte(realDepsDevResponse), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	res, err := resolutionFromDepsResponse(&payload, "flask", "2.0.0")
	if err != nil {
		t.Fatalf("resolution: %v", err)
	}

	if len(res.Packages) != 6 {
		t.Fatalf("got %d packages, want 6 (the transitive tree was dropped)", len(res.Packages))
	}
	if len(res.Edges) != 6 {
		t.Fatalf("got %d edges, want 6", len(res.Edges))
	}

	byName := map[string]model.Package{}
	for _, p := range res.Packages {
		byName[p.Name] = p
	}

	root, ok := byName["flask"]
	if !ok {
		t.Fatal("the root package is missing")
	}
	if !root.Direct {
		t.Error("the root package must be marked direct")
	}

	// flask's immediate dependencies carry relation DIRECT.
	for _, name := range []string{"click", "itsdangerous", "jinja2", "werkzeug"} {
		p, ok := byName[name]
		if !ok {
			t.Errorf("%s is missing from the resolution", name)
			continue
		}
		if !p.Direct {
			t.Errorf("%s should be marked as a direct dependency", name)
		}
	}

	// markupsafe is only reachable through jinja2 and werkzeug.
	ms, ok := byName["markupsafe"]
	if !ok {
		t.Fatal("markupsafe is missing from the resolution")
	}
	if ms.Direct {
		t.Error("markupsafe is transitive and must not be marked direct")
	}

	// The edge from flask to click must exist.
	found := false
	for _, e := range res.Edges {
		if e.Source.Name() == "flask" && e.Target.Name() == "click" {
			found = true
		}
	}
	if !found {
		t.Error("the flask -> click edge is missing")
	}
}

func TestFlatDepsDevOverHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/v3/systems/PYPI/packages/flask/versions/2.0.0:dependencies"; got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(realDepsDevResponse))
	}))
	defer srv.Close()

	r := NewResolver(Options{BaseURL: srv.URL})
	res, err := r.Resolve(context.Background(), "flask", "2.0.0")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(res.Packages) != 6 {
		t.Fatalf("got %d packages, want 6", len(res.Packages))
	}
	if len(res.Edges) != 6 {
		t.Fatalf("got %d edges, want 6", len(res.Edges))
	}
}
