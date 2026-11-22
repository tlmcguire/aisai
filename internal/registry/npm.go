package registry

import (
	"context"
	"crypto/sha1" //nolint:gosec // npm publishes sha1 shasums; sha512 integrity is preferred when present
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultNPMURL is the public npm registry.
const DefaultNPMURL = "https://registry.npmjs.org"

// NPMOptions configures the npm client.
type NPMOptions struct {
	BaseURL   string
	Timeout   time.Duration
	UserAgent string
}

type npmRegistry struct {
	base string
	http *httpClient
}

// NewNPM returns a client for the npm registry.
func NewNPM(opts NPMOptions) Registry {
	base := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if base == "" {
		base = DefaultNPMURL
	}
	return &npmRegistry{base: base, http: newHTTP(opts.Timeout, opts.UserAgent)}
}

func (n *npmRegistry) ID() string    { return NPM }
func (n *npmRegistry) Label() string { return "npm" }

type npmDist struct {
	Tarball   string `json:"tarball"`
	Shasum    string `json:"shasum"`
	Integrity string `json:"integrity"`
}

type npmVersion struct {
	Name        string  `json:"name"`
	Version     string  `json:"version"`
	Description string  `json:"description"`
	Dist        npmDist `json:"dist"`
}

type npmPackument struct {
	Name     string                `json:"name"`
	DistTags map[string]string     `json:"dist-tags"`
	Versions map[string]npmVersion `json:"versions"`
}

// escapeName keeps the "@" of a scoped name and escapes the slash, which is
// the form the registry expects (@scope%2fname).
func escapeName(name string) string {
	if strings.HasPrefix(name, "@") {
		if scope, rest, ok := strings.Cut(name, "/"); ok {
			return scope + "%2f" + url.PathEscape(rest)
		}
	}
	return url.PathEscape(name)
}

func (n *npmRegistry) packument(ctx context.Context, name string) (*npmPackument, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("npm: package name is required")
	}
	var doc npmPackument
	err := n.http.get(ctx, n.base+"/"+escapeName(name), func(resp *http.Response) error {
		return json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(&doc)
	})
	if err != nil {
		return nil, fmt.Errorf("npm: %w", err)
	}
	return &doc, nil
}

func (n *npmRegistry) Lookup(ctx context.Context, name string) (*Info, error) {
	doc, err := n.packument(ctx, name)
	if err != nil {
		return nil, err
	}
	latest := doc.DistTags["latest"]
	info := &Info{Name: doc.Name, Latest: latest, Versions: sortedKeys(doc.Versions)}
	if info.Name == "" {
		info.Name = name
	}
	info.Summary = doc.Versions[latest].Description
	return info, nil
}

func (n *npmRegistry) Download(ctx context.Context, name, version, destDir string) (string, string, error) {
	doc, err := n.packument(ctx, name)
	if err != nil {
		return "", "", err
	}
	if version == "" {
		version = doc.DistTags["latest"]
	}
	v, ok := doc.Versions[version]
	if !ok {
		return "", "", fmt.Errorf("npm: %s has no version %q", name, version)
	}
	if v.Dist.Tarball == "" {
		return "", "", fmt.Errorf("npm: %s@%s has no tarball", name, version)
	}
	if err := ensureDir(destDir); err != nil {
		return "", "", err
	}
	target := join(destDir, strings.ReplaceAll(name, "/", "-")+"-"+version+".tgz")

	var h hash.Hash
	var want string
	switch {
	case strings.HasPrefix(v.Dist.Integrity, "sha512-"):
		h, want = sha512.New(), strings.TrimPrefix(v.Dist.Integrity, "sha512-")
	case v.Dist.Shasum != "":
		h, want = sha1.New(), v.Dist.Shasum //nolint:gosec
	}
	err = n.http.download(ctx, v.Dist.Tarball, target, func(w io.Writer) io.Writer {
		if h == nil {
			return w
		}
		return io.MultiWriter(w, h)
	})
	if err != nil {
		return "", "", fmt.Errorf("npm: download %s@%s: %w", name, version, err)
	}
	if h != nil {
		got := hex.EncodeToString(h.Sum(nil))
		if strings.HasPrefix(v.Dist.Integrity, "sha512-") {
			got = base64.StdEncoding.EncodeToString(h.Sum(nil))
		}
		if got != want {
			return "", "", fmt.Errorf("npm: %s@%s failed its integrity check", name, version)
		}
	}
	return target, version, nil
}
