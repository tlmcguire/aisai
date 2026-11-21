package pypi

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// archiveEntry describes one entry of a test archive.
type archiveEntry struct {
	name     string
	typeflag byte
	content  string
	linkname string
	mode     int64
}

// buildArchive builds a tar (optionally gzip compressed) from entries.
func buildArchive(t testing.TB, compressed bool, entries ...archiveEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	var tw *tar.Writer
	var gz *gzip.Writer
	if compressed {
		gz = gzip.NewWriter(&buf)
		tw = tar.NewWriter(gz)
	} else {
		tw = tar.NewWriter(&buf)
	}
	for _, e := range entries {
		flag := e.typeflag
		if flag == 0 {
			flag = tar.TypeReg
		}
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		hdr := &tar.Header{
			Name:     e.name,
			Typeflag: flag,
			Mode:     mode,
			Size:     int64(len(e.content)),
			Linkname: e.linkname,
		}
		if flag != tar.TypeReg {
			hdr.Size = 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write header %q: %v", e.name, err)
		}
		if flag == tar.TypeReg && e.content != "" {
			if _, err := tw.Write([]byte(e.content)); err != nil {
				t.Fatalf("write body %q: %v", e.name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if gz != nil {
		if err := gz.Close(); err != nil {
			t.Fatalf("close gzip: %v", err)
		}
	}
	return buf.Bytes()
}

// TestSafeExtractRejectsUnsafeEntries covers every archive entry that must
// be refused before anything is written.
func TestSafeExtractRejectsUnsafeEntries(t *testing.T) {
	tests := []struct {
		name    string
		entries []archiveEntry
		wantErr string
	}{
		{
			name: "parent traversal",
			entries: []archiveEntry{
				{name: "../etc/passwd", content: "pwned"},
			},
			wantErr: "escapes the destination",
		},
		{
			name: "absolute path",
			entries: []archiveEntry{
				{name: "/etc/passwd", content: "pwned"},
			},
			wantErr: "absolute path",
		},
		{
			name: "nested traversal",
			entries: []archiveEntry{
				{name: "a/../../b", content: "pwned"},
			},
			wantErr: "escapes the destination",
		},
		{
			name: "deep traversal",
			entries: []archiveEntry{
				{name: "a/b/../../../c", content: "pwned"},
			},
			wantErr: "escapes the destination",
		},
		{
			name: "dot dot only",
			entries: []archiveEntry{
				{name: "..", content: "pwned"},
			},
			wantErr: "escapes the destination",
		},
		{
			name: "absolute symlink target",
			entries: []archiveEntry{
				{name: "link", typeflag: tar.TypeSymlink, linkname: "/etc/passwd"},
			},
			wantErr: "absolute symlink target",
		},
		{
			name: "relative symlink escape",
			entries: []archiveEntry{
				{name: "pkg/link", typeflag: tar.TypeSymlink, linkname: "../../evil.sh"},
			},
			wantErr: "escapes the destination",
		},
		{
			name: "hardlink escape",
			entries: []archiveEntry{
				{name: "pkg/evil", typeflag: tar.TypeLink, linkname: "../../evil.sh"},
			},
			wantErr: "escapes the destination",
		},
		{
			name: "absolute hardlink target",
			entries: []archiveEntry{
				{name: "pkg/evil", typeflag: tar.TypeLink, linkname: "/etc/passwd"},
			},
			wantErr: "absolute path",
		},
		{
			name: "write through symlinked directory",
			entries: []archiveEntry{
				{name: "pkg", typeflag: tar.TypeDir},
				{name: "pkg/link", typeflag: tar.TypeSymlink, linkname: ".."},
				{name: "pkg/link/evil.sh", content: "pwned"},
			},
			wantErr: "symlinked directory",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			parent := t.TempDir()
			dst := filepath.Join(parent, "dst")
			archive := buildArchive(t, true, tc.entries...)

			err := SafeExtract(bytes.NewReader(archive), dst)
			if err == nil {
				t.Fatalf("SafeExtract accepted an unsafe archive")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not mention %q", err, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.entries[0].name) {
				t.Fatalf("error %q does not name the offending entry %q", err, tc.entries[0].name)
			}
			assertNoEscape(t, parent, dst)
		})
	}
}

// TestSafeExtractTooManyEntries verifies the entry count limit.
func TestSafeExtractTooManyEntries(t *testing.T) {
	parent := t.TempDir()
	dst := filepath.Join(parent, "dst")

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for i := 0; i <= MaxExtractEntries; i++ {
		hdr := &tar.Header{Name: "file-" + strconv.Itoa(i), Typeflag: tar.TypeReg, Mode: 0o644, Size: 0}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write header: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}

	err := SafeExtract(bytes.NewReader(buf.Bytes()), dst)
	if err == nil {
		t.Fatalf("SafeExtract accepted an archive with too many entries")
	}
	if !strings.Contains(err.Error(), "entries") {
		t.Fatalf("unexpected error %q", err)
	}
	assertNoEscape(t, parent, dst)
}

// TestSafeExtractHappyPath extracts a normal source distribution.
func TestSafeExtractHappyPath(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "dst")
	archive := buildArchive(t, true,
		archiveEntry{name: "flask-2.0.0/", typeflag: tar.TypeDir, mode: 0o777},
		archiveEntry{name: "flask-2.0.0/setup.py", content: "print('hi')\n", mode: 0o777},
		archiveEntry{name: "flask-2.0.0/flask/__init__.py", content: "x = 1\n"},
		archiveEntry{name: "flask-2.0.0/LICENSE", content: "MIT"},
	)
	if err := SafeExtract(bytes.NewReader(archive), dst); err != nil {
		t.Fatalf("SafeExtract: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(dst, "flask-2.0.0", "setup.py"))
	if err != nil {
		t.Fatalf("read extracted file: %v", err)
	}
	if string(content) != "print('hi')\n" {
		t.Fatalf("unexpected content %q", content)
	}
	for _, name := range []string{"flask-2.0.0", "flask-2.0.0/flask", "flask-2.0.0/flask/__init__.py"} {
		info, err := os.Stat(filepath.Join(dst, filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("stat %q: %v", name, err)
		}
		want := os.FileMode(0o755)
		if !info.IsDir() {
			want = 0o644
		}
		if info.Mode().Perm() != want {
			t.Fatalf("%q has mode %v, want %v", name, info.Mode().Perm(), want)
		}
	}
}

// TestSafeExtractPlainTar verifies that an uncompressed tar is accepted.
func TestSafeExtractPlainTar(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "dst")
	archive := buildArchive(t, false, archiveEntry{name: "pkg/mod.py", content: "x = 1\n"})
	if err := SafeExtract(bytes.NewReader(archive), dst); err != nil {
		t.Fatalf("SafeExtract: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "pkg", "mod.py")); err != nil {
		t.Fatalf("stat extracted file: %v", err)
	}
}

// TestSafeExtractSkipsSpecialFiles verifies that device and FIFO entries are
// skipped instead of failing the extraction.
func TestSafeExtractSkipsSpecialFiles(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "dst")
	archive := buildArchive(t, true,
		archiveEntry{name: "pkg/mod.py", content: "x = 1\n"},
		archiveEntry{name: "dev/null", typeflag: tar.TypeChar},
		archiveEntry{name: "dev/zero", typeflag: tar.TypeBlock},
		archiveEntry{name: "var/pipe", typeflag: tar.TypeFifo},
	)
	if err := SafeExtract(bytes.NewReader(archive), dst); err != nil {
		t.Fatalf("SafeExtract: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "pkg", "mod.py")); err != nil {
		t.Fatalf("stat extracted file: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dst, "dev", "null")); !os.IsNotExist(err) {
		t.Fatalf("device entry was extracted")
	}
}

// TestSafeExtractAllowsInnerSymlink verifies that a symlink staying inside
// the destination is created.
func TestSafeExtractAllowsInnerSymlink(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "dst")
	archive := buildArchive(t, true,
		archiveEntry{name: "pkg/data.txt", content: "hello"},
		archiveEntry{name: "pkg/link.txt", typeflag: tar.TypeSymlink, linkname: "data.txt"},
	)
	if err := SafeExtract(bytes.NewReader(archive), dst); err != nil {
		t.Fatalf("SafeExtract: %v", err)
	}
	target, err := os.Readlink(filepath.Join(dst, "pkg", "link.txt"))
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if target != "data.txt" {
		t.Fatalf("unexpected symlink target %q", target)
	}
}

// TestSafeExtractStripsSetuidBits verifies that privileged modes are not
// preserved.
func TestSafeExtractStripsSetuidBits(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "dst")
	archive := buildArchive(t, true,
		archiveEntry{name: "bin/tool", content: "#!/bin/sh\n", mode: 0o4755},
		archiveEntry{name: "bin/dir", typeflag: tar.TypeDir, mode: 0o2775},
	)
	if err := SafeExtract(bytes.NewReader(archive), dst); err != nil {
		t.Fatalf("SafeExtract: %v", err)
	}
	info, err := os.Stat(filepath.Join(dst, "bin", "tool"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("file mode is %v, want 0644", info.Mode().Perm())
	}
	if info.Mode()&os.ModeSetuid != 0 {
		t.Fatalf("setuid bit survived extraction")
	}
	dirInfo, err := os.Stat(filepath.Join(dst, "bin", "dir"))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if dirInfo.Mode().Perm() != 0o755 {
		t.Fatalf("directory mode is %v, want 0755", dirInfo.Mode().Perm())
	}
	if dirInfo.Mode()&os.ModeSetgid != 0 {
		t.Fatalf("setgid bit survived extraction")
	}
}

// TestSafeExtractRejectsGarbage verifies that a malformed stream is an
// error, not a panic.
func TestSafeExtractRejectsGarbage(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "dst")
	if err := SafeExtract(bytes.NewReader([]byte("this is not a tar archive at all")), dst); err == nil {
		t.Fatalf("SafeExtract accepted garbage")
	}
}

// assertNoEscape verifies that nothing was written next to the destination.
func assertNoEscape(t *testing.T, parent, dst string) {
	t.Helper()
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatalf("read parent: %v", err)
	}
	for _, e := range entries {
		if e.Name() == filepath.Base(dst) {
			continue
		}
		t.Fatalf("unexpected file %q written outside the destination", e.Name())
	}
}
