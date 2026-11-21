// Package pypi is the PyPI client and the safe sdist extractor used by the
// AISAI backend.
//
// Nothing in this package ever executes the downloaded package: source
// distributions are fetched as opaque bytes and unpacked with SafeExtract,
// which refuses any archive that tries to write outside its destination.
package pypi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DefaultBaseURL is the public PyPI JSON API endpoint.
const DefaultBaseURL = "https://pypi.org"

// DefaultTimeout is the default request timeout for a PyPI client.
const DefaultTimeout = 30 * time.Second

// defaultUserAgent identifies AISAI to PyPI and upstream mirrors.
const defaultUserAgent = "aisai/0.1 (+https://github.com/tlmcguire/aisai)"

// maxAttempts is the number of times a request is attempted before failing.
const maxAttempts = 3

// backoffBase is the delay before the second attempt; it doubles per retry.
const backoffBase = 500 * time.Millisecond

// ClientOptions configures a PyPI Client.
type ClientOptions struct {
	// BaseURL is the API root. It defaults to "https://pypi.org".
	BaseURL string
	// Timeout bounds a single request. It defaults to 30s.
	Timeout time.Duration
	// UserAgent is sent with every request. It defaults to an AISAI agent.
	UserAgent string
}

// Client is a minimal PyPI JSON API client.
type Client struct {
	baseURL   string
	userAgent string
	http      *http.Client
}

// PackageInfo describes one release of a PyPI project.
type PackageInfo struct {
	// Name is the project name.
	Name string
	// Version is the resolved release version.
	Version string
	// Summary is the short project description.
	Summary string
	// License is the declared license.
	License string
	// Homepage is the project home page.
	Homepage string
	// SdistURL is the URL of the source distribution, or "" when the release
	// does not ship one.
	SdistURL string
	// WheelURL is the URL of the built wheel, used when no sdist exists.
	WheelURL string
}

// NewClient returns a PyPI client configured by opts.
func NewClient(opts ClientOptions) *Client {
	base := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if base == "" {
		base = DefaultBaseURL
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	agent := strings.TrimSpace(opts.UserAgent)
	if agent == "" {
		agent = defaultUserAgent
	}
	return &Client{
		baseURL:   base,
		userAgent: agent,
		http:      &http.Client{Timeout: timeout},
	}
}

// releaseFile is one distribution file of a release.
type releaseFile struct {
	Packagetype string `json:"packagetype"`
	Filename    string `json:"filename"`
	URL         string `json:"url"`
	Size        int64  `json:"size"`
}

// releaseInfo is the "info" object of a PyPI JSON response.
type releaseInfo struct {
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Summary      string   `json:"summary"`
	License      string   `json:"license"`
	HomePage     string   `json:"home_page"`
	ProjectURL   string   `json:"project_url"`
	RequiresDist []string `json:"requires_dist"`
}

// releaseResponse is the JSON document returned by the PyPI JSON API.
type releaseResponse struct {
	Info     releaseInfo              `json:"info"`
	URLs     []releaseFile            `json:"urls"`
	Releases map[string][]releaseFile `json:"releases"`
}

// Package returns metadata for a specific release, or for the latest release
// when version is empty.
//
// When version is empty the highest stable release is chosen; if the project
// only ships pre-releases, the highest pre-release is used.
func (c *Client) Package(ctx context.Context, name, version string) (*PackageInfo, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("pypi: package name is required")
	}
	if strings.TrimSpace(version) == "" {
		return c.latestPackage(ctx, name)
	}
	var resp releaseResponse
	endpoint := "/pypi/" + url.PathEscape(name) + "/" + url.PathEscape(version) + "/json"
	if err := c.getJSON(ctx, endpoint, &resp); err != nil {
		return nil, err
	}
	return infoFromRelease(&resp, name, version), nil
}

