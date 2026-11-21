package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tlmcguire/aisai/internal/pypi"
)

// fakeIndex serves the two PyPI endpoints a lookup needs.
func fakeIndex(t *testing.T) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/pypi/flask/json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"info": map[string]any{
				"name":    "flask",
				"version": "3.1.0",
				"summary": "A simple framework for building complex web applications.",
			},
			"urls": []any{},
			"releases": map[string][]any{
				"3.1.0":   {map[string]any{"filename": "flask-3.1.0.tar.gz"}},
				"3.0.3":   {map[string]any{"filename": "flask-3.0.3.tar.gz"}},
				"2.3.3":   {map[string]any{"filename": "flask-2.3.3.tar.gz"}},
				"4.0.0b1": {map[string]any{"filename": "flask-4.0.0b1.tar.gz"}},
			},
		})
	})
	mux.HandleFunc("/pypi/flask/3.1.0/json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"info": map[string]any{
				"name":    "flask",
				"version": "3.1.0",
				"summary": "A simple framework for building complex web applications.",
			},
			"urls": []any{},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestPackageLookupEndpoint(t *testing.T) {
	s, h := newTestServer(t)
	index := fakeIndex(t)
	s.pypi = pypi.NewClient(pypi.ClientOptions{BaseURL: index.URL})

	rec, body := do(t, h, http.MethodGet, "/api/packages?name=flask", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	pkg, ok := body["package"].(map[string]any)
	if !ok {
		t.Fatalf("response is missing the package field: %v", body)
	}
	if pkg["name"] != "flask" {
		t.Errorf("name = %v, want flask", pkg["name"])
	}
	if pkg["latest"] != "3.1.0" {
		t.Errorf("latest = %v, want 3.1.0", pkg["latest"])
	}
	if pkg["summary"] == "" {
		t.Error("summary is empty")
	}
	versions, _ := pkg["versions"].([]any)
	if len(versions) == 0 {
		t.Fatal("no versions were reported")
	}
	if versions[0] != "3.1.0" {
		t.Errorf("versions[0] = %v, want 3.1.0", versions[0])
	}
}

func TestPackageLookupOrdersPreReleasesLast(t *testing.T) {
	s, h := newTestServer(t)
	index := fakeIndex(t)
	s.pypi = pypi.NewClient(pypi.ClientOptions{BaseURL: index.URL})

	rec, body := do(t, h, http.MethodGet, "/api/packages?name=flask", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	pkg := body["package"].(map[string]any)
	versions := pkg["versions"].([]any)
	if versions[len(versions)-1] != "4.0.0b1" {
		t.Errorf("the pre-release is not last: %v", versions)
	}
}

func TestPackageLookupReportsUnknownPackages(t *testing.T) {
	s, h := newTestServer(t)
	index := fakeIndex(t)
	s.pypi = pypi.NewClient(pypi.ClientOptions{BaseURL: index.URL})

	rec, body := do(t, h, http.MethodGet, "/api/packages?name=nosuchpackage", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	pkg := body["package"].(map[string]any)
	if pkg["error"] == nil || pkg["error"] == "" {
		t.Error("an unknown package must be reported in the error field")
	}
}

func TestPackageLookupRequiresAName(t *testing.T) {
	_, h := newTestServer(t)
	rec, _ := do(t, h, http.MethodGet, "/api/packages", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestDeleteWorkspaceEndpoint(t *testing.T) {
	_, h := newTestServer(t)

	rec, body := do(t, h, http.MethodDelete, "/api/audits/missing/workspace", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if body == nil {
		t.Error("the response body is empty")
	}
}
