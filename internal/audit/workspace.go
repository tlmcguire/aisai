package audit

import (
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/tlmcguire/aisai/internal/ast"
	"github.com/tlmcguire/aisai/internal/model"
	"github.com/tlmcguire/aisai/internal/pypi"
)

// extractTarball extracts a downloaded sdist into dst.
//
// Both tarballs and wheels (zip) are supported. Every extraction goes
// through the path-traversal-safe extractors below — a PyPI sdist is
// attacker-controlled input and the legacy implementation extracted it with
// an unguarded tar.extractall().
func extractTarball(tarballPath, dst string) error {
	f, err := os.Open(tarballPath)
	if err != nil {
		return fmt.Errorf("open tarball: %w", err)
	}
	defer f.Close()

	lower := strings.ToLower(tarballPath)
	switch {
	case strings.HasSuffix(lower, ".zip"), strings.HasSuffix(lower, ".whl"):
		return safeUnzip(f, dst)
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		gz, err := gzip.NewReader(f)
		if err != nil {
			return fmt.Errorf("open gzip: %w", err)
		}
		defer gz.Close()
		return pypi.SafeExtract(gz, dst)
	case strings.HasSuffix(lower, ".tar"):
		return pypi.SafeExtract(f, dst)
	default:
		// Sniff the magic bytes rather than trusting the extension.
		buf := make([]byte, 2)
		if _, err := f.ReadAt(buf, 0); err != nil {
			return fmt.Errorf("read tarball header: %w", err)
		}
		if buf[0] == 0x1f && buf[1] == 0x8b {
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				return err
			}
			gz, err := gzip.NewReader(f)
			if err != nil {
				return fmt.Errorf("open gzip: %w", err)
			}
			defer gz.Close()
			return pypi.SafeExtract(gz, dst)
		}
		if buf[0] == 'P' && buf[1] == 'K' {
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				return err
			}
			return safeUnzip(f, dst)
		}
		return fmt.Errorf("unsupported archive format: %s", filepath.Base(tarballPath))
	}
}

// safeUnzip extracts a zip archive with the same path-traversal protections
// as safeExtract: entries that escape the destination are rejected, and
// symlinks (stored as regular files by the zip format) are written as
// regular files.
func safeUnzip(f *os.File, dst string) error {
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat zip: %w", err)
	}
	zr, err := zip.NewReader(f, info.Size())
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	root, err := filepath.EvalSymlinks(dst)
	if err != nil {
		root = filepath.Clean(dst)
	}
	const maxEntries = 50000
	if len(zr.File) > maxEntries {
		return fmt.Errorf("zip has too many entries (%d)", len(zr.File))
	}
	var total int64
	for _, zf := range zr.File {
		name := filepath.Clean(filepath.FromSlash(zf.Name))
		if name == "." || name == string(filepath.Separator) {
			continue
		}
		if filepath.IsAbs(name) || strings.HasPrefix(name, "..") {
			return fmt.Errorf("zip entry escapes destination: %q", zf.Name)
		}
		target := filepath.Join(root, name)
		if target != root && !strings.HasPrefix(target, root+string(os.PathSeparator)) {
			return fmt.Errorf("zip entry escapes destination: %q", zf.Name)
		}
		if zf.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if !strings.HasSuffix(strings.ToLower(zf.Name), ".py") &&
			!strings.HasSuffix(strings.ToLower(zf.Name), ".pyi") &&
			!isInterestingTextFile(zf.Name) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			rc.Close()
			return err
		}
		n, err := io.Copy(out, io.LimitReader(rc, 8<<20))
		rc.Close()
		out.Close()
		if err != nil {
			return err
		}
		total += n
		if total > 512<<20 {
			return fmt.Errorf("zip exceeds the maximum uncompressed size")
		}
	}
	return nil
}

// isInterestingTextFile reports whether a file is worth keeping from an
// archive. Wheels contain a lot of data that is never scanned.
func isInterestingTextFile(name string) bool {
	lower := strings.ToLower(name)
	switch filepath.Ext(lower) {
	case ".py", ".pyi", ".toml", ".cfg", ".ini", ".txt", ".json", ".yaml", ".yml", ".md", ".rst":
		return true
	}
	return false
}

// parseTree parses every Python file under dir into an ast.File.
//
// The returned paths are relative to the audit root, so findings stay stable
// regardless of where the workspace lives on disk. A path relative to the
// root is what every other part of the system expects: the file tree, the
// findings store and the patch applier all key on it.
func parseTree(dir, root, auditID string) ([]*ast.File, error) {
	var files []*ast.File
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip unreadable entries rather than aborting
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "__pycache__", "node_modules", ".tox", ".venv", "venv":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(info.Name(), ".py") {
			return nil
		}
		rel := relToRoot(root, path)
		f, _ := ast.Parse(path)
		if f == nil {
			return nil
		}
		f.Path = rel
		// Re-stamp the chunk metadata with the audit id and relative path.
		for i := range f.Chunks {
			f.Chunks[i].AuditID = auditID
			f.Chunks[i].File = rel
			f.Chunks[i].ID = model.FindingID(rel, f.Chunks[i].StartLine, "", f.Chunks[i].SymbolPath)
		}
		files = append(files, f)
		return nil
	})
	if err != nil {
		return files, err
	}
	return files, nil
}

// relToRoot returns the workspace-relative, slash-separated path of an
// absolute path inside the audit root.
//
// Every path that leaves the backend is expressed this way. Exposing the
// absolute path would leak the local filesystem layout into the UI and into
// exported reports, and it would break the file view: the client resolves a
// path against the workspace root, so a path that already contains the root
// would be looked up in the wrong place.
func relToRoot(root, abs string) string {
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return model.RelPath(abs)
	}
	return model.RelPath(rel)
}
