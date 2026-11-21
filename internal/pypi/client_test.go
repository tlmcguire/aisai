package pypi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newTestClient returns a client that talks to the given test server.
func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	return NewClient(ClientOptions{BaseURL: srv.URL, Timeout: 5 * time.Second, UserAgent: "aisai-test/1.0"})
}

// releaseJSON builds a PyPI JSON document for one release.
func releaseJSON(name, version, urls string) string {
	return fmt.Sprintf(`{
  "info": {
    "name": %q,
    "version": %q,
    "summary": "A tiny test package",
    "license": "MIT",
    "home_page": "https://example.test/",
    "requires_dist": ["pytest>=7.0", "six ; python_version < \"3.9\""]
  },
  "urls": [%s],
  "releases": {
    %q: [%s]
  }
}`, name, version, urls, version, urls)
}

// TestPackageSpecificVersion verifies the version specific endpoint.
func TestPackageSpecificVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/pypi/flask/2.0.0/json"; got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		if got := r.Header.Get("User-Agent"); got != "aisai-test/1.0" {
			t.Errorf("user agent = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(releaseJSON("flask", "2.0.0",
			`{"packagetype":"sdist","filename":"flask-2.0.0.tar.gz","url":"https://files.test/flask-2.0.0.tar.gz","size":10}`)))
	}))
	defer srv.Close()

	info, err := newTestClient(t, srv).Package(context.Background(), "flask", "2.0.0")
	if err != nil {
		t.Fatalf("Package: %v", err)
	}
	if info.Name != "flask" || info.Version != "2.0.0" {
		t.Fatalf("unexpected package info %+v", info)
	}
	if info.Summary == "" || info.License != "MIT" || info.Homepage == "" {
		t.Fatalf("missing metadata %+v", info)
	}
	if info.SdistURL != "https://files.test/flask-2.0.0.tar.gz" {
		t.Fatalf("unexpected sdist URL %q", info.SdistURL)
	}
}

// TestPackageLatestVersion verifies that the newest stable release is picked.
func TestPackageLatestVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/pypi/demo/json":
			_, _ = w.Write([]byte(`{
  "info": {"name": "demo", "version": "2.0.0b1", "summary": "s"},
  "urls": [],
  "releases": {
    "1.0.0": [{"packagetype":"sdist","filename":"demo-1.0.0.tar.gz","url":"https://files.test/demo-1.0.0.tar.gz"}],
    "1.5.0": [{"packagetype":"sdist","filename":"demo-1.5.0.tar.gz","url":"https://files.test/demo-1.5.0.tar.gz"}],
    "2.0.0b1": [{"packagetype":"sdist","filename":"demo-2.0.0b1.tar.gz","url":"https://files.test/demo-2.0.0b1.tar.gz"}]
  }
}`))
		case "/pypi/demo/1.5.0/json":
			_, _ = w.Write([]byte(releaseJSON("demo", "1.5.0",
				`{"packagetype":"sdist","filename":"demo-1.5.0.tar.gz","url":"https://files.test/demo-1.5.0.tar.gz"}`)))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	info, err := newTestClient(t, srv).Package(context.Background(), "demo", "")
	if err != nil {
		t.Fatalf("Package: %v", err)
	}
	if info.Version != "1.5.0" {
		t.Fatalf("version = %q, want 1.5.0", info.Version)
	}
	if info.SdistURL != "https://files.test/demo-1.5.0.tar.gz" {
		t.Fatalf("unexpected sdist URL %q", info.SdistURL)
	}
}

// TestPackageNotFound verifies that a missing package is an error.
func TestPackageNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	if _, err := newTestClient(t, srv).Package(context.Background(), "nope", "1.0"); err == nil {
		t.Fatalf("expected an error for a missing package")
	}
}

// TestPackageRetriesOnServerError verifies the retry behavior.
func TestPackageRetriesOnServerError(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(releaseJSON("flask", "2.0.0",
			`{"packagetype":"sdist","filename":"flask-2.0.0.tar.gz","url":"https://files.test/flask-2.0.0.tar.gz"}`)))
	}))
	defer srv.Close()

	info, err := newTestClient(t, srv).Package(context.Background(), "flask", "2.0.0")
	if err != nil {
		t.Fatalf("Package: %v", err)
	}
	if info.Version != "2.0.0" {
		t.Fatalf("unexpected version %q", info.Version)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("server was called %d times, want 3", got)
	}
}

