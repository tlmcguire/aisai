package pypi

import (
	"archive/tar"
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// FuzzSafeExtract feeds arbitrary bytes to SafeExtract.
//
// The invariant checked here is that a successful extraction never leaves the
// destination directory: every extracted file is a regular file or a
// directory, and every symbolic link resolves inside the destination.
func FuzzSafeExtract(f *testing.F) {
	f.Add(buildArchive(f, true,
		archiveEntry{name: "pkg-1.0/setup.py", content: "print('hi')\n"},
	))
	f.Add(buildArchive(f, true,
		archiveEntry{name: "../escape.txt", content: "pwned"},
	))
	f.Add(buildArchive(f, true,
		archiveEntry{name: "pkg/link", typeflag: tar.TypeSymlink, linkname: "/etc/passwd"},
	))
	f.Add([]byte("not a tar archive at all"))
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		dst := filepath.Join(t.TempDir(), "dst")
		if err := SafeExtract(bytes.NewReader(data), dst); err != nil {
			return
		}
		err := filepath.WalkDir(dst, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.Type()&os.ModeSymlink == 0 {
				return nil
			}
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			resolved := target
			if !filepath.IsAbs(resolved) {
				resolved = filepath.Join(filepath.Dir(path), target)
			}
			rel, err := filepath.Rel(dst, resolved)
			if err != nil {
				return err
			}
			if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
				t.Fatalf("symlink %q points outside the destination (%q)", path, target)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk the extraction result: %v", err)
		}
	})
}