// latestPackage resolves the newest release of a project.
func (c *Client) latestPackage(ctx context.Context, name string) (*PackageInfo, error) {
	var resp releaseResponse
	endpoint := "/pypi/" + url.PathEscape(name) + "/json"
	if err := c.getJSON(ctx, endpoint, &resp); err != nil {
		return nil, err
	}
	version := latestVersionFromReleases(resp.Releases)
	if version == "" {
		version = resp.Info.Version
	}
	if version == "" {
		return nil, fmt.Errorf("pypi: %s has no releases", name)
	}
	if resp.Info.Version == version {
		return infoFromRelease(&resp, name, version), nil
	}
	// The project index describes the newest release only; fetch the chosen
	// one so the metadata belongs to the version we report.
	var exact releaseResponse
	exactEndpoint := "/pypi/" + url.PathEscape(name) + "/" + url.PathEscape(version) + "/json"
	if err := c.getJSON(ctx, exactEndpoint, &exact); err == nil {
		return infoFromRelease(&exact, name, version), nil
	}
	return infoFromRelease(&resp, name, version), nil
}

// latestVersionFromReleases picks the highest stable release, falling back
// to the highest release of any kind.
func latestVersionFromReleases(releases map[string][]releaseFile) string {
	var stable, all []string
	for v := range releases {
		all = append(all, v)
		if !IsPrerelease(v) {
			stable = append(stable, v)
		}
	}
	sort.Strings(stable)
	sort.Strings(all)
	if v := LatestVersion(stable, ""); v != "" {
		return v
	}
	return LatestVersion(all, "")
}

// infoFromRelease converts a PyPI JSON response into a PackageInfo.
func infoFromRelease(resp *releaseResponse, name, version string) *PackageInfo {
	info := &PackageInfo{
		Name:     strings.TrimSpace(resp.Info.Name),
		Version:  strings.TrimSpace(resp.Info.Version),
		Summary:  resp.Info.Summary,
		License:  resp.Info.License,
		Homepage: firstNonEmpty(resp.Info.HomePage, resp.Info.ProjectURL),
	}
	if info.Name == "" {
		info.Name = name
	}
	if info.Version == "" {
		info.Version = version
	}
	sdist, wheel := pickFiles(resp.URLs)
	info.SdistURL = sdist
	info.WheelURL = wheel
	return info
}

// pickFiles selects the source distribution and the wheel from a release.
func pickFiles(files []releaseFile) (sdist string, wheel string) {
	for _, f := range files {
		switch f.Packagetype {
		case "sdist":
			if sdist == "" || isTarGz(f.Filename) {
				sdist = f.URL
			}
			if isTarGz(f.Filename) {
				return sdist, wheel
			}
		case "bdist_wheel":
			if wheel == "" {
				wheel = f.URL
			}
		}
	}
	return sdist, wheel
}

// isTarGz reports whether a filename is a gzip-compressed tarball.
func isTarGz(name string) bool {
	return strings.HasSuffix(strings.ToLower(name), ".tar.gz")
}

// firstNonEmpty returns the first non-empty string.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// Releases returns the versions published for a project, sorted ascending.
func (c *Client) Releases(ctx context.Context, name string) ([]string, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("pypi: package name is required")
	}
	var resp releaseResponse
	endpoint := "/pypi/" + url.PathEscape(name) + "/json"
	if err := c.getJSON(ctx, endpoint, &resp); err != nil {
		return nil, err
	}
	versions := make([]string, 0, len(resp.Releases))
	for v := range resp.Releases {
		versions = append(versions, v)
	}
	sort.Slice(versions, func(i, j int) bool {
		return CompareVersions(versions[i], versions[j]) < 0
	})
	return versions, nil
}

// RequiresDist returns the dependency specifications declared by a release.
//
// An empty version uses the newest release of the project.
func (c *Client) RequiresDist(ctx context.Context, name, version string) ([]string, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("pypi: package name is required")
	}
	var resp releaseResponse
	endpoint := "/pypi/" + url.PathEscape(name)
	if strings.TrimSpace(version) != "" {
		endpoint += "/" + url.PathEscape(version)
	}
	endpoint += "/json"
	if err := c.getJSON(ctx, endpoint, &resp); err != nil {
		return nil, err
	}
	return resp.Info.RequiresDist, nil
}

