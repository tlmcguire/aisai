package patch

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tlmcguire/aisai/internal/model"
)

// backupDirName is the directory inside the workspace root that holds the
// pre-patch copies of files that were modified.
const backupDirName = ".aisai-backups"

// Backup stores a copy of the file at relPath inside the workspace and
// returns the workspace-relative path of the copy.
//
// Every applied patch gets a backup, so a change can always be rolled back.
// The backup lives inside the workspace and is never written outside it.
func Backup(root, relPath string) (string, error) {
	target := model.RelPath(relPath)
	abs, err := resolveInRoot(root, target)
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", target, err)
	}

	dir := filepath.Join(root, backupDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create backup directory: %w", err)
	}

	// The name is derived from the target path and the time, so successive
	// patches of the same file each keep their own backup.
	stamp := time.Now().UTC().Format("20060102T150405.000000000")
	name := fmt.Sprintf("%s.%s.orig", sanitizeBackupName(target), stamp)
	dest := filepath.Join(dir, name)
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		return "", fmt.Errorf("write backup: %w", err)
	}
	return filepath.ToSlash(filepath.Join(backupDirName, name)), nil
}

// sanitizeBackupName turns a workspace-relative path into a safe file name.
func sanitizeBackupName(p string) string {
	r := strings.NewReplacer("/", "__", "\\", "__", ":", "_", " ", "_")
	return r.Replace(p)
}

// Restore copies a backup back over the file it was taken from.
func Restore(root, relPath, backupRel string) error {
	target := model.RelPath(relPath)
	abs, err := resolveInRoot(root, target)
	if err != nil {
		return err
	}
	backupAbs, err := resolveInRoot(root, model.RelPath(backupRel))
	if err != nil {
		return err
	}
	data, err := os.ReadFile(backupAbs)
	if err != nil {
		return fmt.Errorf("read backup %s: %w", backupRel, err)
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(abs); err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.WriteFile(abs, data, mode); err != nil {
		return fmt.Errorf("restore %s: %w", target, err)
	}
	return nil
}

// Revert undoes the most recent un-reverted change to relPath.
//
// It restores the pre-patch file contents and marks the change as reverted,
// so a second call does not restore the same backup twice.
func (a *Applier) Revert(ctx context.Context, root, relPath string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	target := model.RelPath(relPath)

	logs, err := LoadChanges(root)
	if err != nil {
		return false, err
	}

	// Find the most recent change to this file that has a backup.
	idx := -1
	for i := len(logs) - 1; i >= 0; i-- {
		if logs[i].Reverted {
			continue
		}
		if model.RelPath(logs[i].Path) != target {
			continue
		}
		if logs[i].Backup == "" {
			continue
		}
		idx = i
		break
	}
	if idx < 0 {
		return false, nil
	}

	if err := Restore(root, logs[idx].Path, logs[idx].Backup); err != nil {
		return false, err
	}
	logs[idx].Reverted = true

	data, err := json.MarshalIndent(logs, "", "  ")
	if err != nil {
		return false, err
	}
	if err := os.WriteFile(filepath.Join(root, changeLogName), data, 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// PatchSeries renders every un-reverted change as a single unified diff
// that can be saved as a .patch file and applied with "git apply".
func (a *Applier) PatchSeries(ctx context.Context, root string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	logs, err := LoadChanges(root)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, l := range logs {
		if l.Reverted || strings.TrimSpace(l.Unified) == "" {
			continue
		}
		b.WriteString(strings.TrimRight(l.Unified, "\n"))
		b.WriteString("\n")
	}
	return b.String(), nil
}

// BackupPath returns the workspace-relative path of the most recent backup
// for a file, or "" when there is none.
func BackupPath(root, relPath string) string {
	logs, err := LoadChanges(root)
	if err != nil {
		return ""
	}
	target := model.RelPath(relPath)
	for i := len(logs) - 1; i >= 0; i-- {
		if logs[i].Reverted {
			continue
		}
		if model.RelPath(logs[i].Path) == target && logs[i].Backup != "" {
			return logs[i].Backup
		}
	}
	return ""
}
