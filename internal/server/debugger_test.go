package server

import (
	"net/http"
	"os/exec"
	"testing"
)

func TestDebugSessionOfALocalProject(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	h := newEditorServer(t, nil)
	id := readyLocalAudit(t, h)

	rec, body := do(t, h, http.MethodPost, "/api/debug",
		`{"auditId":"`+id+`","program":"pkgs/demo/app.py","breakpoints":{"pkgs/demo/app.py":[2]}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
	}
	session, _ := body["id"].(string)
	if session == "" || body["program"] != "pkgs/demo/app.py" {
		t.Fatalf("start = %v", body)
	}
	if rec, _ := do(t, h, http.MethodPost, "/api/debug/"+session+"/command", `{"cmd":"rm -rf"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("an unknown command must be refused, got %d", rec.Code)
	}
	// Paused on the breakpoint, so it is still running.
	if rec, _ := do(t, h, http.MethodDelete, "/api/debug/"+session, ""); rec.Code != http.StatusOK {
		t.Errorf("stop: %d %s", rec.Code, rec.Body.String())
	}
}

func TestDebugRefusesBadRequests(t *testing.T) {
	h := newEditorServer(t, nil)
	id := readyLocalAudit(t, h)
	if rec, _ := do(t, h, http.MethodPost, "/api/debug", `{"auditId":"`+id+`","program":"../../etc/x.py"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("a path outside the workspace: %d", rec.Code)
	}
	if rec, _ := do(t, h, http.MethodPost, "/api/debug", `{"auditId":"nope","program":"a.py"}`); rec.Code != http.StatusNotFound {
		t.Errorf("an unknown audit: %d", rec.Code)
	}
	if rec, _ := do(t, h, http.MethodPost, "/api/debug/nope/command", `{"cmd":"continue"}`); rec.Code != http.StatusNotFound {
		t.Errorf("an unknown session: %d", rec.Code)
	}
}

func TestDebuggerCanBeDisabled(t *testing.T) {
	t.Setenv("AISAI_DISABLE_DEBUGGER", "1")
	h := newEditorServer(t, nil)
	if rec, _ := do(t, h, http.MethodPost, "/api/debug", `{"auditId":"x","program":"a.py"}`); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status %d, want 503", rec.Code)
	}
}
