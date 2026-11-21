package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tlmcguire/aisai/internal/audit"
	"github.com/tlmcguire/aisai/internal/config"
	"github.com/tlmcguire/aisai/internal/findings"
	"github.com/tlmcguire/aisai/internal/index"
	"github.com/tlmcguire/aisai/internal/model"
	"github.com/tlmcguire/aisai/internal/patch"
	"github.com/tlmcguire/aisai/internal/sast"
)

// fixtureSource is the source of the file the patch fixture applies to.
const fixtureSource = "import os\n\ndef run(cmd):\n    \"\"\"Run a command.\"\"\"\n    os.system(cmd)\n"

// fixtureDiff replaces the last line of fixtureSource with a safer call.
const fixtureDiff = "--- a/pkg/mod.py\n+++ b/pkg/mod.py\n@@ -3,3 +3,3 @@\n def run(cmd):\n     \"\"\"Run a command.\"\"\"\n-    os.system(cmd)\n+    os.system(cmd, shell=False)\n"

// testToolset is a toolset backed by a temporary audit workspace.
type testToolset struct {
	toolset *Toolset
	server  *Server
	manager *audit.Manager
	store   *findings.Store
	auditID string
	root    string
	// outside is the directory holding the audit root, used by the
	// path-escape tests.
	outside string
}

