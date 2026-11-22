package audit

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tlmcguire/aisai/internal/config"
	"github.com/tlmcguire/aisai/internal/gitsrc"
	"github.com/tlmcguire/aisai/internal/model"
)

func TestGitAuditClonesParsesAndStaysInert(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	src := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", src, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	writeFile(t, src, "app.py", "import os\nos.system(cmd)\n")
	writeFile(t, src, "node_modules/dep/index.js", "x\n")
	// A hook the clone must not run.
	writeFile(t, src, ".githooks/post-checkout", "#!/bin/sh\ntouch /tmp/aisai-hook-ran\n")
	git("add", "-A")
	git("commit", "-q", "-m", "init")

	m := NewManager(Deps{Config: &config.Config{Workspace: t.TempDir()}, GitProtocols: "file"})
	snap, err := m.CreateRepo(context.Background(), gitsrc.Repo{CloneURL: "file://" + src, Owner: "o", Name: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Run(context.Background(), snap.ID); err != nil {
		t.Fatalf("run: %v", err)
	}
	got, _ := m.Snapshot(snap.ID)
	if got.Status != model.AuditReady || got.Source != "git" {
		t.Fatalf("audit = %+v", got)
	}
	root, _ := m.WorkspaceRoot(snap.ID)
	if _, err := os.Stat(filepath.Join(root, "pkgs", "demo", "app.py")); err != nil {
		t.Errorf("the source file is missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "pkgs", "demo", "node_modules")); err == nil {
		t.Error("dependency folders must not be copied")
	}
	if _, err := os.Stat(filepath.Join(root, "pkgs", "demo", ".git")); err == nil {
		t.Error(".git must not be copied")
	}
	if _, err := os.Stat("/tmp/aisai-hook-ran"); err == nil {
		t.Error("a repository hook ran")
	}
}
