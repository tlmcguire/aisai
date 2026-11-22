package audit

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// minFreeBytes is the free space an audit needs before it starts. Downloads
// and the copy of a project are written under the workspace, and a disk that
// fills up half way leaves a broken audit behind, so a nearly full disk is
// refused up front.
const minFreeBytes = 256 << 20

// DiskFullError reports that the disk holding the workspace is full, or too
// full to start. It carries what a person needs to fix it.
type DiskFullError struct {
	// Path is the workspace folder the audit writes to.
	Path string
	// Free is the space left, when it is known.
	Free uint64
	// Known is whether Free was measured.
	Known bool
	// Started is true when the disk filled up during the audit, false when the
	// audit was refused before it began.
	Started bool
}

func (e *DiskFullError) Error() string {
	where := e.Path
	if where == "" {
		where = "the workspace"
	}
	free := ""
	if e.Known {
		free = fmt.Sprintf(" (%s free)", humanBytes(e.Free))
	}
	if e.Started {
		return fmt.Sprintf("The disk is full%s, so the download could not be saved to %s. Free some space, or delete the sources of old audits in Settings > Downloaded sources, then try again. Nothing was left half-written.", free, where)
	}
	return fmt.Sprintf("Not enough free disk space%s to download into %s: at least %s is needed. Free some space, or delete the sources of old audits in Settings > Downloaded sources, then try again.", free, where, humanBytes(minFreeBytes))
}

func humanBytes(n uint64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	v := float64(n)
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}

// checkDiskSpace refuses to start when the workspace disk is nearly full. A
// platform that cannot report free space is let through: the write errors are
// still caught and explained.
func checkDiskSpace(root string) error {
	dir := root
	for dir != "" {
		if _, err := os.Stat(dir); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	free, ok := freeSpace(dir)
	if ok && free < minFreeBytes {
		return &DiskFullError{Path: root, Free: free, Known: true}
	}
	return nil
}

// explainDiskFull turns a "no space left" write error into a DiskFullError and
// leaves every other error alone.
func explainDiskFull(err error, root string) error {
	if err == nil {
		return nil
	}
	var already *DiskFullError
	if errors.As(err, &already) {
		return err
	}
	if !isNoSpace(err) {
		return err
	}
	free, ok := freeSpace(root)
	return &DiskFullError{Path: root, Free: free, Known: ok, Started: true}
}