// TestPackageFailsAfterMaxAttempts verifies that the client gives up.
func TestPackageFailsAfterMaxAttempts(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	if _, err := newTestClient(t, srv).Package(context.Background(), "flask", "2.0.0"); err == nil {
		t.Fatalf("expected an error")
	}
	if got := atomic.LoadInt32(&calls); got != maxAttempts {
		t.Fatalf("server was called %d times, want %d", got, maxAttempts)
	}
}

// TestDownloadSdist verifies that the archive is streamed to disk.
func TestDownloadSdist(t *testing.T) {
	payload := []byte("archive bytes")
	var baseURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/pypi/flask/2.0.0/json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(releaseJSON("flask", "2.0.0",
			`{"packagetype":"sdist","filename":"flask-2.0.0.tar.gz","url":"`+baseURL+`/files/flask-2.0.0.tar.gz"}`)))
	})
	mux.HandleFunc("/files/flask-2.0.0.tar.gz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	baseURL = srv.URL

	client := newTestClient(t, srv)

	dest := t.TempDir()
	path, err := client.DownloadSdist(context.Background(), "flask", "2.0.0", dest)
	if err != nil {
		t.Fatalf("DownloadSdist: %v", err)
	}
	if filepath.Base(path) != "flask-2.0.0.tar.gz" {
		t.Fatalf("unexpected file name %q", filepath.Base(path))
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read download: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("downloaded %q, want %q", got, payload)
	}
}

// TestDownloadSdistFallsBackToWheel verifies the wheel fallback.
func TestDownloadSdistFallsBackToWheel(t *testing.T) {
	var baseURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/pypi/demo/1.0/json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(releaseJSON("demo", "1.0",
			`{"packagetype":"bdist_wheel","filename":"demo-1.0-py3-none-any.whl","url":"`+baseURL+`/files/demo-1.0-py3-none-any.whl"}`)))
	})
	mux.HandleFunc("/files/demo-1.0-py3-none-any.whl", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("wheel bytes"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	baseURL = srv.URL

	client := newTestClient(t, srv)

	dest := t.TempDir()
	path, err := client.DownloadSdist(context.Background(), "demo", "1.0", dest)
	if err != nil {
		t.Fatalf("DownloadSdist: %v", err)
	}
	if !strings.HasSuffix(path, ".whl") {
		t.Fatalf("expected a wheel, got %q", path)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read download: %v", err)
	}
	if string(got) != "wheel bytes" {
		t.Fatalf("downloaded %q", got)
	}
}

// TestRequiresDist verifies the dependency metadata accessor.
func TestRequiresDist(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(releaseJSON("flask", "2.0.0", "")))
	}))
	defer srv.Close()

	requires, err := newTestClient(t, srv).RequiresDist(context.Background(), "flask", "2.0.0")
	if err != nil {
		t.Fatalf("RequiresDist: %v", err)
	}
	if len(requires) != 2 || requires[0] != "pytest>=7.0" {
		t.Fatalf("unexpected requirements %v", requires)
	}
}

// TestReleases verifies the release listing.
func TestReleases(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"info": {"name": "demo"}, "releases": {
  "1.10.0": [], "1.9.0": [], "1.2.0": [], "2.0.0rc1": []
}}`))
	}))
	defer srv.Close()

	versions, err := newTestClient(t, srv).Releases(context.Background(), "demo")
	if err != nil {
		t.Fatalf("Releases: %v", err)
	}
	want := []string{"1.2.0", "1.9.0", "1.10.0", "2.0.0rc1"}
	if len(versions) != len(want) {
		t.Fatalf("versions = %v, want %v", versions, want)
	}
	for i := range want {
		if versions[i] != want[i] {
			t.Fatalf("versions = %v, want %v", versions, want)
		}
	}
}
