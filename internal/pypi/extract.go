package pypi

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Extraction limits. They bound the damage a malicious or merely broken
// archive can do.
const (
	// MaxExtractBytes is the maximum total uncompressed size of an archive.
	MaxExtractBytes int64 = 512 << 20 // 512 MiB
	// MaxExtractEntries is the maximum number of entries in an archive.
	MaxExtractEntries = 50000
	// fileMode is the mode every extracted regular file receives.
	fileMode os.FileMode = 0o644
	// dirMode is the mode every extracted directory receives.
	dirMode os.FileMode = 0o755
)

// gzipMagic is the two byte signature of a gzip stream.
var gzipMagic = []byte{0x1f, 0x8b}

// SafeExtract extracts a tar archive into dst.
//
// The input is an attacker-controlled source distribution, so the extraction
// is deliberately strict:
//
//   - absolute entry names are rejected;
//   - any entry whose cleaned path escapes dst is rejected;
//   - symbolic and hard links that point outside dst are rejected instead of
//     being created;
//   - setuid, setgid and sticky bits are dropped: files are written with
//     mode 0644 and directories with mode 0755;
//   - the total uncompressed size is capped at MaxExtractBytes and the
//     number of entries at MaxExtractEntries;
//   - device and FIFO entries are skipped.
//
// Both gzip-compressed and plain tar streams are accepted; the compression is
// detected from the magic bytes. The first unsafe entry aborts the extraction
// with a descriptive error.
func SafeExtract(r io.Reader, dst string) error {
	if r == nil {
		return fmt.Errorf("pypi: extract: no archive stream")
	}
	if strings.TrimSpace(dst) == "" {
		return fmt.Errorf("pypi: extract: destination directory is required")
	}
	root, err := filepath.Abs(dst)
	if err != nil {
		return fmt.Errorf("pypi: extract: resolve %q: %w", dst, err)
	}
	if err := os.MkdirAll(root, dirMode); err != nil {
		return fmt.Errorf("pypi: extract: create %q: %w", root, err)
	}

	buffered := bufio.NewReader(r)
	stream := io.Reader(buffered)
	if magic, err := buffered.Peek(2); err == nil || len(magic) == 2 {
		if len(magic) == 2 && magic[0] == gzipMagic[0] && magic[1] == gzipMagic[1] {
			gz, err := gzip.NewReader(buffered)
			if err != nil {
				return fmt.Errorf("pypi: extract: gzip: %w", err)
			}
			defer gz.Close()
			stream = gz
		}
	}

	counter := &countingReader{r: stream}
	tr := tar.NewReader(counter)
	entries := 0
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("pypi: extract: read archive: %w", err)
		}
		entries++
		if entries > MaxExtractEntries {
			return fmt.Errorf("pypi: extract: archive has more than %d entries", MaxExtractEntries)
		}
		if counter.n > MaxExtractBytes {
			return fmt.Errorf("pypi: extract: archive exceeds the %d byte uncompressed limit", MaxExtractBytes)
		}
		if err := extractEntry(tr, hdr, root); err != nil {
			return err
		}
	}
}

// countingReader counts the bytes read from the underlying stream, which for
// a gzip archive is the uncompressed size.
type countingReader struct {
	r io.Reader
	n int64
}

