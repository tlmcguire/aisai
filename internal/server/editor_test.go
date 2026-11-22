package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tlmcguire/aisai/internal/audit"
	"github.com/tlmcguire/aisai/internal/config"
	"github.com/tlmcguire/aisai/internal/findings"
	"github.com/tlmcguire/aisai/internal/llm"
	"github.com/tlmcguire/aisai/internal/patch"
)

type fakeLLM struct {
	llm.Provider
	reply string
}

func (f fakeLLM) Complete(context.Context, llm.CompletionRequest) (*llm.Completion, error) {
	return &llm.Completion{Text: f.reply}, nil
}

func newEditorServer(t *testing.T, provider llm.Provider) http.Handler {
	t.Helper()
	store, err := findings.Open(t.TempDir() + "/findings.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	cfg := &config.Config{Workspace: t.TempDir()}
	s := New(Options{Config: cfg, Store: store, Patches: patch.NewApplier(), LLM: provider})
	s.SetManager(audit.NewManager(audit.Deps{Config: cfg, Store: store}))
	return s.Handler()
}

func readyLocalAudit(t *testing.T, h http.Handler) string {
	t.Helper()
	project := filepath.Join(t.TempDir(), "demo")
	_ = os.MkdirAll(project, 0o755)
	_ = os.WriteFile(filepath.Join(project, "app.py"), []byte("x = 1\ny = 2\n"), 0o644)
	_, body := do(t, h, http.MethodPost, "/api/audits", `{"path":`+strconv.Quote(project)+`}`)
	a, _ := body["audit"].(map[string]any)
	id, _ := a["id"].(string)
	deadline := time.Now().Add(20 * time.Second)
	for {
		_, got := do(t, h, http.MethodGet, "/api/audits/"+id, "")
		cur, _ := got["audit"].(map[string]any)
		if cur["status"] == "ready" {
			return id
		}
		if cur["status"] == "failed" || time.Now().After(deadline) {
			t.Fatalf("audit not ready: %v", cur)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestSaveFileWritesBacksUpAndIsRevertible(t *testing.T) {
	h := newEditorServer(t, nil)
	id := readyLocalAudit(t, h)
	base := "/api/audits/" + id

	rec, body := do(t, h, http.MethodPut, base+"/file", `{"path":"pkgs/demo/app.py","content":"x = 1\ny = 3\n"}`)
	if rec.Code != http.StatusOK || body["changed"] != true {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	_, file := do(t, h, http.MethodGet, base+"/file?path=pkgs/demo/app.py", "")
	lines, _ := file["file"].(map[string]any)["lines"].([]any)
	if len(lines) < 2 || lines[1] != "y = 3" {
		t.Fatalf("the saved text is not what the file endpoint returns: %v", lines)
	}
	_, changes := do(t, h, http.MethodGet, base+"/changes", "")
	if list, _ := changes["changes"].([]any); len(list) != 1 {
		t.Fatalf("want 1 recorded change, got %v", changes)
	}
	if rec, _ := do(t, h, http.MethodPut, base+"/file", `{"path":"../../etc/passwd","content":"x"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("a path outside the workspace must be refused, got %d", rec.Code)
	}
	if rec, _ := do(t, h, http.MethodPost, base+"/revert-patch", `{"path":"pkgs/demo/app.py"}`); rec.Code != http.StatusOK {
		t.Fatalf("revert: %d %s", rec.Code, rec.Body.String())
	}
	_, file = do(t, h, http.MethodGet, base+"/file?path=pkgs/demo/app.py", "")
	lines, _ = file["file"].(map[string]any)["lines"].([]any)
	if lines[1] != "y = 2" {
		t.Errorf("revert did not restore the file: %v", lines)
	}
}

func TestCompleteAndEditNeedAModel(t *testing.T) {
	h := newEditorServer(t, nil)
	id := readyLocalAudit(t, h)
	for path, body := range map[string]string{
		"/complete":       `{"path":"pkgs/demo/app.py","prefix":"x"}`,
		"/edit-selection": `{"path":"pkgs/demo/app.py","selection":"x","instruction":"do"}`,
	} {
		rec, _ := do(t, h, http.MethodPost, "/api/audits/"+id+path, body)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s without a model: status %d, want 503", path, rec.Code)
		}
	}
}

func TestCompleteAndEditReturnModelText(t *testing.T) {
	h := newEditorServer(t, fakeLLM{reply: "```\nz = 3\n```"})
	id := readyLocalAudit(t, h)
	_, body := do(t, h, http.MethodPost, "/api/audits/"+id+"/complete", `{"path":"pkgs/demo/app.py","language":"python","prefix":"y = 2\n","suffix":""}`)
	if got, _ := body["completion"].(string); strings.TrimSpace(got) != "z = 3" {
		t.Errorf("completion = %q", got)
	}
	_, body = do(t, h, http.MethodPost, "/api/audits/"+id+"/edit-selection", `{"path":"pkgs/demo/app.py","selection":"y = 2\n","instruction":"rename"}`)
	if got, _ := body["replacement"].(string); strings.TrimSpace(got) != "z = 3" {
		t.Errorf("replacement = %q", got)
	}
}

func TestTerminalLifecycle(t *testing.T) {
	h := newEditorServer(t, nil)
	rec, body := do(t, h, http.MethodPost, "/api/terminals", `{"cols":80,"rows":24}`)
	if rec.Code == http.StatusNotImplemented {
		t.Skip("no terminal on this platform")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("open: %d %s", rec.Code, rec.Body.String())
	}
	id, _ := body["id"].(string)
	if rec, _ := do(t, h, http.MethodPost, "/api/terminals/"+id+"/resize", `{"cols":100,"rows":30}`); rec.Code != http.StatusOK {
		t.Errorf("resize: %d", rec.Code)
	}
	if rec, _ := do(t, h, http.MethodPost, "/api/terminals/"+id+"/resize", `{"cols":0,"rows":0}`); rec.Code != http.StatusBadRequest {
		t.Errorf("a zero size must be refused, got %d", rec.Code)
	}
	if rec, _ := do(t, h, http.MethodDelete, "/api/terminals/"+id, ""); rec.Code != http.StatusOK {
		t.Errorf("close: %d", rec.Code)
	}
	if rec, _ := do(t, h, http.MethodPost, "/api/terminals/nope/input", `{"data":"x"}`); rec.Code != http.StatusNotFound {
		t.Errorf("an unknown terminal must be 404, got %d", rec.Code)
	}
}

func TestTerminalCanBeDisabled(t *testing.T) {
	t.Setenv("AISAI_DISABLE_TERMINAL", "1")
	h := newEditorServer(t, nil)
	if rec, _ := do(t, h, http.MethodPost, "/api/terminals", `{}`); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status %d, want 503", rec.Code)
	}
}

func TestScanJobReportsItsEnd(t *testing.T) {
	h := newEditorServer(t, nil)
	id := readyLocalAudit(t, h)
	_, body := do(t, h, http.MethodPost, "/api/audits/"+id+"/scan", `{"engine":"sast"}`)
	job, _ := body["jobId"].(string)
	if job == "" {
		t.Fatalf("no job id: %v", body)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, got := do(t, h, http.MethodGet, "/api/audits/"+id+"/jobs/"+job, "")
		state, _ := got["job"].(map[string]any)["state"].(string)
		// This test server has no rule engine, so the scan fails, and says so.
		if state == "failed" || state == "done" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the job never ended: %v", got)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if rec, _ := do(t, h, http.MethodGet, "/api/audits/"+id+"/jobs/nope", ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown job: %d", rec.Code)
	}
	if rec, _ := do(t, h, http.MethodDelete, "/api/terminals", ""); rec.Code != http.StatusOK {
		t.Errorf("close all terminals: %d", rec.Code)
	}
}

// A local folder that holds a nested repository (a worktree, a copy) reports
// it as skipped, so the audit is not silently several times its real size.
func TestLocalAuditReportsSkippedNestedRepositories(t *testing.T) {
	h := newEditorServer(t, nil)
	project := filepath.Join(t.TempDir(), "demo")
	_ = os.MkdirAll(filepath.Join(project, "copy"), 0o755)
	_ = os.WriteFile(filepath.Join(project, "app.py"), []byte("x = 1\n"), 0o644)
	_ = os.WriteFile(filepath.Join(project, "copy", "app.py"), []byte("x = 1\n"), 0o644)
	_ = os.WriteFile(filepath.Join(project, "copy", ".git"), []byte("gitdir: /elsewhere"), 0o644)

	_, body := do(t, h, http.MethodPost, "/api/audits", `{"path":`+strconv.Quote(project)+`}`)
	id, _ := body["audit"].(map[string]any)["id"].(string)
	deadline := time.Now().Add(20 * time.Second)
	for {
		_, got := do(t, h, http.MethodGet, "/api/audits/"+id, "")
		cur, _ := got["audit"].(map[string]any)
		if cur["status"] == "ready" {
			skipped, _ := cur["skippedRepos"].([]any)
			if len(skipped) != 1 || skipped[0] != "copy" {
				t.Fatalf("skippedRepos = %v, want [copy]", cur["skippedRepos"])
			}
			if cur["fileCount"] != float64(1) {
				t.Errorf("fileCount = %v, want 1 (only app.py)", cur["fileCount"])
			}
			return
		}
		if cur["status"] == "failed" || time.Now().After(deadline) {
			t.Fatalf("audit not ready: %v", cur)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
