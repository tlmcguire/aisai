// Package registry abstracts the package indexes AISAI can download from.
//
// An audit starts from a package name on some index. PyPI, npm and crates.io
// all answer the same three questions — what is this project, which versions
// exist, and where is the source archive — so the audit pipeline talks to the
// Registry interface and never to one index directly.
package registry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The ecosystem identifiers. They are the values of the "ecosystem" field of
// the HTTP API and the type of the package URLs (pkg:<id>/...).
const (
	PyPI  = "pypi"
	NPM   = "npm"
	Crate = "cargo"
)

// Info describes a project on an index.
type Info struct {
	// Name is the canonical project name.
	Name string
	// Summary is the short description.
	Summary string
	// Latest is the newest stable version.
	Latest string
	// Versions are the published versions, in no particular order.
	Versions []string
}

// Registry is one package index.
type Registry interface {
	// ID is the ecosystem identifier (PyPI, NPM, Crate).
	ID() string
	// Label is the name of the index as shown to the user.
	Label() string
	// Lookup resolves a project name.
	Lookup(ctx context.Context, name string) (*Info, error)
	// Download fetches the source archive of one version into destDir and
	// returns its path. An empty version means the latest release. The
	// returned version is the one that was downloaded.
	Download(ctx context.Context, name, version, destDir string) (path, resolved string, err error)
}

// ErrUnknownEcosystem is returned for an ecosystem AISAI cannot download from.
var ErrUnknownEcosystem = errors.New("unknown package ecosystem")

// Set is the registries available to the backend, by ecosystem id.
type Set struct {
	byID  map[string]Registry
	order []string
}

// NewSet returns a set holding the given registries.
func NewSet(regs ...Registry) *Set {
	s := &Set{byID: map[string]Registry{}}
	for _, r := range regs {
		if r == nil {
			continue
		}
		if _, dup := s.byID[r.ID()]; !dup {
			s.order = append(s.order, r.ID())
		}
		s.byID[r.ID()] = r
	}
	return s
}

// Get returns the registry of an ecosystem. An empty id means PyPI, which is
// what every client sent before ecosystems existed.
func (s *Set) Get(id string) (Registry, error) {
	if s == nil {
		return nil, fmt.Errorf("%w: no package index is configured", ErrUnknownEcosystem)
	}
	id = Normalize(id)
	r, ok := s.byID[id]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownEcosystem, id)
	}
	return r, nil
}

// All returns the registries in registration order.
func (s *Set) All() []Registry {
	if s == nil {
		return nil
	}
	out := make([]Registry, 0, len(s.order))
	for _, id := range s.order {
		out = append(out, s.byID[id])
	}
	return out
}

// Normalize maps the spellings of an ecosystem to its identifier.
func Normalize(id string) string {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case "", "pypi", "pip", "python":
		return PyPI
	case "npm", "node", "nodejs", "js":
		return NPM
	case "cargo", "crates", "crates.io", "rust":
		return Crate
	}
	return strings.ToLower(strings.TrimSpace(id))
}

// OSVEcosystem is the name the OSV database uses for an ecosystem.
func OSVEcosystem(id string) string {
	// Go modules are not one of the registries AISAI downloads from, but a
	// local project's go.mod lists them and OSV knows them as "Go".
	if strings.EqualFold(id, "go") {
		return "Go"
	}
	switch Normalize(id) {
	case NPM:
		return "npm"
	case Crate:
		return "crates.io"
	default:
		return "PyPI"
	}
}

// DepsDevSystem is the name deps.dev uses for an ecosystem.
func DepsDevSystem(id string) string {
	switch Normalize(id) {
	case NPM:
		return "NPM"
	case Crate:
		return "CARGO"
	default:
		return "PYPI"
	}
}

// ---------------------------------------------------------------------------
// shared HTTP plumbing
// ---------------------------------------------------------------------------

const (
	defaultTimeout = 30 * time.Second
	maxAttempts    = 3
	backoffBase    = 500 * time.Millisecond
	// maxArchiveBytes bounds one download; a source archive larger than this
	// is not something to unpack on a laptop.
	maxArchiveBytes = 512 << 20
)

type httpClient struct {
	http      *http.Client
	userAgent string
}

func newHTTP(timeout time.Duration, agent string) *httpClient {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	if strings.TrimSpace(agent) == "" {
		agent = "aisai (+https://github.com/tlmcguire/aisai)"
	}
	return &httpClient{http: &http.Client{Timeout: timeout}, userAgent: agent}
}

// ErrNotFound marks a project or version the index does not have.
var ErrNotFound = errors.New("not found")

// get issues a GET request, retrying transient failures with backoff.
func (c *httpClient) get(ctx context.Context, endpoint string, handle func(*http.Response) error) error {
	var last error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			t := time.NewTimer(backoffBase << (attempt - 1))
			select {
			case <-ctx.Done():
				t.Stop()
				return ctx.Err()
			case <-t.C:
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", c.userAgent)
		req.Header.Set("Accept", "application/json")
		resp, err := c.http.Do(req)
		if err != nil {
			last = fmt.Errorf("GET %s: %w", endpoint, err)
			continue
		}
		switch {
		case resp.StatusCode >= 500:
			resp.Body.Close()
			last = fmt.Errorf("GET %s: upstream error %s", endpoint, resp.Status)
			continue
		case resp.StatusCode == http.StatusNotFound:
			resp.Body.Close()
			return fmt.Errorf("GET %s: %w", endpoint, ErrNotFound)
		case resp.StatusCode >= 400:
			resp.Body.Close()
			return fmt.Errorf("GET %s: unexpected status %s", endpoint, resp.Status)
		}
		defer resp.Body.Close()
		return handle(resp)
	}
	return last
}

// download streams a URL to target, refusing anything over maxArchiveBytes,
// and returns the bytes written through w so the caller can hash them.
func (c *httpClient) download(ctx context.Context, remote, target string, w func(io.Writer) io.Writer) error {
	return c.get(ctx, remote, func(resp *http.Response) error {
		f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		var dst io.Writer = f
		if w != nil {
			dst = w(f)
		}
		n, err := io.Copy(dst, io.LimitReader(resp.Body, maxArchiveBytes+1))
		cerr := f.Close()
		if err == nil && n > maxArchiveBytes {
			err = fmt.Errorf("archive is larger than %d MB", maxArchiveBytes>>20)
		}
		if err == nil {
			err = cerr
		}
		if err != nil {
			_ = os.Remove(target)
			return err
		}
		return nil
	})
}

// safeFileName keeps a remote file name to characters that are safe on disk.
func safeFileName(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '.', r == '-', r == '_', r == '+':
			return r
		}
		return '-'
	}, s)
	if s == "" || strings.Trim(s, ".-") == "" {
		return "package.tgz"
	}
	return s
}

func ensureDir(dir string) error {
	if strings.TrimSpace(dir) == "" {
		return errors.New("destination directory is required")
	}
	return os.MkdirAll(dir, 0o755)
}

func join(dir, name string) string { return filepath.Join(dir, safeFileName(name)) }

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