// Read implements io.Reader.
func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// extractEntry writes a single archive entry into root.
func extractEntry(tr *tar.Reader, hdr *tar.Header, root string) error {
	rel, ok, err := safeRelPath(hdr.Name)
	if err != nil {
		return err
	}
	if !ok {
		// The entry refers to the destination directory itself; nothing to do.
		return nil
	}
	target := filepath.Join(root, filepath.FromSlash(rel))
	if !withinRoot(root, target) {
		return fmt.Errorf("pypi: extract: unsafe archive entry %q: path escapes the destination", hdr.Name)
	}

	switch hdr.Typeflag {
	case tar.TypeDir:
		if err := clearSymlinkTarget(target); err != nil {
			return fmt.Errorf("pypi: extract: replace %q: %w", hdr.Name, err)
		}
		if err := os.MkdirAll(target, dirMode); err != nil {
			return fmt.Errorf("pypi: extract: create directory %q: %w", hdr.Name, err)
		}
		if err := os.Chmod(target, dirMode); err != nil {
			return fmt.Errorf("pypi: extract: set mode on directory %q: %w", hdr.Name, err)
		}
		return nil

	case tar.TypeReg:
		return writeRegularFile(tr, hdr, root, rel, target)

	case tar.TypeSymlink:
		return createSymlink(hdr, rel, target)

	case tar.TypeLink:
		return createHardlink(hdr, rel, target, root)

	case tar.TypeChar, tar.TypeBlock, tar.TypeFifo:
		// Device and FIFO entries have no meaning for source code; skip them
		// rather than failing the whole extraction.
		return nil

	case tar.TypeXGlobalHeader, tar.TypeXHeader:
		return nil

	default:
		// Unknown entry types (sockets, GNU long names, ...) are skipped.
		return nil
	}
}

// writeRegularFile writes a regular archive entry to disk.
func writeRegularFile(tr *tar.Reader, hdr *tar.Header, root, rel, target string) error {
	if err := ensureParentDirs(root, rel); err != nil {
		return err
	}
	if err := checkNoSymlinkParents(root, rel); err != nil {
		return err
	}
	if err := clearSymlinkTarget(target); err != nil {
		return fmt.Errorf("pypi: extract: replace %q: %w", hdr.Name, err)
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fileMode)
	if err != nil {
		return fmt.Errorf("pypi: extract: create %q: %w", hdr.Name, err)
	}
	written, copyErr := io.Copy(f, io.LimitReader(tr, MaxExtractBytes+1))
	closeErr := f.Close()
	if copyErr != nil {
		return fmt.Errorf("pypi: extract: write %q: %w", hdr.Name, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("pypi: extract: close %q: %w", hdr.Name, closeErr)
	}
	if written > MaxExtractBytes {
		return fmt.Errorf("pypi: extract: entry %q exceeds the %d byte uncompressed limit", hdr.Name, MaxExtractBytes)
	}
	if err := os.Chmod(target, fileMode); err != nil {
		return fmt.Errorf("pypi: extract: set mode on %q: %w", hdr.Name, err)
	}
	return nil
}

// createSymlink creates a symbolic link after validating its target.
func createSymlink(hdr *tar.Header, rel, target string) error {
	link := hdr.Linkname
	if strings.TrimSpace(link) == "" {
		return fmt.Errorf("pypi: extract: unsafe archive entry %q: symlink without a target", hdr.Name)
	}
	if path.IsAbs(link) || filepath.IsAbs(link) || filepath.VolumeName(link) != "" {
		return fmt.Errorf("pypi: extract: unsafe archive entry %q: absolute symlink target %q", hdr.Name, link)
	}
	resolved := path.Clean(path.Join(path.Dir(rel), link))
	if resolved == ".." || strings.HasPrefix(resolved, "../") || path.IsAbs(resolved) {
		return fmt.Errorf("pypi: extract: unsafe archive entry %q: symlink target %q escapes the destination", hdr.Name, link)
	}
	if err := os.MkdirAll(filepath.Dir(target), dirMode); err != nil {
		return fmt.Errorf("pypi: extract: create directory for %q: %w", hdr.Name, err)
	}
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("pypi: extract: replace %q: %w", hdr.Name, err)
	}
	if err := os.Symlink(link, target); err != nil {
		return fmt.Errorf("pypi: extract: create symlink %q: %w", hdr.Name, err)
	}
	return nil
}

