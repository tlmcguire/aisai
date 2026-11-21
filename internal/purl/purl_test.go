package purl

import (
	"testing"

	"github.com/tlmcguire/aisai/internal/model"
)

// TestNew checks the canonical PURL construction.
func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		version string
		want    model.PURL
	}{
		{name: "flask", version: "2.0.0", want: "pkg:pypi/flask@2.0.0"},
		{name: "Flask", version: "2.0.0", want: "pkg:pypi/flask@2.0.0"},
		{name: "zope.interface", version: "5.4.0", want: "pkg:pypi/zope.interface@5.4.0"},
		{name: "flask", version: "", want: "pkg:pypi/flask"},
	}
	for _, tc := range tests {
		if got := New(tc.name, tc.version); got != tc.want {
			t.Errorf("New(%q, %q) = %q, want %q", tc.name, tc.version, got, tc.want)
		}
	}
}

// TestParseRoundTrip checks that a PURL parses back into its parts.
func TestParseRoundTrip(t *testing.T) {
	tests := []struct {
		input   string
		name    string
		version string
	}{
		{input: "pkg:pypi/flask@2.0.0", name: "flask", version: "2.0.0"},
		{input: "pkg:pypi/Flask@2.0.0", name: "flask", version: "2.0.0"},
		{input: "pkg:pypi/zope.interface@5.4.0", name: "zope.interface", version: "5.4.0"},
		{input: "pkg:pypi/flask", name: "flask", version: ""},
		{input: "pkg:pypi/flask@2.0.0?extension=tar.gz", name: "flask", version: "2.0.0"},
	}
	for _, tc := range tests {
		got, err := Parse(tc.input)
		if err != nil {
			t.Errorf("Parse(%q): %v", tc.input, err)
			continue
		}
		if got.Name() != tc.name {
			t.Errorf("Parse(%q).Name() = %q, want %q", tc.input, got.Name(), tc.name)
		}
		if got.Version() != tc.version {
			t.Errorf("Parse(%q).Version() = %q, want %q", tc.input, got.Version(), tc.version)
		}
	}
}

// TestParseRejectsForeignPURLs checks that only PyPI PURLs are accepted.
func TestParseRejectsForeignPURLs(t *testing.T) {
	for _, input := range []string{"", "   ", "flask@2.0.0", "pkg:npm/express@1.0", "pkg:pypi/", "pkg:pypi/@1.0"} {
		if _, err := Parse(input); err == nil {
			t.Errorf("Parse(%q) did not fail", input)
		}
	}
}

// TestNormalizeName checks the PEP 503 normalization.
func TestNormalizeName(t *testing.T) {
	tests := map[string]string{
		"Flask":            "flask",
		"flask":            "flask",
		"zope.interface":   "zope-interface",
		"a__b":             "a-b",
		"a--b":             "a-b",
		"a..b":             "a-b",
		"a-_.-b":           "a-b",
		"Flask-SQLAlchemy": "flask-sqlalchemy",
		"":                 "",
	}
	for input, want := range tests {
		if got := NormalizeName(input); got != want {
			t.Errorf("NormalizeName(%q) = %q, want %q", input, got, want)
		}
	}
}
