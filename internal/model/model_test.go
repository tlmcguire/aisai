package model

import "testing"

func TestPackageURL(t *testing.T) {
	tests := []struct {
		name    string
		version string
		want    string
	}{
		{"flask", "2.0.0", "pkg:pypi/flask@2.0.0"},
		{"Flask", "2.0.0", "pkg:pypi/flask@2.0.0"},
		{"zope.interface", "5.4.0", "pkg:pypi/zope.interface@5.4.0"},
		{"requests", "", "pkg:pypi/requests"},
	}
	for _, tc := range tests {
		got := string(PackageURL(tc.name, tc.version))
		if got != tc.want {
			t.Errorf("PackageURL(%q, %q) = %q, want %q", tc.name, tc.version, got, tc.want)
		}
	}
}

func TestPURLParts(t *testing.T) {
	p := PackageURL("Flask", "2.0.0")
	if got := p.Name(); got != "flask" {
		t.Errorf("Name() = %q, want %q", got, "flask")
	}
	if got := p.Version(); got != "2.0.0" {
		t.Errorf("Version() = %q, want %q", got, "2.0.0")
	}

	noVersion := PackageURL("requests", "")
	if got := noVersion.Version(); got != "" {
		t.Errorf("Version() on versionless = %q, want empty", got)
	}
	if got := noVersion.Name(); got != "requests" {
		t.Errorf("Name() on versionless = %q, want %q", got, "requests")
	}
}

func TestSeverityRanking(t *testing.T) {
	if SeverityCritical.Rank() <= SeverityHigh.Rank() {
		t.Error("critical must outrank high")
	}
	if SeverityHigh.Rank() <= SeverityMedium.Rank() {
		t.Error("high must outrank medium")
	}
	if !SeverityHigh.AtLeast(SeverityMedium) {
		t.Error("high should be at least medium")
	}
	if SeverityMedium.AtLeast(SeverityHigh) {
		t.Error("medium should not be at least high")
	}
	if got := MaxSeverity(SeverityLow, SeverityCritical); got != SeverityCritical {
		t.Errorf("MaxSeverity = %q, want critical", got)
	}
	if got := MaxSeverity(SeverityMedium, SeverityHigh); got != SeverityHigh {
		t.Errorf("MaxSeverity = %q, want high", got)
	}
	if !SeverityInfo.Valid() || Severity("bogus").Valid() {
		t.Error("severity validity check failed")
	}
}

func TestFindingIDIsStableAndDistinct(t *testing.T) {
	a := FindingID("pkg/mod.py", 42, "CWE-798", "hardcoded-secret")
	b := FindingID("pkg/mod.py", 42, "CWE-798", "hardcoded-secret")
	if a != b {
		t.Errorf("FindingID is not stable: %q vs %q", a, b)
	}

	// Different line must produce a different id.
	c := FindingID("pkg/mod.py", 43, "CWE-798", "hardcoded-secret")
	if a == c {
		t.Error("FindingID collided across lines")
	}

	// Different file must produce a different id.
	d := FindingID("pkg/other.py", 42, "CWE-798", "hardcoded-secret")
	if a == d {
		t.Error("FindingID collided across files")
	}

	// Path normalization must not change the id.
	e := FindingID("./pkg/mod.py", 42, "CWE-798", "hardcoded-secret")
	if a != e {
		t.Errorf("FindingID changed after path normalization: %q vs %q", a, e)
	}
}

func TestRelPath(t *testing.T) {
	tests := map[string]string{
		"pkg/mod.py":      "pkg/mod.py",
		"./pkg/mod.py":    "pkg/mod.py",
		"pkg\\mod.py":     "pkg/mod.py",
		"/abs/pkg/mod.py": "abs/pkg/mod.py",
	}
	for in, want := range tests {
		if got := RelPath(in); got != want {
			t.Errorf("RelPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestScopedNPMPURL(t *testing.T) {
	p := PackageURLFor("npm", "@Scope/Pkg", "1.2.3")
	if p.String() != "pkg:npm/@scope/pkg@1.2.3" || p.Name() != "@scope/pkg" || p.Version() != "1.2.3" || p.Ecosystem() != "npm" {
		t.Fatalf("unexpected: %q %q %q %q", p, p.Name(), p.Version(), p.Ecosystem())
	}
	if q := PackageURLFor("npm", "@scope/pkg", ""); q.Name() != "@scope/pkg" || q.Version() != "" {
		t.Fatalf("versionless scoped purl parsed wrong: %q %q", q.Name(), q.Version())
	}
}