// newTestToolset builds a toolset with one audit whose workspace holds a
// small fixture tree. No network access is required.
func newTestToolset(t *testing.T) *testToolset {
	t.Helper()

	workspace := t.TempDir()
	cfg := config.Default()
	cfg.Workspace = workspace
	cfg.DisableLLM = true

	store, err := findings.Open(filepath.Join(workspace, "findings.sqlite"))
	if err != nil {
		t.Fatalf("open findings store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	idx, err := index.Open(filepath.Join(workspace, "index.sqlite"))
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	t.Cleanup(func() { idx.Close() })

	mgr := audit.NewManager(audit.Deps{
		Config: cfg,
		Store:  store,
		Index:  idx,
		SAST:   sast.NewEngine(),
	})

	ts := NewToolset(Deps{
		Manager: mgr,
		Store:   store,
		Index:   idx,
		Patches: patch.NewApplier(),
	})
	srv := NewServer()
	RegisterTools(srv, ts)

	a, err := mgr.Create(context.Background(), "demo-pkg", "1.0.0")
	if err != nil {
		t.Fatalf("create audit: %v", err)
	}
	root, err := mgr.AuditRoot(a.ID)
	if err != nil {
		t.Fatalf("audit root: %v", err)
	}
	writeFile(t, filepath.Join(root, "pkg", "mod.py"), fixtureSource)
	writeFile(t, filepath.Join(root, "pkg", "util.py"), "VALUE = 1\n")
	writeFile(t, filepath.Join(root, "README.md"), "# demo\n")

	return &testToolset{
		toolset: ts,
		server:  srv,
		manager: mgr,
		store:   store,
		auditID: a.ID,
		root:    root,
		outside: workspace,
	}
}

// writeFile writes a file, creating its directory.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// call invokes a registered tool and returns its output.
func (tt *testToolset) call(t *testing.T, name string, args map[string]any) (string, error) {
	t.Helper()
	for _, tool := range tt.server.Tools() {
		if tool.Name == name {
			return tool.Handler(context.Background(), args)
		}
	}
	t.Fatalf("tool %q is not registered", name)
	return "", nil
}

// callOK invokes a registered tool and fails the test when it errors.
func (tt *testToolset) callOK(t *testing.T, name string, args map[string]any) string {
	t.Helper()
	out, err := tt.call(t, name, args)
	if err != nil {
		t.Fatalf("tool %s failed: %v", name, err)
	}
	return out
}

func TestApplyPatchRefusesPathEscape(t *testing.T) {
	tt := newTestToolset(t)

	sentinel := filepath.Join(tt.outside, "sentinel.txt")
	writeFile(t, sentinel, "do not touch\n")
	before, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatalf("read sentinel: %v", err)
	}

	escapeDiff := "--- a/../sentinel.txt\n+++ b/../sentinel.txt\n@@ -1,1 +1,1 @@\n-do not touch\n+pwned\n"
	_, err = tt.call(t, "apply_patch", map[string]any{
		"path":         "../sentinel.txt",
		"unified_diff": escapeDiff,
		"audit_id":     tt.auditID,
	})
	if err == nil {
		t.Fatal("apply_patch accepted a path that escapes the audit workspace")
	}
	if !strings.Contains(err.Error(), "escapes the audit workspace") {
		t.Fatalf("error = %v, want it to mention the workspace escape", err)
	}

	after, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatalf("read sentinel: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("the file outside the workspace changed: %q -> %q", before, after)
	}
}

func TestApplyPatchWritesInsideTheWorkspace(t *testing.T) {
	tt := newTestToolset(t)

	out := tt.callOK(t, "apply_patch", map[string]any{
		"path":         "pkg/mod.py",
		"unified_diff": fixtureDiff,
		"audit_id":     tt.auditID,
	})
	if !strings.Contains(out, "pkg/mod.py") {
		t.Fatalf("apply_patch output = %q, want it to name the file", out)
	}

	got, err := os.ReadFile(filepath.Join(tt.root, "pkg", "mod.py"))
	if err != nil {
		t.Fatalf("read patched file: %v", err)
	}
	want := "import os\n\ndef run(cmd):\n    \"\"\"Run a command.\"\"\"\n    os.system(cmd, shell=False)\n"
	if string(got) != want {
		t.Fatalf("patched file = %q, want %q", got, want)
	}
}

func TestApplyPatchRefusesDiffThatDoesNotFit(t *testing.T) {
	tt := newTestToolset(t)

	bad := "--- a/pkg/mod.py\n+++ b/pkg/mod.py\n@@ -1,1 +1,1 @@\n-import nonexistent\n+import nonexistent2\n"
	_, err := tt.call(t, "apply_patch", map[string]any{
		"path":         "pkg/mod.py",
		"unified_diff": bad,
		"audit_id":     tt.auditID,
	})
	if err == nil {
		t.Fatal("apply_patch accepted a diff that does not apply cleanly")
	}
	got, err := os.ReadFile(filepath.Join(tt.root, "pkg", "mod.py"))
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(got) != fixtureSource {
		t.Fatalf("the file changed despite the refused patch: %q", got)
	}
}

func TestListFilesGetSourceAndAST(t *testing.T) {
	tt := newTestToolset(t)

	tree := tt.callOK(t, "list_files", map[string]any{"audit_id": tt.auditID})
	lines := map[string]bool{}
	for _, line := range strings.Split(tree, "\n") {
		lines[line] = true
	}
	for _, want := range []string{"pkg/", "pkg/mod.py", "pkg/util.py", "README.md"} {
		if !lines[want] {
			t.Fatalf("list_files output = %q, want it to contain the line %q", tree, want)
		}
	}

	src := tt.callOK(t, "get_source", map[string]any{"path": "pkg/mod.py", "audit_id": tt.auditID})
	if !strings.Contains(src, "os.system(cmd)") {
		t.Fatalf("get_source output = %q, want the file contents", src)
	}

	tree2 := tt.callOK(t, "get_ast", map[string]any{"path": "pkg/mod.py", "audit_id": tt.auditID})
	if !strings.Contains(tree2, "function_definition") {
		t.Fatalf("get_ast output = %q, want a function_definition node", tree2)
	}
}

func TestListFindingsSeverityFilter(t *testing.T) {
	tt := newTestToolset(t)

	list := []model.Finding{
		{
			ID: "f-high", Rule: "r1", Tool: "sast", CWE: "CWE-78",
			Severity: model.SeverityHigh, Confidence: 0.9,
			File: "pkg/mod.py", StartLine: 5, EndLine: 5,
			Message: "command injection", State: model.StateOpen,
		},
		{
			ID: "f-low", Rule: "r2", Tool: "sast", CWE: "CWE-330",
			Severity: model.SeverityLow, Confidence: 0.4,
			File: "pkg/util.py", StartLine: 1, EndLine: 1,
			Message: "weak constant", State: model.StateOpen,
		},
	}
	if _, err := tt.store.Upsert(context.Background(), tt.auditID, list); err != nil {
		t.Fatalf("upsert findings: %v", err)
	}

	all := tt.callOK(t, "list_findings", map[string]any{"audit_id": tt.auditID})
	if !strings.Contains(all, "f-high") || !strings.Contains(all, "f-low") {
		t.Fatalf("list_findings output = %q, want both findings", all)
	}

	high := tt.callOK(t, "list_findings", map[string]any{"severity": "high", "audit_id": tt.auditID})
	if !strings.Contains(high, "f-high") {
		t.Fatalf("list_findings output = %q, want the high finding", high)
	}
	if strings.Contains(high, "f-low") {
		t.Fatalf("list_findings output = %q, want the low finding filtered out", high)
	}
}

func TestAuditIDFallsBackToTheCurrentAudit(t *testing.T) {
	tt := newTestToolset(t)

	// Without a current audit the tool must say what to do next.
	_, err := tt.call(t, "list_findings", map[string]any{})
	if err == nil {
		t.Fatal("list_findings must fail when no audit is in progress")
	}
	if err.Error() != errNoAudit.Error() {
		t.Fatalf("error = %q, want %q", err.Error(), errNoAudit.Error())
	}

	tt.toolset.SetCurrentAudit(tt.auditID)
	out := tt.callOK(t, "list_files", map[string]any{})
	if !strings.Contains(out, "pkg/mod.py") {
		t.Fatalf("list_files output = %q, want the fixture tree", out)
	}
}

func TestUnknownAuditIDIsRejected(t *testing.T) {
	tt := newTestToolset(t)
	_, err := tt.call(t, "list_files", map[string]any{"audit_id": "a999999999999"})
	if err == nil {
		t.Fatal("list_files must reject an unknown audit id")
	}
	if !strings.Contains(err.Error(), "unknown audit") {
		t.Fatalf("error = %v, want it to mention the unknown audit", err)
	}
}

func TestLLMToolsFailClearlyWithoutAProvider(t *testing.T) {
	tt := newTestToolset(t)

	for _, name := range []string{"ask_codebase", "run_llm_scan"} {
		_, err := tt.call(t, name, map[string]any{"audit_id": tt.auditID, "path": "pkg/mod.py", "question": "what?"})
		if err == nil {
			t.Fatalf("%s must fail when no LLM provider is configured", name)
		}
		if !strings.Contains(err.Error(), "LLM") {
			t.Fatalf("%s error = %v, want it to mention the LLM", name, err)
		}
	}
}

func TestEveryRegisteredToolIsWellFormed(t *testing.T) {
	tt := newTestToolset(t)

	seen := map[string]bool{}
	for _, tool := range tt.server.Tools() {
		if tool.Name == "" {
			t.Fatal("a tool has no name")
		}
		if tool.Description == "" {
			t.Fatalf("tool %s has no description", tool.Name)
		}
		if tool.InputSchema == nil {
			t.Fatalf("tool %s has no input schema", tool.Name)
		}
		if kind, _ := tool.InputSchema["type"].(string); kind != "object" {
			t.Fatalf("tool %s input schema type = %q, want \"object\"", tool.Name, kind)
		}
		if tool.Handler == nil {
			t.Fatalf("tool %s has no handler", tool.Name)
		}
		seen[tool.Name] = true
	}

	want := []string{
		"scan_package", "get_dependency_graph", "list_files", "get_source",
		"get_ast", "run_sast", "run_llm_scan", "list_findings", "get_finding",
		"propose_fix", "apply_patch", "export_report", "search_code", "ask_codebase",
	}
	for _, name := range want {
		if !seen[name] {
			t.Fatalf("tool %s is not registered", name)
		}
	}
	if len(seen) != len(want) {
		t.Fatalf("registered %d tools, want %d", len(seen), len(want))
	}
}

func TestSearchCodeFindsTheFixture(t *testing.T) {
	tt := newTestToolset(t)

	// The index is empty in the fixture, so the search must degrade to "no
	// match" rather than fail.
	out, err := tt.call(t, "search_code", map[string]any{"query": "os.system", "audit_id": tt.auditID})
	if err != nil {
		t.Fatalf("search_code failed: %v", err)
	}
	if !strings.Contains(out, "No code matched") {
		t.Fatalf("search_code output = %q, want a no-match message", out)
	}
}

// TestGetSourcePaginates pins the line range of get_source.
//
// A long file can exceed the output cap of the transport, so the tool takes
// a line range and the header always states the total number of lines and
// the range that was returned. Without that, an agent reading a truncated
// result cannot tell that it is looking at a part of the file.
func TestGetSourcePaginates(t *testing.T) {
	tt := newTestToolset(t)

	full := tt.callOK(t, "get_source", map[string]any{"path": "pkg/mod.py", "audit_id": tt.auditID})
	if !strings.Contains(full, "lines)") {
		t.Fatalf("the header does not state the number of lines: %q", full[:min(80, len(full))])
	}

	part := tt.callOK(t, "get_source", map[string]any{
		"path":       "pkg/mod.py",
		"start_line": 1,
		"end_line":   1,
		"audit_id":   tt.auditID,
	})
	if !strings.Contains(part, "lines 1-1 of") {
		t.Fatalf("the header does not state the range: %q", part[:min(80, len(part))])
	}
	if strings.Contains(part, "os.system(cmd)") && !strings.Contains(full, "os.system(cmd)") {
		t.Fatalf("the range returned more than the first line: %q", part)
	}

	// A range that starts past the end is an error, not an empty answer.
	if _, err := tt.call(t, "get_source", map[string]any{
		"path":       "pkg/mod.py",
		"start_line": 500,
		"end_line":   900,
		"audit_id":   tt.auditID,
	}); err == nil {
		t.Fatal("a range past the end of the file must be refused")
	}
}
