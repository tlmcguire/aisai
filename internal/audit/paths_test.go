package audit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tlmcguire/aisai/internal/config"
	"github.com/tlmcguire/aisai/internal/model"
)

// writeFile creates a file with the given content under dir.
func writeFile(t *testing.T, dir, rel, content string) string {
	t.Helper()
	abs := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
	return abs
}

// newWorkspaceManager creates a manager with one audit whose workspace holds
// the given files.
func newWorkspaceManager(t *testing.T, files map[string]string) (*Manager, string, string) {
	t.Helper()

	root := t.TempDir()
	for rel, content := range files {
		writeFile(t, root, rel, content)
	}

	m := NewManager(Deps{Config: &config.Config{Workspace: filepath.Dir(root)}})
	id := "a-path-test"
	m.audits[id] = &Audit{
		ID:      id,
		Package: "demo",
		Version: "1.0",
		Root:    root,
		status:  model.AuditReady,
	}
	return m, id, root
}

const samplePy = `import os

def run(cmd):
    os.system(cmd)
    return True
`

// TestFileTreePathsAreWorkspaceRelative is a regression test.
//
// The tree used to expose absolute paths with the leading slash stripped.
// A client that took such a path and asked for the file — which is exactly
// what the UI does — resolved it against the workspace root and got a 404,
// and the absolute path leaked the local filesystem layout into exported
// reports.
func TestFileTreePathsAreWorkspaceRelative(t *testing.T) {
	m, id, root := newWorkspaceManager(t, map[string]string{
		"pkgs/demo-1.0/demo/__init__.py": samplePy,
		"pkgs/demo-1.0/demo/mod.py":      samplePy,
	})

	tree, err := m.FileTree(context.Background(), id)
	if err != nil {
		t.Fatalf("FileTree: %v", err)
	}

	var paths []string
	var walk func(n *model.FileNode)
	walk = func(n *model.FileNode) {
		if n.Path != "" {
			paths = append(paths, n.Path)
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(tree)

	if len(paths) == 0 {
		t.Fatal("the file tree is empty")
	}
	for _, p := range paths {
		if strings.HasPrefix(p, "/") {
			t.Errorf("tree path %q is absolute", p)
		}
		if strings.Contains(p, filepath.Base(root)) {
			t.Errorf("tree path %q contains the workspace directory name", p)
		}
		if strings.HasPrefix(p, "..") {
			t.Errorf("tree path %q escapes the workspace", p)
		}
	}
}

// TestTreePathResolvesToTheFile is the end-to-end half of the regression:
// every path the tree hands out must be usable with the file endpoint.
func TestTreePathResolvesToTheFile(t *testing.T) {
	m, id, _ := newWorkspaceManager(t, map[string]string{
		"pkgs/demo-1.0/demo/mod.py": samplePy,
	})

	tree, err := m.FileTree(context.Background(), id)
	if err != nil {
		t.Fatalf("FileTree: %v", err)
	}

	var filePath string
	var walk func(n *model.FileNode)
	walk = func(n *model.FileNode) {
		if n.Type == "file" && filePath == "" {
			filePath = n.Path
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(tree)

	if filePath == "" {
		t.Fatal("no file in the tree")
	}

	content, err := m.File(context.Background(), id, filePath)
	if err != nil {
		t.Fatalf("File(%q): %v", filePath, err)
	}
	if !strings.Contains(strings.Join(content.Lines, "\n"), "os.system") {
		t.Errorf("the file content does not look like the file we wrote")
	}
	if content.Path != filePath {
		t.Errorf("content path = %q, want %q", content.Path, filePath)
	}
}

// TestParseTreePathsAreWorkspaceRelative covers the other half: findings and
// chunks must carry the same root-relative paths as the file tree, or a
// finding can never be linked back to its file.
func TestParseTreePathsAreWorkspaceRelative(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "pkgs/demo-1.0/demo/mod.py", samplePy)

	files, err := parseTree(filepath.Join(root, "pkgs", "demo-1.0"), root, "a1")
	if err != nil {
		t.Fatalf("parseTree: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("parseTree returned no files")
	}
	for _, f := range files {
		if strings.HasPrefix(f.Path, "/") {
			t.Errorf("file path %q is absolute", f.Path)
		}
		if !strings.HasPrefix(f.Path, "pkgs/demo-1.0/") {
			t.Errorf("file path %q is not relative to the workspace root", f.Path)
		}
		for _, c := range f.Chunks {
			if strings.HasPrefix(c.File, "/") {
				t.Errorf("chunk file %q is absolute", c.File)
			}
			if c.File != f.Path {
				t.Errorf("chunk file %q does not match the file path %q", c.File, f.Path)
			}
		}
	}
}

// TestASTOnMissingFileReturnsNotFound guards the nil-dereference that made
// the AST endpoint panic with a 500 for an unreadable file.
func TestASTOnMissingFileReturnsNotFound(t *testing.T) {
	m, id, _ := newWorkspaceManager(t, map[string]string{
		"pkgs/demo-1.0/demo/mod.py": samplePy,
	})

	if _, err := m.AST(context.Background(), id, "pkgs/demo-1.0/demo/nope.py"); err == nil {
		t.Fatal("expected an error for a missing file")
	}
	if _, err := m.Chunks(context.Background(), id, "pkgs/demo-1.0/demo/nope.py"); err == nil {
		t.Fatal("expected an error for a missing file")
	}
}

// TestResolvePathNeverEscapesTheRoot is the safety half of the path work:
// making paths relative must not weaken the traversal guard.
//
// The property under test is that ResolvePath never returns a path outside
// the root, for any input. Some inputs are outright refused; an absolute
// path is interpreted as root-relative, which is safe because the result
// still lands inside the workspace.
func TestResolvePathNeverEscapesTheRoot(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Dir(root), "outside.py", "x = 1\n")

	inputs := []string{
		"../outside.py",
		"../../etc/passwd",
		"pkgs/../../outside.py",
		"/etc/passwd",
		"..",
		"./../../outside.py",
		"a/../../../outside.py",
	}

	for _, in := range inputs {
		abs, err := ResolvePath(root, in)
		if err != nil {
			continue // refused outright, which is the strongest outcome
		}
		cleanRoot := filepath.Clean(root)
		if abs != cleanRoot && !strings.HasPrefix(abs, cleanRoot+string(os.PathSeparator)) {
			t.Errorf("ResolvePath(%q) = %q, which is outside the root %q", in, abs, root)
		}
	}
}
