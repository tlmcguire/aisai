package external

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultCacheDir is the directory the external corpora are cached in.
//
// It is relative to the working directory of the benchmark, so a run leaves
// the cache next to the repository it was started from. It is not part of the
// repository itself: the corpora are not vendored.
const DefaultCacheDir = ".aisai-bench-cache"

// maxArtefactBytes bounds a download.
//
// The corpora are a few megabytes each; the bound is a guard against a
// misdirected URL filling the disk.
const maxArtefactBytes = 512 << 20

// downloadTimeout bounds one artefact download.
const downloadTimeout = 10 * time.Minute

// Artefact is one downloadable file of a corpus.
type Artefact struct {
	// Name is the file name of the artefact inside the cache directory.
	Name string
	// URL is the pinned location the artefact is downloaded from.
	//
	// "Pinned" means the URL addresses a specific immutable revision —
	// a commit archive, not a branch head — so that the same URL always
	// yields the same bytes.
	URL string
	// SHA256 is the expected digest of the file, in hexadecimal.
	//
	// When it is empty the digest of the downloaded file is computed and
	// recorded next to it, but not enforced. The digest of every artefact
	// that was used is written into the report, so a number can always be
	// tied to the bytes it was measured on.
	SHA256 string
}

// FetchResult describes a cached artefact.
type FetchResult struct {
	// Path is the location of the artefact in the cache.
	Path string
	// SHA256 is the digest of the file.
	SHA256 string
	// FromCache reports whether the artefact was already present instead
	// of being downloaded.
	FromCache bool
}

// Fetch returns the artefact in the cache directory, downloading it when it is
// not there yet.
//
// In offline mode nothing is downloaded: an artefact that is not already in
// the cache is an error. The digest is verified against the expected digest of
// the artefact when one is pinned, and it is always recorded, so that the
// report can name the exact bytes the measurement was made on.
func Fetch(ctx context.Context, cacheDir string, a Artefact, offline bool) (*FetchResult, error) {
	if strings.TrimSpace(a.Name) == "" {
		return nil, fmt.Errorf("external: the artefact has no name")
	}
	if strings.TrimSpace(a.URL) == "" {
		return nil, fmt.Errorf("external: the artefact %q has no URL", a.Name)
	}
	if cacheDir == "" {
		cacheDir = DefaultCacheDir
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, fmt.Errorf("external: create the cache directory %s: %w", cacheDir, err)
	}
	path := filepath.Join(cacheDir, a.Name)

	if _, err := os.Stat(path); err == nil {
		digest, err := FileSHA256(path)
		if err != nil {
			return nil, err
		}
		if err := verifyDigest(a, digest); err != nil {
			return nil, err
		}
		return &FetchResult{Path: path, SHA256: digest, FromCache: true}, nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("external: stat %s: %w", path, err)
	}

	if offline {
		return nil, fmt.Errorf(
			"external: offline mode and %s is not in the cache: fetch it first, or drop --offline", path)
	}

	digest, err := download(ctx, a, path)
	if err != nil {
		return nil, err
	}
	return &FetchResult{Path: path, SHA256: digest}, nil
}

// download writes the artefact to path and returns its digest.
//
// The file is written to a temporary name first and renamed afterwards, so
// that an interrupted download never leaves a truncated file in the cache
// that a later run would treat as complete.
func download(ctx context.Context, a Artefact, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return "", fmt.Errorf("external: build the request for %s: %w", a.URL, err)
	}
	client := &http.Client{Timeout: downloadTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("external: download %s: %w", a.URL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("external: download %s: the server answered %s", a.URL, resp.Status)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("external: create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".part-*")
	if err != nil {
		return "", fmt.Errorf("external: create a temporary file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	hasher := sha256.New()
	limited := io.LimitReader(resp.Body, maxArtefactBytes)
	written, err := io.Copy(io.MultiWriter(tmp, hasher), limited)
	if err != nil {
		tmp.Close()
		return "", fmt.Errorf("external: write %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("external: close %s: %w", tmpName, err)
	}
	if written >= maxArtefactBytes {
		return "", fmt.Errorf("external: %s is larger than the %d byte limit", a.URL, maxArtefactBytes)
	}

	digest := hex.EncodeToString(hasher.Sum(nil))
	if err := verifyDigest(a, digest); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return "", fmt.Errorf("external: rename %s to %s: %w", tmpName, path, err)
	}
	return digest, nil
}

// verifyDigest compares the digest of a file with the pinned one.
func verifyDigest(a Artefact, digest string) error {
	want := strings.ToLower(strings.TrimSpace(a.SHA256))
	if want == "" {
		return nil
	}
	if want != digest {
		return fmt.Errorf(
			"external: %s has the digest %s but %s was expected: the pinned artefact does not match",
			a.Name, digest, want)
	}
	return nil
}

// FileSHA256 returns the SHA-256 digest of a file, in hexadecimal.
func FileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("external: open %s: %w", path, err)
	}
	defer f.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return "", fmt.Errorf("external: read %s: %w", path, err)
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}
