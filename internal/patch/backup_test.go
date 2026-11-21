package patch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const originalContent = "import subprocess\n\n\ndef run(cmd):\n    subprocess.run(cmd, shell=True)\n"

const patchedContent = "import subprocess\n\n\ndef run(cmd):\n    subprocess.run([\"ls\"])\n"

// diffFor produces a unified diff that replaces the shell=True call.
func diffFor(t *testing.T, target string) string {
	t.Helper()
	diff := "--- a/" + target + "\n" +
		"+++ b/" + target + "\n" +
		"@@ -1,5 +1,5 @@\n" +
		" import subprocess\n" +
		" \n" +
		" \n" +
		" def run(cmd):\n" +
		"-    subprocess.run(cmd, shell=True)\n" +
		"+    subprocess.run([\"ls\"])\n"
	return diff
}

func TestApplyKeepsABackupAndRevertRestoresIt(t *testing.T) {
	root := t.TempDir()
	target := "pkgs/demo-1.0/demo/mod.py"
	abs := filepath.Join(root, filepath.FromSlash(target))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(abs, []byte(originalContent), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	a := NewApplier()
	ctx := context.Background()

	if err := a.Apply(ctx, root, target, diffFor(t, target)); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	got, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != patchedContent {
		t.Errorf("file content = %q, want %q", string(got), patchedContent)
	}

	// A backup must exist, inside the workspace.
	backup := BackupPath(root, target)
	if backup == "" {
		t.Fatal("no backup was recorded for the applied change")
	}
	if strings.Contains(backup, "..") {
		t.Errorf("backup path %q escapes the workspace", backup)
	}
	backupAbs := filepath.Join(root, filepath.FromSlash(backup))
	if _, err := os.Stat(backupAbs); err != nil {
		t.Fatalf("backup file missing: %v", err)
	}
	backupData, err := os.ReadFile(backupAbs)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(backupData) != originalContent {
		t.Errorf("backup content = %q, want the original content", string(backupData))
	}

	// Revert restores the original.
	reverted, err := a.Revert(ctx, root, target)
	if err != nil {
		t.Fatalf("Revert: %v", err)
	}
	if !reverted {
		t.Fatal("Revert reported nothing to revert")
	}
	got, err = os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != originalContent {
		t.Errorf("after revert content = %q, want the original %q", string(got), originalContent)
	}

	// A second revert must be a no-op, not a double restore.
	reverted, err = a.Revert(ctx, root, target)
	if err != nil {
		t.Fatalf("second Revert: %v", err)
	}
	if reverted {
		t.Error("a second Revert reported a change, want none")
	}
}

// TestFailedApplyLeavesNoBackup checks that a patch that does not apply
// leaves neither a modified file nor a stray backup.
func TestFailedApplyLeavesNoBackup(t *testing.T) {
	root := t.TempDir()
	target := "pkgs/demo-1.0/demo/mod.py"
	abs := filepath.Join(root, filepath.FromSlash(target))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(abs, []byte(originalContent), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	a := NewApplier()
	bad := "--- a/" + target + "\n+++ b/" + target + "\n@@ -1,1 +1,1 @@\n-this line is not there\n+replacement\n"

	if err := a.Apply(context.Background(), root, target, bad); err == nil {
		t.Fatal("expected the bad patch to be refused")
	}

	got, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != originalContent {
		t.Errorf("the file was modified by a failed patch")
	}

	entries, err := os.ReadDir(filepath.Join(root, backupDirName))
	if err == nil && len(entries) > 0 {
		t.Errorf("a failed patch left %d backup file(s) behind", len(entries))
	}
}

// TestPatchSeriesRendersAppliedChanges checks the .patch export.
func TestPatchSeriesRendersAppliedChanges(t *testing.T) {
	root := t.TempDir()
	target := "pkgs/demo-1.0/demo/mod.py"
	abs := filepath.Join(root, filepath.FromSlash(target))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(abs, []byte(originalContent), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	a := NewApplier()
	ctx := context.Background()
	if err := a.Apply(ctx, root, target, diffFor(t, target)); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	series, err := a.PatchSeries(ctx, root)
	if err != nil {
		t.Fatalf("PatchSeries: %v", err)
	}
	if !strings.Contains(series, "-    subprocess.run(cmd, shell=True)") {
		t.Errorf("the patch series does not contain the removal hunk:\n%s", series)
	}
	if !strings.Contains(series, target) {
		t.Errorf("the patch series does not mention the target file:\n%s", series)
	}

	// After a revert the series is empty, because the change is no longer
	// in effect.
	if _, err := a.Revert(ctx, root, target); err != nil {
		t.Fatalf("Revert: %v", err)
	}
	series, err = a.PatchSeries(ctx, root)
	if err != nil {
		t.Fatalf("PatchSeries after revert: %v", err)
	}
	if strings.TrimSpace(series) != "" {
		t.Errorf("the patch series is not empty after a revert:\n%s", series)
	}
}

// TestBackupAndRevertRefuseEscapes checks the traversal guard on the new
// entry points.
func TestBackupAndRevertRefuseEscapes(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(filepath.Dir(root), "outside.py")
	if err := os.WriteFile(outside, []byte("x = 1\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := Backup(root, "../outside.py"); err == nil {
		t.Error("Backup was allowed to escape the workspace")
	}
	if err := Restore(root, "../outside.py", "../outside.py"); err == nil {
		t.Error("Restore was allowed to escape the workspace")
	}

	// The outside file must be untouched.
	data, err := os.ReadFile(outside)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "x = 1\n" {
		t.Errorf("the outside file was modified: %q", string(data))
	}
}