// createHardlink creates a hard link after validating its target.
func createHardlink(hdr *tar.Header, rel, target, root string) error {
	linkRel, ok, err := safeRelPath(hdr.Linkname)
	if err != nil {
		return fmt.Errorf("pypi: extract: unsafe archive entry %q: %w", hdr.Name, err)
	}
	if !ok {
		return fmt.Errorf("pypi: extract: unsafe archive entry %q: hard link to the destination directory", hdr.Name)
	}
	source := filepath.Join(root, filepath.FromSlash(linkRel))
	if !withinRoot(root, source) {
		return fmt.Errorf("pypi: extract: unsafe archive entry %q: hard link target %q escapes the destination", hdr.Name, hdr.Linkname)
	}
	if _, err := os.Lstat(source); err != nil {
		return fmt.Errorf("pypi: extract: unsafe archive entry %q: hard link target %q not found", hdr.Name, hdr.Linkname)
	}
	if err := ensureParentDirs(root, rel); err != nil {
		return err
	}
	if err := checkNoSymlinkParents(root, rel); err != nil {
		return err
	}
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("pypi: extract: replace %q: %w", hdr.Name, err)
	}
	if err := os.Link(source, target); err != nil {
		return fmt.Errorf("pypi: extract: create hard link %q: %w", hdr.Name, err)
	}
	return nil
}

// clearSymlinkTarget removes target when it already exists as a symlink.
//
// A later archive entry must never be written through a symlink created by an
// earlier entry, so the link is removed before the new entry is created.
func clearSymlinkTarget(target string) error {
	info, err := os.Lstat(target)
	if err != nil {
		return nil
	}
	if info.Mode()&os.ModeSymlink != 0 {
		if err := os.Remove(target); err != nil {
			return err
		}
	}
	return nil
}

// ensureParentDirs creates the parent directories of an entry.
func ensureParentDirs(root, rel string) error {
	dir := filepath.Dir(filepath.Join(root, filepath.FromSlash(rel)))
	if dir == "" || dir == "." {
		return nil
	}
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("pypi: extract: create directory for %q: %w", rel, err)
	}
	return nil
}

// checkNoSymlinkParents refuses to write through a symlinked directory.
//
// A malicious archive can create a symlink and then write a file "through"
// it; refusing to follow symlinks in parent directories removes that class of
// attack entirely.
func checkNoSymlinkParents(root, rel string) error {
	dir := path.Dir(rel)
	if dir == "." || dir == "" {
		return nil
	}
	current := root
	for _, part := range strings.Split(dir, "/") {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			// The directory does not exist yet; it was just created by
			// ensureParentDirs, so it cannot be a symlink.
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("pypi: extract: refusing to write %q through symlinked directory %q", rel, current)
		}
	}
	return nil
}

// safeRelPath validates an archive entry name.
//
// It returns the cleaned, slash-separated relative path of the entry. The
// second return value is false when the entry refers to the destination
// directory itself and can be ignored.
func safeRelPath(name string) (string, bool, error) {
	if name == "" {
		return "", false, fmt.Errorf("pypi: extract: unsafe archive entry: empty name")
	}
	if strings.ContainsRune(name, 0) {
		return "", false, fmt.Errorf("pypi: extract: unsafe archive entry %q: contains a NUL byte", name)
	}
	if path.IsAbs(name) || filepath.IsAbs(name) || filepath.VolumeName(name) != "" {
		return "", false, fmt.Errorf("pypi: extract: unsafe archive entry %q: absolute path", name)
	}
	clean := path.Clean(name)
	if clean == "." {
		return "", false, nil
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false, fmt.Errorf("pypi: extract: unsafe archive entry %q: path escapes the destination", name)
	}
	if path.IsAbs(clean) {
		return "", false, fmt.Errorf("pypi: extract: unsafe archive entry %q: absolute path", name)
	}
	return clean, true, nil
}

// withinRoot reports whether target is inside root.
func withinRoot(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return false
	}
	return true
}
