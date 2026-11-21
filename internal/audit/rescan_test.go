package audit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tlmcguire/aisai/internal/ast"
	"github.com/tlmcguire/aisai/internal/config"
	"github.com/tlmcguire/aisai/internal/findings"
	"github.com/tlmcguire/aisai/internal/model"
	"github.com/tlmcguire/aisai/internal/sast"
)

// vulnerablePy contains a shell injection, which the SAST engine reports.
const vulnerablePy = `import subprocess

def run(cmd):
    subprocess.run(cmd, shell=True)
    return True
`

// safePy is the same shape without the dangerous call.
const safePy = `import subprocess

def run(cmd):
    subprocess.run(["ls", "-l"])
    return True
`

// newRescanManager builds a manager with one audit whose workspace holds a
// single Python file, plus a findings store.
func newRescanManager(t *testing.T, content string) (*Manager, string, string, *findings.Store) {
	t.Helper()

	workspace := t.TempDir()
	root := filepath.Join(workspace, "a1")
	rel := "pkgs/demo-1.0/demo/mod.py"
	writeFile(t, root, rel, content)

	store, err := findings.Open(filepath.Join(workspace, "findings.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	m := NewManager(Deps{
		Config: &config.Config{Workspace: workspace},
		SAST:   sast.NewEngine(),
		Store:  store,
	})
	id := "a1"
	m.audits[id] = &Audit{
		ID:       id,
		Package:  "demo",
		Version:  "1.0",
		Root:     root,
		status:   model.AuditReady,
		pkgByDir: map[string]model.PURL{"demo-1.0": model.PackageURL("demo", "1.0")},
	}
	return m, id, rel, store
}

// TestRescanFileDetectsAFix is the core of the download → mutate → rescan
// loop: after the dangerous call is removed, the re-scan must report the
// finding as resolved.
func TestRescanFileDetectsAFix(t *testing.T) {
	m, id, rel, store := newRescanManager(t, vulnerablePy)
	ctx := context.Background()

	// The "before" state: scan the file as it is.
	f, err := astParse(t, m, id, rel)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	before := m.normalizeFindings(m.deps.SAST.Scan(f), map[string]model.PURL{})
	if len(before) == 0 {
		t.Fatal("the vulnerable file produced no findings; the test is not exercising anything")
	}
	if _, err := store.Upsert(ctx, id, before); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Apply the fix.
	fixed := safePy
	if err := os.WriteFile(filepath.Join(m.audits[id].Root, filepath.FromSlash(rel)), []byte(fixed), 0o644); err != nil {
		t.Fatalf("write fixed file: %v", err)
	}

	res, err := m.RescanFile(ctx, id, rel)
	if err != nil {
		t.Fatalf("RescanFile: %v", err)
	}
	if len(res.Resolved) != len(before) {
		t.Errorf("resolved = %d, want %d (all findings should be gone)", len(res.Resolved), len(before))
	}
	if len(res.Introduced) != 0 {
		t.Errorf("introduced = %v, want none", res.Introduced)
	}
	if res.Unchanged != 0 {
		t.Errorf("unchanged = %d, want 0", res.Unchanged)
	}

	// The resolved findings must be marked fixed, not left open.
	list, err := store.List(ctx, id, findings.Filter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, fl := range list {
		if fl.State != model.StateFixed {
			t.Errorf("finding %s state = %q, want %q", fl.ID, fl.State, model.StateFixed)
		}
	}
}

// TestRescanFileKeepsUnrelatedFindings checks that the comparison is scoped
// to the patched file: a finding in another file must not be counted as
// resolved.
func TestRescanFileKeepsUnrelatedFindings(t *testing.T) {
	m, id, rel, store := newRescanManager(t, vulnerablePy)
	ctx := context.Background()

	other := model.Finding{
		ID:        "other-1",
		Rule:      "some.other.rule",
		Tool:      "sast",
		CWE:       "CWE-999",
		Severity:  model.SeverityHigh,
		File:      "pkgs/demo-1.0/demo/other.py",
		StartLine: 3,
		State:     model.StateOpen,
	}
	f, err := astParse(t, m, id, rel)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	before := m.normalizeFindings(m.deps.SAST.Scan(f), map[string]model.PURL{})
	all := append(append([]model.Finding(nil), before...), other)
	if _, err := store.Upsert(ctx, id, all); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	if err := os.WriteFile(filepath.Join(m.audits[id].Root, filepath.FromSlash(rel)), []byte(safePy), 0o644); err != nil {
		t.Fatalf("write fixed file: %v", err)
	}

	res, err := m.RescanFile(ctx, id, rel)
	if err != nil {
		t.Fatalf("RescanFile: %v", err)
	}
	for _, id := range res.Resolved {
		if id == other.ID {
			t.Error("a finding in another file was reported as resolved")
		}
	}
}

// TestRescanFileRejectsEscapingPaths checks the traversal guard holds on the
// rescan path too.
func TestRescanFileRejectsEscapingPaths(t *testing.T) {
	m, id, _, _ := newRescanManager(t, vulnerablePy)

	if _, err := m.RescanFile(context.Background(), id, "../../etc/passwd"); err == nil {
		t.Fatal("expected an error for a path that escapes the workspace")
	}
}

// astParse parses one workspace file through the manager's own machinery.
func astParse(t *testing.T, m *Manager, id, rel string) ([]*ast.File, error) {
	t.Helper()
	root, err := m.WorkspaceRoot(id)
	if err != nil {
		return nil, err
	}
	abs, err := ResolvePath(root, rel)
	if err != nil {
		return nil, err
	}
	f, err := ast.Parse(abs)
	if err != nil || f == nil {
		return nil, errors.New("parse failed")
	}
	f.Path = rel
	for i := range f.Chunks {
		f.Chunks[i].AuditID = id
		f.Chunks[i].File = rel
	}
	return []*ast.File{f}, nil
}
