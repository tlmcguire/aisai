// Package localsrc copies a project folder from the user's disk into an audit
// workspace.
//
// An audit never works on the original folder. The project is copied into the
// workspace first, and everything else (the file tree, the scan, the patch
// applier) runs on the copy. That keeps the path confinement of the audit
// intact, and it means that applying a fix can never damage the user's tree:
// the changes are exported as a patch instead.
//
// The copy is a snapshot of the source files only. Dependency folders, build
// output, version control data, symbolic links and large files are left out,
// and the size of the snapshot is capped so that a folder chosen by mistake,
// such as a home directory, cannot fill the disk.
package localsrc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Default limits of a snapshot.
const (
	// DefaultMaxFiles is the largest number of files a snapshot holds.
	DefaultMaxFiles = 20000
	// DefaultMaxBytes is the largest total size of a snapshot.
	DefaultMaxBytes int64 = 256 << 20
	// DefaultMaxFileBytes is the largest single file a snapshot holds.
	DefaultMaxFileBytes int64 = 2 << 20
)

// ErrNotDirectory is returned when the chosen path is not a folder.
var ErrNotDirectory = errors.New("the path is not a folder")

// skippedDirs are directory names that are never copied: dependency folders,
// build output and caches. They hold code the project does not own, or code
// that is generated from the code the project does own.
var skippedDirs = map[string]bool{
	"node_modules":  true,
	"__pycache__":   true,
	"venv":          true,
	"target":        true,
	"dist":          true,
	"build":         true,
	"vendor":        true,
	"site-packages": true,
}

// Limits bounds a snapshot. A zero field selects its default.
type Limits struct {
	MaxFiles     int
	MaxBytes     int64
	MaxFileBytes int64
}

func (l Limits) withDefaults() Limits {
	if l.MaxFiles <= 0 {
		l.MaxFiles = DefaultMaxFiles
	}
	if l.MaxBytes <= 0 {
		l.MaxBytes = DefaultMaxBytes
	}
	if l.MaxFileBytes <= 0 {
		l.MaxFileBytes = DefaultMaxFileBytes
	}
	return l
}

// Result describes a finished snapshot.
type Result struct {
	// Files and Bytes are what was copied.
	Files int
	Bytes int64
	// Skipped is the number of files that were left out because they were
	// not wanted, too large, or not regular files.
	Skipped int
	// Truncated reports that a limit stopped the copy early.
	Truncated bool
}

// Validate resolves a folder chosen by the user into a clean absolute path.
//
// A leading "~" stands for the home directory. The path must exist and be a
// folder, and it must not be the root of the filesystem: a snapshot of "/"
// is never what the user meant.
func Validate(path string) (string, error) {
	p := strings.TrimSpace(path)
	if p == "" {
		return "", errors.New("choose a folder to inspect")
	}
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve the home folder: %w", err)
		}
		p = filepath.Join(home, p[1:])
	}
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("%q is not an absolute path", path)
	}
	p = filepath.Clean(p)
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("the folder %q does not exist", path)
		}
		return "", fmt.Errorf("open the folder %q: %w", path, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("open the folder %q: %w", path, err)
	}
	if !info.IsDir() {
		return "", ErrNotDirectory
	}
	if filepath.Dir(resolved) == resolved {
		return "", errors.New("the root of a drive is too large to inspect; choose a project folder")
	}
	return resolved, nil
}

// Name returns the display name of a project folder.
func Name(dir string) string {
	name := filepath.Base(filepath.Clean(dir))
	if name == "." || name == string(filepath.Separator) || name == "" {
		return "project"
	}
	return name
}

// Snapshot copies the wanted files of src into dst.
//
// keep decides, from the file name, which files are copied; a nil keep copies
// every regular file. Directories whose name starts with a dot, the directories
// in the skip list, symbolic links and anything that is not a regular file are
// never copied. dst is created when it does not exist.
func Snapshot(ctx context.Context, src, dst string, keep func(name string) bool, lim Limits) (Result, error) {
	lim = lim.withDefaults()
	var res Result

	if err := os.MkdirAll(dst, 0o755); err != nil {
		return res, fmt.Errorf("create the snapshot folder: %w", err)
	}
	dstResolved, err := filepath.EvalSymlinks(dst)
	if err != nil {
		dstResolved = filepath.Clean(dst)
	}

	errStop := errors.New("snapshot limit reached")

	walkErr := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if err != nil {
			// An entry that cannot be read is left out rather than
			// failing the whole snapshot.
			res.Skipped++
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if path == src {
				return nil
			}
			name := d.Name()
			if strings.HasPrefix(name, ".") || skippedDirs[name] {
				return fs.SkipDir
			}
			// Never copy the audit workspace into itself: a folder that is
			// the destination or holds it is not part of the project.
			if resolved, rerr := filepath.EvalSymlinks(path); rerr == nil &&
				(resolved == dstResolved || strings.HasPrefix(dstResolved, resolved+string(os.PathSeparator))) {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			res.Skipped++
			return nil
		}
		if keep != nil && !keep(d.Name()) {
			res.Skipped++
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > lim.MaxFileBytes {
			res.Skipped++
			return nil
		}
		if res.Files >= lim.MaxFiles || res.Bytes+info.Size() > lim.MaxBytes {
			res.Truncated = true
			return errStop
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			res.Skipped++
			return nil
		}
		n, err := copyFile(path, filepath.Join(dst, rel))
		if err != nil {
			res.Skipped++
			return nil
		}
		res.Files++
		res.Bytes += n
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, errStop) {
		return res, walkErr
	}
	return res, nil
}

// copyFile copies one regular file and returns the number of bytes written.
// The copy is always created with plain read and write permissions: nothing
// that comes out of a project folder is allowed to stay executable.
func copyFile(from, to string) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return 0, err
	}
	in, err := os.Open(from)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(to)
		return 0, err
	}
	return n, nil
}
