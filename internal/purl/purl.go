// Package purl constructs and parses Package URLs (PURLs) for the PyPI
// ecosystem.
//
// A PURL is the join key used across the whole AISAI backend: advisory
// records (OSV, PyPI), CycloneDX/SPDX exports, dependency graph nodes and
// finding package references all key on the same string. The canonical form
// used here is:
//
//	pkg:pypi/<name>@<version>
//
// The canonical form is produced by model.PackageURL so that a PURL built
// here is byte-identical to one built by the rest of the backend.
package purl

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/tlmcguire/aisai/internal/model"
)

// prefix is the required prefix of every PURL handled by this package.
const prefix = "pkg:pypi/"

// New returns the canonical PURL for a PyPI package name and version.
//
// The name is lower-cased (PURLs are case-insensitive for PyPI) and the
// version is preserved verbatim because versions are not case-normalized by
// PyPI. An empty version yields a version-less PURL.
func New(name, version string) model.PURL {
	return model.PackageURL(strings.TrimSpace(name), strings.TrimSpace(version))
}

// Parse parses a PURL string into its canonical form.
//
// Only PyPI PURLs are accepted; anything else is an error. Qualifiers
// ("?...") and subpaths ("#...") are accepted syntactically but dropped, since
// the AISAI graph keys on name and version only.
func Parse(s string) (model.PURL, error) {
	raw := strings.TrimSpace(s)
	if raw == "" {
		return "", fmt.Errorf("purl: empty purl")
	}
	if len(raw) < len(prefix) || !strings.EqualFold(raw[:len(prefix)], prefix) {
		return "", fmt.Errorf("purl: %q is not a pypi purl (expected %q prefix)", s, prefix)
	}
	rest := raw[len(prefix):]
	// Drop qualifiers and subpath: they carry no identity for the graph.
	if i := strings.IndexAny(rest, "?#"); i >= 0 {
		rest = rest[:i]
	}
	name, version := rest, ""
	if i := strings.LastIndex(rest, "@"); i >= 0 {
		name, version = rest[:i], rest[i+1:]
	}
	decodedName, err := url.PathUnescape(name)
	if err != nil {
		return "", fmt.Errorf("purl: %q has an invalid escaped name: %w", s, err)
	}
	decodedVersion, err := url.PathUnescape(version)
	if err != nil {
		return "", fmt.Errorf("purl: %q has an invalid escaped version: %w", s, err)
	}
	if decodedName == "" {
		return "", fmt.Errorf("purl: %q has an empty package name", s)
	}
	if strings.ContainsAny(decodedName, "/") {
		return "", fmt.Errorf("purl: %q has an invalid package name %q", s, decodedName)
	}
	if strings.Contains(decodedVersion, "/") {
		return "", fmt.Errorf("purl: %q has an invalid version %q", s, decodedVersion)
	}
	return New(decodedName, decodedVersion), nil
}

// NormalizeName normalizes a Python package name according to PEP 503.
//
// The name is lower-cased and every run of "-", "_" or "." is replaced with a
// single "-". This is how PyPI itself treats project names, so "Flask",
// "flask" and "FLASK" all resolve to the same project.
func NormalizeName(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	prevSep := false
	for _, r := range name {
		switch r {
		case '-', '_', '.':
			if !prevSep {
				b.WriteByte('-')
				prevSep = true
			}
		default:
			b.WriteRune(r)
			prevSep = false
		}
	}
	return strings.ToLower(b.String())
}
