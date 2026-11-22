// Package link understands the links people paste to say what to audit: a
// page of a package index, a package URL (pkg:npm/express@4.19.0), or a
// repository.
package link

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/tlmcguire/aisai/internal/gitsrc"
	"github.com/tlmcguire/aisai/internal/registry"
)

// Kind says what a link points at.
type Kind string

const (
	// KindPackage is a package on a package index.
	KindPackage Kind = "package"
	// KindRepo is a source repository to clone.
	KindRepo Kind = "repo"
)

// Link is the parsed meaning of a pasted link.
type Link struct {
	Kind Kind `json:"kind"`
	// Ecosystem, Name and Version describe a package; Version may be empty.
	Ecosystem string `json:"ecosystem,omitempty"`
	Name      string `json:"name,omitempty"`
	Version   string `json:"version,omitempty"`
	// Repo describes a repository.
	RepoURL string `json:"repoUrl,omitempty"`
	Display string `json:"display,omitempty"`
	Ref     string `json:"ref,omitempty"`
}

// ErrUnrecognised is returned for text that names neither a package nor a
// repository.
var ErrUnrecognised = errors.New("that link is not a package page or a repository")

// Looks reports whether text is a link rather than a bare package name.
func Looks(text string) bool {
	t := strings.TrimSpace(text)
	return strings.Contains(t, "://") || strings.HasPrefix(t, "pkg:") || strings.HasPrefix(t, "git@")
}

// Parse works out what a pasted link refers to.
func Parse(raw string) (*Link, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, ErrUnrecognised
	}
	if strings.HasPrefix(raw, "pkg:") {
		return parsePURL(raw)
	}
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
		path := strings.Split(strings.Trim(u.Path, "/"), "/")
		switch host {
		case "pypi.org":
			// /project/<name>/[<version>/]
			if len(path) >= 2 && path[0] == "project" {
				l := &Link{Kind: KindPackage, Ecosystem: registry.PyPI, Name: path[1]}
				if len(path) >= 3 && path[2] != "" {
					l.Version = path[2]
				}
				return l, nil
			}
		case "npmjs.com", "npmjs.org":
			// /package/<name> or /package/@scope/<name>, then /v/<version>
			if len(path) >= 2 && path[0] == "package" {
				name, rest := path[1], path[2:]
				if strings.HasPrefix(name, "@") && len(rest) > 0 {
					name, rest = name+"/"+rest[0], rest[1:]
				}
				l := &Link{Kind: KindPackage, Ecosystem: registry.NPM, Name: name}
				if len(rest) >= 2 && rest[0] == "v" {
					l.Version = rest[1]
				}
				return l, nil
			}
		case "crates.io":
			// /crates/<name>[/<version>]
			if len(path) >= 2 && path[0] == "crates" {
				l := &Link{Kind: KindPackage, Ecosystem: registry.Crate, Name: path[1]}
				if len(path) >= 3 && path[2] != "" {
					l.Version = path[2]
				}
				return l, nil
			}
		}
	}
	// A page of a package index that is not a package page (a user, a search)
	// is not a repository either.
	if u, err := url.Parse(raw); err == nil {
		switch strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.") {
		case "pypi.org", "npmjs.com", "npmjs.org", "crates.io":
			return nil, fmt.Errorf("%w (open the package's own page and copy that link)", ErrUnrecognised)
		}
	}
	repo, err := gitsrc.ParseRepo(raw)
	if err != nil {
		return nil, fmt.Errorf("%w (%v)", ErrUnrecognised, err)
	}
	return &Link{Kind: KindRepo, RepoURL: repo.CloneURL, Name: repo.Name, Display: repo.Display(), Ref: repo.Ref}, nil
}

func parsePURL(raw string) (*Link, error) {
	s := strings.TrimPrefix(raw, "pkg:")
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	typ, rest, ok := strings.Cut(s, "/")
	if !ok || rest == "" {
		return nil, ErrUnrecognised
	}
	var eco string
	switch strings.ToLower(typ) {
	case "pypi":
		eco = registry.PyPI
	case "npm":
		eco = registry.NPM
	case "cargo":
		eco = registry.Crate
	default:
		return nil, fmt.Errorf("%w (package type %q is not supported)", ErrUnrecognised, typ)
	}
	name, version := rest, ""
	slash := strings.LastIndex(rest, "/")
	if at := strings.Index(rest[slash+1:], "@"); at >= 0 {
		name, version = rest[:slash+1+at], rest[slash+2+at:]
	}
	if dec, err := url.PathUnescape(name); err == nil {
		name = dec
	}
	return &Link{Kind: KindPackage, Ecosystem: eco, Name: name, Version: version}, nil
}
