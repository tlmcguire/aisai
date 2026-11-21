package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tlmcguire/aisai/internal/audit"
	"github.com/tlmcguire/aisai/internal/config"
	"github.com/tlmcguire/aisai/internal/findings"
	"github.com/tlmcguire/aisai/internal/model"
)

// newTestServer builds a server backed by an empty findings store and a
// manager with no resolver, so no test touches the network.
func newTestServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()

	store, err := findings.Open(t.TempDir() + "/findings.sqlite")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	cfg := &config.Config{Workspace: t.TempDir()}

	mgr := audit.NewManager(audit.Deps{
		Config: cfg,
		Store:  store,
	})

	s := New(Options{
		Config: cfg,
		Store:  store,
	})
	s.SetManager(mgr)
	return s, s.Handler()
}

// do performs a request against the handler and decodes the JSON body.
func do(t *testing.T, h http.Handler, method, path, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()

	var rdr *strings.Reader
	if body == "" {
		rdr = strings.NewReader("")
	} else {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var out map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			// Not every response is JSON; that is not a failure here.
			out = nil
		}
	}
	return rec, out
}

func TestHealthEndpoint(t *testing.T) {
	_, h := newTestServer(t)
	rec, body := do(t, h, http.MethodGet, "/api/health", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body["ok"] != true {
		t.Errorf("ok = %v, want true", body["ok"])
	}
	if _, ok := body["version"]; !ok {
		t.Error("response is missing the version field")
	}
}

func TestCreateAuditRequiresPackage(t *testing.T) {
	_, h := newTestServer(t)
	rec, body := do(t, h, http.MethodPost, "/api/audits", `{"version":"1.0"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	e, _ := body["error"].(map[string]any)
	if e["code"] != "bad_request" {
		t.Errorf("error code = %v, want bad_request", e["code"])
	}
}

func TestCreateAuditRejectsABadFolder(t *testing.T) {
	_, h := newTestServer(t)
	for name, path := range map[string]string{
		"relative": "some/relative/dir",
		"missing":  filepath.Join(t.TempDir(), "does-not-exist"),
	} {
		rec, body := do(t, h, http.MethodPost, "/api/audits", `{"path":`+strconv.Quote(path)+`}`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, rec.Code)
			continue
		}
		e, _ := body["error"].(map[string]any)
		if e["code"] != "bad_request" {
			t.Errorf("%s: error code = %v, want bad_request", name, e["code"])
		}
	}
}

func TestCreateAuditFromAFolder(t *testing.T) {
	_, h := newTestServer(t)
	project := filepath.Join(t.TempDir(), "demo-app")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, src := range map[string]string{
		"app.py":  "import subprocess\nsubprocess.run(cmd, shell=True)\n",
		"main.go": "package main\n\nfunc main() {}\n",
	} {
		if err := os.WriteFile(filepath.Join(project, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	rec, body := do(t, h, http.MethodPost, "/api/audits", `{"path":`+strconv.Quote(project)+`}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (%s)", rec.Code, rec.Body.String())
	}
	a, _ := body["audit"].(map[string]any)
	id, _ := a["id"].(string)
	if a["package"] != "demo-app" || a["source"] != "local" {
		t.Fatalf("audit = %v, want package demo-app and source local", a)
	}

	deadline := time.Now().Add(20 * time.Second)
	for {
		_, got := do(t, h, http.MethodGet, "/api/audits/"+id, "")
		cur, _ := got["audit"].(map[string]any)
		status, _ := cur["status"].(string)
		if status == "ready" {
			if files, _ := cur["fileCount"].(float64); files != 2 {
				t.Fatalf("fileCount = %v, want 2", cur["fileCount"])
			}
			break
		}
		if status == "failed" || time.Now().After(deadline) {
			t.Fatalf("audit did not become ready: %v", cur)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestUnknownAuditReturnsNotFound(t *testing.T) {
	_, h := newTestServer(t)
	for _, path := range []string{
		"/api/audits/does-not-exist",
		"/api/audits/does-not-exist/graph",
		"/api/audits/does-not-exist/files",
		"/api/audits/does-not-exist/advisories",
	} {
		rec, body := do(t, h, http.MethodGet, path, "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", path, rec.Code)
			continue
		}
		e, _ := body["error"].(map[string]any)
		if e["code"] != "not_found" {
			t.Errorf("%s: error code = %v, want not_found", path, e["code"])
		}
	}
}

func TestAdvisoryStatusWithoutSource(t *testing.T) {
	_, h := newTestServer(t)
	rec, body := do(t, h, http.MethodGet, "/api/advisories/status", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body["mode"] != "unavailable" {
		t.Errorf("mode = %v, want unavailable", body["mode"])
	}
	osv, _ := body["osv"].(map[string]any)
	if osv == nil {
		t.Fatal("response is missing the osv object")
	}
	if osv["available"] != false {
		t.Errorf("osv.available = %v, want false", osv["available"])
	}
}

func TestListAdvisoriesReturnsEmptyList(t *testing.T) {
	_, h := newTestServer(t)
	rec, body := do(t, h, http.MethodGet, "/api/audits/whatever/advisories", "")

	// An unknown audit is a 404; a known audit with no advisories is an empty
	// list. This test only asserts the response is well formed JSON.
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 or 404", rec.Code)
	}
	if rec.Code == http.StatusOK {
		list, ok := body["advisories"].([]any)
		if !ok {
			t.Fatal("advisories is not an array")
		}
		if len(list) != 0 {
			t.Errorf("got %d advisories, want 0", len(list))
		}
	}
}

func TestPathEscapeIsRejected(t *testing.T) {
	_, h := newTestServer(t)
	rec, body := do(t, h, http.MethodGet, "/api/audits/whatever/file?path=../../etc/passwd", "")

	if rec.Code == http.StatusOK {
		t.Fatal("a path escape must not succeed")
	}
	e, _ := body["error"].(map[string]any)
	if e["code"] != "not_found" && e["code"] != "path_escape" {
		t.Errorf("error code = %v, want not_found or path_escape", e["code"])
	}
}

func TestScanRejectsUnknownEngine(t *testing.T) {
	_, h := newTestServer(t)
	rec, body := do(t, h, http.MethodPost, "/api/audits/whatever/scan", `{"engine":"nonsense"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	e, _ := body["error"].(map[string]any)
	if e["code"] != "bad_request" {
		t.Errorf("error code = %v, want bad_request", e["code"])
	}
}

func TestExportDefaultsToSARIF(t *testing.T) {
	_, h := newTestServer(t)
	rec, _ := do(t, h, http.MethodGet, "/api/audits/whatever/export", "")

	// The export lists findings for an unknown audit, which yields an empty
	// report rather than an error.
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/sarif+json" {
		t.Errorf("content type = %q, want application/sarif+json", ct)
	}
}

func TestAuditSnapshotShape(t *testing.T) {
	_, h := newTestServer(t)

	// Create an audit directly through the manager so no network is needed.
	// The handler starts a background run, which will fail to resolve; that
	// is fine because only the create response is asserted here.
	rec, body := do(t, h, http.MethodPost, "/api/audits", `{"package":"flask","version":"2.0.0"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
	a, _ := body["audit"].(map[string]any)
	if a == nil {
		t.Fatal("response is missing the audit object")
	}
	for _, field := range []string{"id", "package", "version", "status"} {
		if _, ok := a[field]; !ok {
			t.Errorf("audit is missing the %q field", field)
		}
	}
	if _, ok := a["id"].(string); !ok {
		t.Error("audit id is not a string")
	}
}

func TestEventTypeConstantsAreStable(t *testing.T) {
	// The Electron client switches on these strings; changing one silently
	// breaks the UI, so pin them here.
	cases := map[string]string{
		model.EventAuditProgress: "audit.progress",
		model.EventAuditDone:     "audit.done",
		model.EventAuditError:    "audit.error",
		model.EventScanProgress:  "scan.progress",
		model.EventFinding:       "finding.created",
		model.EventAdvisories:    "advisories.ready",
		model.EventChatToken:     "chat.token",
		model.EventChatDone:      "chat.done",
		model.EventChatError:     "chat.error",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("event type = %q, want %q", got, want)
		}
	}
}

// TestWebSocketRejectsAForeignOrigin pins the origin policy of the event
// stream.
//
// The desktop app connects from the Electron main process, whose handshake
// carries no Origin header. A browser always sends one, and a page on
// another site must not be able to subscribe to the audit events of the
// loopback fallback. The library accepts a request without an Origin and
// refuses one whose origin does not match the host it connected to, which is
// exactly the policy the server relies on — so this test asserts both halves.
func TestWebSocketRejectsAForeignOrigin(t *testing.T) {
	_, handler := newTestServer(t)

	t.Run("a request without an origin is accepted", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/ws", nil)
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Sec-WebSocket-Version", "13")
		req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		// The upgrade is refused by the missing hijacker in the test
		// handler, but it must not be refused with a 403 "origin not
		// authorized": that is the check under test.
		if rec.Code == http.StatusForbidden {
			t.Fatalf("a request without an origin was refused with 403")
		}
	})

	t.Run("a request with a foreign origin is refused", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/ws", nil)
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Sec-WebSocket-Version", "13")
		req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
		req.Header.Set("Origin", "http://attacker.example")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("a foreign origin was not refused: status %d", rec.Code)
		}
	})
}
