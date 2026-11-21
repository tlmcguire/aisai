package audit

import (
	"io/fs"
	"os"
	"path/filepath"
)

// The workspace of an audit is laid out like this:
//
//	<root>/pkgs/<package>-<version>/   the extracted sources of a package
//	<root>/.download/                  scratch space for the downloaded archives
//
// The sources live under "pkgs" so that they can be told apart from the rest
// of the workspace, and so that they can be deleted in one go when the audit
// is finished with them. The archives are downloaded into a scratch space
// outside the sandbox and deleted right after the extraction: an archive is
// an opaque blob that is never inspected, and keeping it out of the tree
// means the file browser only ever shows real source files.

// sandboxDir is the directory the extracted sources live in.
func sandboxDir(root string) string {
	return filepath.Join(root, "pkgs")
}

// downloadDir is the scratch directory the archives are downloaded into.
func downloadDir(root string) string {
	return filepath.Join(root, ".download")
}

// hardenSandbox makes the extracted sources safe to keep on disk.
//
// A downloaded package is untrusted input. Nothing in it is ever executed,
// but an archive can still carry a setuid bit or an executable flag, and
// those bits would survive the extraction. Stripping them keeps the sandbox
// inert even if something later runs a file from it by accident.
//
// Symlinks and device files are removed: the extractor already refuses the
// ones that point outside the sandbox, and a link that stays inside it has
// no value for an inspection.
func hardenSandbox(dir string) error {
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// A file that cannot be read is a file that cannot be
			// hardened either; it is left alone rather than aborting the
			// whole extraction.
			return nil
		}
		if d.IsDir() {
			return os.Chmod(path, 0o755)
		}
		if !d.Type().IsRegular() {
			return os.Remove(path)
		}
		return os.Chmod(path, 0o644)
	})
}

// flattenSingleRoot removes the wrapper directory a source distribution
// carries.
//
// A source distribution holds a single top-level directory named after the
// project ("flask-3.1.3/…"). Keeping it would show the same name twice in
// the file tree, so the contents are moved up one level. The wrapper is left
// alone when it cannot be moved safely.
func flattenSingleRoot(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if len(entries) != 1 || !entries[0].IsDir() {
		return nil
	}
	inner := filepath.Join(dir, entries[0].Name())
	children, err := os.ReadDir(inner)
	if err != nil {
		return nil
	}
	for _, child := range children {
		from := filepath.Join(inner, child.Name())
		to := filepath.Join(dir, child.Name())
		if _, err := os.Stat(to); err == nil {
			// Something of that name is already there: keep both by leaving
			// the wrapper alone.
			return nil
		}
		if err := os.Rename(from, to); err != nil {
			return nil
		}
	}
	return os.Remove(inner)
}

// sandboxSize returns the total size of the extracted sources.
func sandboxSize(root string) int64 {
	var total int64
	_ = filepath.WalkDir(sandboxDir(root), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		total += info.Size()
		return nil
	})
	return total
}
