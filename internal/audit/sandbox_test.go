package audit

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tlmcguire/aisai/internal/config"
	"github.com/tlmcguire/aisai/internal/findings"
)

// newSandboxManager builds a manager with an empty workspace.
func newSandboxManager(t *testing.T) (*Manager, string) {
	t.Helper()

	store, err := findings.Open(t.TempDir() + "/findings.sqlite")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	cfg := &config.Config{Workspace: t.TempDir()}
	return NewManager(Deps{Config: cfg, Store: store}), cfg.Workspace
}

// TestHardenSandboxStripsExecutableBits checks that nothing that comes out of
// an archive stays executable.
func TestHardenSandboxStripsExecutableBits(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "run.sh")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := os.Chmod(exe, 0o4755); err != nil {
		t.Fatalf("chmod file: %v", err)
	}

	if err := hardenSandbox(dir); err != nil {
		t.Fatalf("hardenSandbox: %v", err)
	}

	info, err := os.Stat(exe)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if info.Mode().Perm()&0o111 != 0 {
		t.Errorf("the file is still executable: %v", info.Mode())
	}
	if info.Mode()&os.ModeSetuid != 0 {
		t.Errorf("the setuid bit survived: %v", info.Mode())
	}
}

// TestHardenSandboxRemovesLinks checks that symlinks do not survive in the
// sandbox.
func TestHardenSandboxRemovesLinks(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks are not supported here: %v", err)
	}

	if err := hardenSandbox(dir); err != nil {
		t.Fatalf("hardenSandbox: %v", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Error("the symlink survived the hardening")
	}
}

// TestFlattenSingleRoot checks that the wrapper directory of a source
// distribution is removed.
func TestFlattenSingleRoot(t *testing.T) {
	dir := t.TempDir()
	inner := filepath.Join(dir, "flask-3.1.3")
	if err := os.MkdirAll(filepath.Join(inner, "src", "flask"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(inner, "setup.py"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if err := flattenSingleRoot(dir); err != nil {
		t.Fatalf("flattenSingleRoot: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "setup.py")); err != nil {
		t.Errorf("the content was not moved up: %v", err)
	}
	if _, err := os.Stat(inner); !os.IsNotExist(err) {
		t.Error("the wrapper directory is still there")
	}
}

// TestFlattenSingleRootKeepsSeveralEntries checks that a directory with more
// than one entry is left alone.
func TestFlattenSingleRootKeepsSeveralEntries(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "a"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := flattenSingleRoot(dir); err != nil {
		t.Fatalf("flattenSingleRoot: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "a")); err != nil {
		t.Error("the directory was removed")
	}
}

// TestDeleteSourcesRemovesTheSandbox checks that the downloaded sources can
// be deleted while the audit and its findings are kept.
func TestDeleteSourcesRemovesTheSandbox(t *testing.T) {
	mgr, _ := newSandboxManager(t)
	ctx := context.Background()

	audit, err := mgr.Create(ctx, "flask", "3.1.0")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	root := audit.Root
	pkgDir := filepath.Join(sandboxDir(root), "flask-3.1.0")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	source := filepath.Join(pkgDir, "app.py")
	if err := os.WriteFile(source, []byte("print('x')\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	freed, err := mgr.DeleteSources(ctx, audit.ID)
	if err != nil {
		t.Fatalf("DeleteSources: %v", err)
	}
	if freed <= 0 {
		t.Errorf("freed = %d, want a positive number of bytes", freed)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Error("the downloaded source is still on disk")
	}
	if _, err := os.Stat(sandboxDir(root)); !os.IsNotExist(err) {
		t.Error("the sandbox directory is still on disk")
	}

	snapshot, err := mgr.Snapshot(audit.ID)
	if err != nil {
		t.Fatalf("the audit must survive the deletion: %v", err)
	}
	if !snapshot.SourcesDeleted {
		t.Error("the audit does not report that its sources were deleted")
	}
	if snapshot.SandboxBytes != 0 {
		t.Errorf("sandboxBytes = %d, want 0", snapshot.SandboxBytes)
	}
}

// TestDeleteSourcesReportsUnknownAudits checks the not-found path.
func TestDeleteSourcesReportsUnknownAudits(t *testing.T) {
	mgr, _ := newSandboxManager(t)
	if _, err := mgr.DeleteSources(context.Background(), "nope"); err == nil {
		t.Fatal("expected an error for an unknown audit")
	}
}

// TestSandboxSizeReportsTheDownloadedSources checks the size helper.
func TestSandboxSizeReportsTheDownloadedSources(t *testing.T) {
	mgr, _ := newSandboxManager(t)
	ctx := context.Background()

	audit, err := mgr.Create(ctx, "flask", "3.1.0")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	pkgDir := filepath.Join(sandboxDir(audit.Root), "flask-3.1.0")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	payload := make([]byte, 1024)
	if err := os.WriteFile(filepath.Join(pkgDir, "app.py"), payload, 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	size, err := mgr.SandboxSize(audit.ID)
	if err != nil {
		t.Fatalf("SandboxSize: %v", err)
	}
	if size != 1024 {
		t.Errorf("size = %d, want 1024", size)
	}
}
