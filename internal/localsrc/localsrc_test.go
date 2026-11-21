package localsrc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func isGo(name string) bool { return strings.HasSuffix(name, ".go") || strings.HasSuffix(name, ".py") }

func TestSnapshotCopiesSourceAndSkipsTheRest(t *testing.T) {
	src, dst := t.TempDir(), filepath.Join(t.TempDir(), "out")
	write(t, src, "main.go", "package main")
	write(t, src, "pkg/a.py", "x = 1")
	write(t, src, "node_modules/lib/x.py", "bad")
	write(t, src, ".git/config", "bad")
	write(t, src, "README.md", "not wanted")
	if err := os.Symlink("/etc/hosts", filepath.Join(src, "link.go")); err != nil {
		t.Skip("symlinks unavailable")
	}

	res, err := Snapshot(context.Background(), src, dst, isGo, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Files != 2 {
		t.Fatalf("copied %d files, want 2 (%+v)", res.Files, res)
	}
	for _, rel := range []string{"main.go", "pkg/a.py"} {
		info, err := os.Stat(filepath.Join(dst, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("%s missing: %v", rel, err)
		}
		if info.Mode().Perm()&0o111 != 0 {
			t.Errorf("%s kept an executable bit: %v", rel, info.Mode())
		}
	}
	for _, rel := range []string{"node_modules", ".git", "README.md", "link.go"} {
		if _, err := os.Lstat(filepath.Join(dst, rel)); err == nil {
			t.Errorf("%s must not be copied", rel)
		}
	}
}

func TestSnapshotLimits(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	for _, n := range []string{"a.go", "b.go", "c.go"} {
		write(t, src, n, "package x")
	}
	write(t, src, "big.go", strings.Repeat("x", 100))
	res, err := Snapshot(context.Background(), src, dst, isGo, Limits{MaxFiles: 2, MaxFileBytes: 50})
	if err != nil {
		t.Fatal(err)
	}
	if res.Files != 2 || !res.Truncated {
		t.Fatalf("want 2 files and truncation, got %+v", res)
	}
	if _, err := os.Stat(filepath.Join(dst, "big.go")); err == nil {
		t.Error("an oversized file must not be copied")
	}
}

func TestSnapshotDoesNotCopyTheDestinationIntoItself(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(src, "workspace")
	write(t, src, "a.go", "package a")
	write(t, src, "workspace/old.go", "package old")
	if _, err := Snapshot(context.Background(), src, dst, isGo, Limits{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "workspace")); err == nil {
		t.Error("the destination was copied into itself")
	}
}

func TestSnapshotSkipsAWorkspaceThatHoldsTheDestination(t *testing.T) {
	src := t.TempDir()
	write(t, src, "a.go", "package a")
	write(t, src, "work/other-audit/pkgs/x/b.go", "package b")
	dst := filepath.Join(src, "work", "new-audit", "pkgs", "proj")
	res, err := Snapshot(context.Background(), src, dst, isGo, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Files != 1 {
		t.Fatalf("copied %d files, want only a.go (%+v)", res.Files, res)
	}
}

func TestSnapshotHonoursCancellation(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	write(t, src, "a.go", "package a")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Snapshot(ctx, src, dst, isGo, Limits{}); err == nil {
		t.Fatal("expected a cancellation error")
	}
}

func TestValidate(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.go")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := Validate(dir); err != nil || got == "" {
		t.Fatalf("Validate(dir) = %q, %v", got, err)
	}
	for name, in := range map[string]string{
		"empty": "", "relative": "some/dir", "missing": filepath.Join(dir, "nope"),
		"file": file, "root": string(filepath.Separator),
	} {
		if _, err := Validate(in); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestName(t *testing.T) {
	if got := Name("/a/b/proj/"); got != "proj" {
		t.Errorf("Name = %q", got)
	}
	if got := Name("/"); got != "project" {
		t.Errorf("Name(/) = %q", got)
	}
}