// DownloadSdist downloads the source distribution of a release into destDir
// and returns the path of the downloaded file.
//
// The download is streamed to disk; the archive is never buffered in memory.
// When the release ships no source distribution, the built wheel is
// downloaded instead and its path is returned, so the caller can decide what
// to do with it.
func (c *Client) DownloadSdist(ctx context.Context, name, version, destDir string) (string, error) {
	info, err := c.Package(ctx, name, version)
	if err != nil {
		return "", err
	}
	remote := info.SdistURL
	if remote == "" {
		remote = info.WheelURL
	}
	if remote == "" {
		return "", fmt.Errorf("pypi: %s@%s has neither a source distribution nor a wheel", name, info.Version)
	}
	if strings.TrimSpace(destDir) == "" {
		return "", fmt.Errorf("pypi: destination directory is required")
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("pypi: create %s: %w", destDir, err)
	}
	filename := downloadFilename(remote, name, info.Version)
	target := filepath.Join(destDir, filename)
	if err := c.download(ctx, remote, target); err != nil {
		return "", err
	}
	return target, nil
}

// downloadFilename derives a safe local file name for a remote archive.
func downloadFilename(remote, name, version string) string {
	base := path.Base(strings.SplitN(remote, "?", 2)[0])
	base = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '.', r == '-', r == '_', r == '+':
			return r
		default:
			return '-'
		}
	}, base)
	if base == "" || base == "." || base == "-" {
		base = fmt.Sprintf("%s-%s.tar.gz", name, version)
	}
	return base
}

// getJSON performs a GET request and decodes the JSON body.
func (c *Client) getJSON(ctx context.Context, endpoint string, out any) error {
	return c.do(ctx, c.baseURL+endpoint, func(resp *http.Response) error {
		defer resp.Body.Close()
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("pypi: decode %s: %w", endpoint, err)
		}
		return nil
	})
}

// download streams a remote file to a local path.
func (c *Client) download(ctx context.Context, remote, target string) error {
	return c.do(ctx, remote, func(resp *http.Response) error {
		defer resp.Body.Close()
		f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return fmt.Errorf("pypi: create %s: %w", target, err)
		}
		if _, err := io.Copy(f, resp.Body); err != nil {
			f.Close()
			os.Remove(target)
			return fmt.Errorf("pypi: download %s: %w", remote, err)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("pypi: close %s: %w", target, err)
		}
		return nil
	})
}

// do issues a GET request, retrying transient failures with backoff.
//
// Network errors and 5xx responses are retried up to maxAttempts times with
// an exponentially growing delay. Client errors are returned immediately.
func (c *Client) do(ctx context.Context, endpoint string, handle func(*http.Response) error) error {
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			delay := backoffBase << (attempt - 1)
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return fmt.Errorf("pypi: GET %s: %w", endpoint, ctx.Err())
			case <-timer.C:
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return fmt.Errorf("pypi: GET %s: %w", endpoint, err)
		}
		req.Header.Set("User-Agent", c.userAgent)
		req.Header.Set("Accept", "application/json")
		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = describeTransportError(endpoint, err)
			continue
		}
		if resp.StatusCode >= 500 {
			resp.Body.Close()
			lastErr = fmt.Errorf("pypi: GET %s: upstream error %s", endpoint, resp.Status)
			continue
		}
		if resp.StatusCode == http.StatusNotFound {
			resp.Body.Close()
			return fmt.Errorf("pypi: GET %s: not found", endpoint)
		}
		if resp.StatusCode >= 400 {
			resp.Body.Close()
			return fmt.Errorf("pypi: GET %s: unexpected status %s", endpoint, resp.Status)
		}
		if err := handle(resp); err != nil {
			return err
		}
		return nil
	}
	return lastErr
}

// describeTransportError renders a network failure with a timeout-aware
// message.
func describeTransportError(endpoint string, err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("pypi: GET %s: timeout: %w", endpoint, err)
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return fmt.Errorf("pypi: GET %s: timeout: %w", endpoint, err)
	}
	return fmt.Errorf("pypi: GET %s: %w", endpoint, err)
}
