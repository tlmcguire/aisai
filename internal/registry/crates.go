package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultCratesURL is the public crates.io API.
const DefaultCratesURL = "https://crates.io"

// CratesOptions configures the crates.io client.
type CratesOptions struct {
	BaseURL   string
	Timeout   time.Duration
	UserAgent string
}

type cratesRegistry struct {
	base string
	http *httpClient
}

// NewCrates returns a client for crates.io.
//
// crates.io requires a descriptive User-Agent, so the caller's is used as is.
func NewCrates(opts CratesOptions) Registry {
	base := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if base == "" {
		base = DefaultCratesURL
	}
	return &cratesRegistry{base: base, http: newHTTP(opts.Timeout, opts.UserAgent)}
}

func (c *cratesRegistry) ID() string    { return Crate }
func (c *cratesRegistry) Label() string { return "crates.io" }

type crateResponse struct {
	Crate struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		MaxVersion  string `json:"max_stable_version"`
		NewVersion  string `json:"newest_version"`
	} `json:"crate"`
	Versions []struct {
		Num    string `json:"num"`
		Yanked bool   `json:"yanked"`
	} `json:"versions"`
}

func (c *cratesRegistry) Lookup(ctx context.Context, name string) (*Info, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("crates.io: package name is required")
	}
	var doc crateResponse
	err := c.http.get(ctx, c.base+"/api/v1/crates/"+url.PathEscape(name), func(resp *http.Response) error {
		return json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&doc)
	})
	if err != nil {
		return nil, fmt.Errorf("crates.io: %w", err)
	}
	info := &Info{Name: doc.Crate.Name, Summary: doc.Crate.Description, Latest: doc.Crate.MaxVersion}
	if info.Latest == "" {
		info.Latest = doc.Crate.NewVersion
	}
	for _, v := range doc.Versions {
		if !v.Yanked {
			info.Versions = append(info.Versions, v.Num)
		}
	}
	return info, nil
}

func (c *cratesRegistry) Download(ctx context.Context, name, version, destDir string) (string, string, error) {
	if version == "" {
		info, err := c.Lookup(ctx, name)
		if err != nil {
			return "", "", err
		}
		version = info.Latest
	}
	if err := ensureDir(destDir); err != nil {
		return "", "", err
	}
	target := join(destDir, name+"-"+version+".tgz")
	remote := c.base + "/api/v1/crates/" + url.PathEscape(name) + "/" + url.PathEscape(version) + "/download"
	if err := c.http.download(ctx, remote, target, nil); err != nil {
		return "", "", fmt.Errorf("crates.io: download %s@%s: %w", name, version, err)
	}
	return target, version, nil
}
