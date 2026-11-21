package pypi

import (
	"testing"
)

// TestCompareVersions checks the PEP 440 ordering.
func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a    string
		b    string
		want int
	}{
		{"1.0", "1.0", 0},
		{"1.0", "1.0.0", 0},
		{"1.0", "1.1", -1},
		{"1.10", "1.9", 1},
		{"2.0", "10.0", -1},
		{"1.0a1", "1.0", -1},
		{"1.0a1", "1.0b1", -1},
		{"1.0rc1", "1.0b1", 1},
		{"1.0rc1", "1.0", -1},
		{"1.0.post1", "1.0", 1},
		{"1.0.dev1", "1.0", -1},
		{"1.0.dev1", "1.0.dev2", -1},
		{"1.0.dev1", "1.0a1", 1},
		{"1!1.0", "2.0", 1},
		{"1.0+local", "1.0", 1},
		{"1.0+local", "1.0+other", -1},
		{"v1.2.3", "1.2.3", 0},
		{"1.0-1", "1.0", 1},
	}
	for _, tc := range tests {
		got := CompareVersions(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
		if got := CompareVersions(tc.b, tc.a); got != -tc.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tc.b, tc.a, got, -tc.want)
		}
	}
}

// TestIsPrerelease checks the pre-release detection.
func TestIsPrerelease(t *testing.T) {
	tests := map[string]bool{
		"1.0":      false,
		"1.0a1":    true,
		"1.0b1":    true,
		"1.0rc1":   true,
		"1.0.dev1": true,
		"2.0.0":    false,
	}
	for version, want := range tests {
		if got := IsPrerelease(version); got != want {
			t.Errorf("IsPrerelease(%q) = %v, want %v", version, got, want)
		}
	}
}

// TestMatches checks the specifier evaluation.
func TestMatches(t *testing.T) {
	tests := []struct {
		version string
		spec    string
		want    bool
	}{
		{"1.0", "", true},
		{"1.0", ">=1.0", true},
		{"1.0", ">1.0", false},
		{"2.0", ">=1.0,<3.0", true},
		{"3.0", ">=1.0,<3.0", false},
		{"1.4.2", "~=1.4.2", true},
		{"1.5.0", "~=1.4.2", false},
		{"1.4.9", "~=1.4.2", true},
		{"2.0", "==1.0.*", false},
		{"1.9", "==1.0.*", false},
		{"1.0.5", "==1.0.*", true},
		{"1.0", "!=2.0", true},
		{"1.0", "===1.0", true},
	}
	for _, tc := range tests {
		if got := Matches(tc.version, tc.spec); got != tc.want {
			t.Errorf("Matches(%q, %q) = %v, want %v", tc.version, tc.spec, got, tc.want)
		}
	}
}

// TestLatestVersion checks the release selection.
func TestLatestVersion(t *testing.T) {
	versions := []string{"1.0", "1.10", "2.0.0b1", "2.0.0", "0.9"}
	if got := LatestVersion(versions, ""); got != "2.0.0" {
		t.Errorf("LatestVersion = %q, want 2.0.0", got)
	}
	if got := LatestVersion(versions, "<2.0.0"); got != "1.10" {
		t.Errorf("LatestVersion = %q, want 1.10", got)
	}
	if got := LatestVersion(versions, ">=3.0"); got != "" {
		t.Errorf("LatestVersion = %q, want an empty string", got)
	}
	if got := LatestVersion([]string{"1.0rc1"}, ""); got != "1.0rc1" {
		t.Errorf("LatestVersion = %q, want 1.0rc1", got)
	}
}

// TestParseRequirement checks the PEP 508 parser.
func TestParseRequirement(t *testing.T) {
	tests := []struct {
		input   string
		name    string
		extras  []string
		spec    string
		marker  string
		wantErr bool
	}{
		{input: "requests", name: "requests"},
		{input: "requests (>=2.0)", name: "requests", spec: ">=2.0"},
		{input: "requests>=2.0", name: "requests", spec: ">=2.0"},
		{input: "requests[security]>=2.0", name: "requests", extras: []string{"security"}, spec: ">=2.0"},
		{input: "zope.interface>=5.0", name: "zope.interface", spec: ">=5.0"},
		{input: `six ; python_version < "3.9"`, name: "six", marker: `python_version < "3.9"`},
		{input: `requests ; extra == "socks"`, name: "requests", marker: `extra == "socks"`},
		{input: "", wantErr: true},
		{input: "  ", wantErr: true},
	}
	for _, tc := range tests {
		req, err := ParseRequirement(tc.input)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseRequirement(%q) did not fail", tc.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseRequirement(%q): %v", tc.input, err)
			continue
		}
		if req.Name != tc.name {
			t.Errorf("ParseRequirement(%q).Name = %q, want %q", tc.input, req.Name, tc.name)
		}
		if len(req.Extras) != len(tc.extras) {
			t.Errorf("ParseRequirement(%q).Extras = %v, want %v", tc.input, req.Extras, tc.extras)
		}
		if req.Specifier != tc.spec {
			t.Errorf("ParseRequirement(%q).Specifier = %q, want %q", tc.input, req.Specifier, tc.spec)
		}
		if req.Marker != tc.marker {
			t.Errorf("ParseRequirement(%q).Marker = %q, want %q", tc.input, req.Marker, tc.marker)
		}
	}
}

// TestEvaluateMarker checks the environment marker evaluation.
func TestEvaluateMarker(t *testing.T) {
	env := MarkerEnv{
		PythonVersion:         "3.13",
		PythonFullVersion:     "3.13.1",
		OSName:                "posix",
		SysPlatform:           "linux",
		PlatformMachine:       "x86_64",
		PlatformSystem:        "Linux",
		Implementation:        "cpython",
		ImplementationVersion: "3.13.1",
	}
	tests := []struct {
		marker string
		want   bool
	}{
		{marker: "", want: true},
		{marker: `python_version >= "3.9"`, want: true},
		{marker: `python_version < "3.9"`, want: false},
		{marker: `python_version >= "3.9" and sys_platform == "linux"`, want: true},
		{marker: `python_version >= "3.9" and sys_platform == "win32"`, want: false},
		{marker: `sys_platform == "win32" or os_name == "posix"`, want: true},
		{marker: `not python_version < "3.9"`, want: true},
		{marker: `(python_version >= "3.9" and sys_platform == "linux") or extra == "x"`, want: true},
		{marker: `extra == "socks"`, want: false},
		{marker: `platform_system == "Linux"`, want: true},
	}
	for _, tc := range tests {
		got, err := EvaluateMarker(tc.marker, env)
		if err != nil {
			t.Errorf("EvaluateMarker(%q): %v", tc.marker, err)
			continue
		}
		if got != tc.want {
			t.Errorf("EvaluateMarker(%q) = %v, want %v", tc.marker, got, tc.want)
		}
	}
}

// TestEvaluateMarkerInvalid checks that a broken marker is reported.
func TestEvaluateMarkerInvalid(t *testing.T) {
	if _, err := EvaluateMarker(`python_version >= `, MarkerEnv{}); err == nil {
		t.Fatalf("expected an error for a broken marker")
	}
}
