package registry

import (
	"context"

	"github.com/tlmcguire/aisai/internal/pypi"
)

type pypiRegistry struct{ c *pypi.Client }

// NewPyPI adapts a PyPI client to the Registry interface.
func NewPyPI(c *pypi.Client) Registry { return &pypiRegistry{c: c} }

func (p *pypiRegistry) ID() string    { return PyPI }
func (p *pypiRegistry) Label() string { return "PyPI" }

func (p *pypiRegistry) Lookup(ctx context.Context, name string) (*Info, error) {
	info, err := p.c.Package(ctx, name, "")
	if err != nil {
		return nil, err
	}
	out := &Info{Name: info.Name, Summary: info.Summary, Latest: info.Version}
	if versions, err := p.c.Releases(ctx, info.Name); err == nil {
		out.Versions = versions
	}
	return out, nil
}

func (p *pypiRegistry) Download(ctx context.Context, name, version, destDir string) (string, string, error) {
	info, err := p.c.Package(ctx, name, version)
	if err != nil {
		return "", "", err
	}
	path, err := p.c.DownloadSdist(ctx, name, info.Version, destDir)
	return path, info.Version, err
}
