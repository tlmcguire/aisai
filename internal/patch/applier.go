package patch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tlmcguire/aisai/internal/model"
)

// Applier applies patches inside an audit workspace and records what it
// changed.
type Applier struct{}

// NewApplier creates an Applier.
func NewApplier() *Applier { return &Applier{} }

// Apply applies a unified diff to a file inside the workspace and records
// the change.
//
// The write is confined to the workspace: the path is resolved against the
// root and anything that escapes is refused.
//
// The pre-patch contents are backed up inside the workspace first, so the
// change can be reverted. If the patch does not apply, the backup is
// discarded and the file is left untouched.
func (a *Applier) Apply(ctx context.Context, root, path, unified string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	backup, err := Backup(root, path)
	if err != nil {
		return err
	}

	if err := Apply(root, path, unified); err != nil {
		// The patch did not apply, so the file is unchanged. Drop the
		// backup rather than leaving a stale copy behind.
		_ = os.Remove(filepath.Join(root, filepath.FromSlash(backup)))
		return err
	}

	return RecordChange(root, ChangeLog{
		Path:    model.RelPath(path),
		Unified: unified,
		Applied: time.Now().UTC(),
		Backup:  backup,
	})
}

// MaxWriteBytes bounds a file saved from the editor.
const MaxWriteBytes = 4 << 20

// Write replaces the contents of a file inside the workspace and records the
// change, exactly as Apply does for a patch: the previous contents are backed
// up first, the write is confined to the workspace, and the change is logged
// as the unified diff between the two versions, so the Changes view, the
// exported patch series and Revert all see an edit made in the editor.
//
// It reports whether anything changed.
func (a *Applier) Write(ctx context.Context, root, path, content string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if len(content) > MaxWriteBytes {
		return false, fmt.Errorf("the file is larger than %d MB", MaxWriteBytes>>20)
	}
	target := model.RelPath(path)
	abs, err := resolveInRoot(root, target)
	if err != nil {
		return false, err
	}
	original, err := os.ReadFile(abs)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", target, err)
	}
	unified := UnifiedDiff(target, string(original), content)
	if unified == "" {
		return false, nil
	}
	backup, err := Backup(root, target)
	if err != nil {
		return false, err
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(abs); err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.WriteFile(abs, []byte(content), mode); err != nil {
		_ = os.Remove(filepath.Join(root, filepath.FromSlash(backup)))
		return false, fmt.Errorf("write %s: %w", target, err)
	}
	return true, RecordChange(root, ChangeLog{
		Path:    target,
		Unified: unified,
		Applied: time.Now().UTC(),
		Backup:  backup,
	})
}

// Changes returns the patches applied to this workspace.
func (a *Applier) Changes(ctx context.Context, root string) ([]AppliedPatch, error) {
	logs, err := LoadChanges(root)
	if err != nil {
		return nil, err
	}
	out := make([]AppliedPatch, 0, len(logs))
	for _, l := range logs {
		out = append(out, AppliedPatch{
			Path:      l.Path,
			Unified:   l.Unified,
			AppliedAt: l.Applied,
			Backup:    l.Backup,
			Reverted:  l.Reverted,
		})
	}
	return out, nil
}

// AppliedPatch is a record of an applied patch.
type AppliedPatch struct {
	Path      string    `json:"path"`
	Unified   string    `json:"unified"`
	AppliedAt time.Time `json:"appliedAt"`
	// Backup is the workspace-relative path of the pre-patch copy.
	Backup string `json:"backup,omitempty"`
	// Reverted reports that the change was rolled back.
	Reverted bool `json:"reverted,omitempty"`
}

// Validate reports whether a patch would apply cleanly to the given content,
// without writing anything.
//
// The UI uses this to distinguish "the model produced a usable diff" from
// "the model produced prose" before offering an Apply button.
func Validate(content, unified string) error {
	diffs, err := Parse(unified)
	if err != nil {
		return err
	}
	if len(diffs) != 1 {
		return fmt.Errorf("expected a patch for exactly one file, got %d", len(diffs))
	}
	_, err = ApplyToContent(content, diffs[0].Hunks)
	return err
}
