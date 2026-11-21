package sbom

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tlmcguire/aisai/internal/model"
)

func testPackages() []model.Package {
	return []model.Package{
		{
			PURL:      "pkg:pypi/flask@2.0.0",
			Name:      "flask",
			Version:   "2.0.0",
			Ecosystem: "PyPI",
			Direct:    true,
		},
		{
			PURL:      "pkg:pypi/jinja2@3.1.6",
			Name:      "jinja2",
			Version:   "3.1.6",
			Ecosystem: "PyPI",
		},
	}
}

func testAdvisories() []model.Advisory {
	return []model.Advisory{
		{
			ID:       "CVE-2023-30861",
			Source:   "osv",
			Summary:  "Flask session cookie disclosure",
			Severity: model.SeverityHigh,
			CWE:      []string{"CWE-200"},
			URL:      "https://osv.dev/CVE-2023-30861",
			Package:  "pkg:pypi/flask@2.0.0",
		},
	}
}

func testFindings() []model.Finding {
	return []model.Finding{
		{
			ID:        "f1",
			Rule:      "subprocess-shell-true",
			CWE:       "CWE-78",
			Severity:  model.SeverityHigh,
			File:      "pkgs/flask-2.0.0/src/flask/cli.py",
			StartLine: 887,
			Message:   "subprocess call with shell=True",
			Package:   "pkg:pypi/flask@2.0.0",
		},
	}
}

func TestWriteCycloneDX(t *testing.T) {
	opts := Options{
		ToolVersion:    "0.1.0",
		AuditID:        "a1",
		PackageName:    "flask",
		PackageVersion: "2.0.0",
		Timestamp:      time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC),
	}
	data, err := WriteCycloneDX(testPackages(), testAdvisories(), testFindings(), opts)
	if err != nil {
		t.Fatalf("WriteCycloneDX: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("the document is not valid JSON: %v", err)
	}
	if doc["bomFormat"] != "CycloneDX" {
		t.Errorf("bomFormat = %v, want CycloneDX", doc["bomFormat"])
	}
	if doc["specVersion"] != "1.5" {
		t.Errorf("specVersion = %v, want 1.5", doc["specVersion"])
	}

	components, _ := doc["components"].([]any)
	if len(components) != 2 {
		t.Fatalf("got %d components, want 2", len(components))
	}
	first, _ := components[0].(map[string]any)
	if first["bom-ref"] != "pkg:pypi/flask@2.0.0" {
		t.Errorf("the first component's bom-ref = %v, want the flask PURL", first["bom-ref"])
	}
	if first["type"] != "library" {
		t.Errorf("component type = %v, want library", first["type"])
	}

	vulns, _ := doc["vulnerabilities"].([]any)
	if len(vulns) != 1 {
		t.Fatalf("got %d vulnerabilities, want 1", len(vulns))
	}
	v, _ := vulns[0].(map[string]any)
	if v["id"] != "CVE-2023-30861" {
		t.Errorf("vulnerability id = %v, want CVE-2023-30861", v["id"])
	}
	cwes, _ := v["cwes"].([]any)
	if len(cwes) != 1 || cwes[0].(float64) != 200 {
		t.Errorf("vulnerability cwes = %v, want [200]", cwes)
	}

	// The finding must be carried as a property.
	props, _ := first["properties"].([]any)
	if len(props) != 1 {
		t.Fatalf("got %d properties on the flask component, want 1", len(props))
	}
	p, _ := props[0].(map[string]any)
	if !strings.Contains(p["value"].(string), "cli.py:887") {
		t.Errorf("the finding property does not carry the location: %v", p["value"])
	}
}

func TestWriteCycloneDXWithNothing(t *testing.T) {
	data, err := WriteCycloneDX(nil, nil, nil, Options{})
	if err != nil {
		t.Fatalf("WriteCycloneDX: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("the document is not valid JSON: %v", err)
	}
	if doc["components"] == nil {
		t.Error("components is null; it must be an empty array so the schema holds")
	}
	if doc["vulnerabilities"] == nil {
		t.Error("vulnerabilities is null; it must be an empty array so the schema holds")
	}
}

func TestWriteSPDX(t *testing.T) {
	opts := Options{
		ToolVersion:    "0.1.0",
		AuditID:        "a1",
		PackageName:    "flask",
		PackageVersion: "2.0.0",
		Timestamp:      time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC),
	}
	data, err := WriteSPDX(testPackages(), testAdvisories(), testFindings(), opts)
	if err != nil {
		t.Fatalf("WriteSPDX: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("the document is not valid JSON: %v", err)
	}
	if doc["spdxVersion"] != "SPDX-2.3" {
		t.Errorf("spdxVersion = %v, want SPDX-2.3", doc["spdxVersion"])
	}
	if doc["dataLicense"] != "CC0-1.0" {
		t.Errorf("dataLicense = %v, want CC0-1.0", doc["dataLicense"])
	}

	pkgs, _ := doc["packages"].([]any)
	if len(pkgs) != 2 {
		t.Fatalf("got %d packages, want 2", len(pkgs))
	}
	flask, _ := pkgs[0].(map[string]any)
	if flask["name"] != "flask" {
		t.Errorf("the first package is %v, want flask", flask["name"])
	}
	refs, _ := flask["externalRefs"].([]any)
	if len(refs) != 1 {
		t.Fatalf("got %d externalRefs, want 1", len(refs))
	}
	ref, _ := refs[0].(map[string]any)
	if ref["referenceLocator"] != "pkg:pypi/flask@2.0.0" {
		t.Errorf("the purl reference = %v, want the flask PURL", ref["referenceLocator"])
	}
	// SPDX has no vulnerability vocabulary, so the advisory is a comment.
	if !strings.Contains(flask["comment"].(string), "CVE-2023-30861") {
		t.Errorf("the comment does not mention the advisory: %v", flask["comment"])
	}

	describes, _ := doc["documentDescribes"].([]any)
	if len(describes) != 1 {
		t.Errorf("documentDescribes = %v, want one entry", describes)
	}
}

func TestCWENumber(t *testing.T) {
	cases := map[string]struct {
		want int
		ok   bool
	}{
		"CWE-78":    {78, true},
		"cwe-200":   {200, true},
		"CWE-89":    {89, true},
		"78":        {78, true},
		"":          {0, false},
		"CWE-":      {0, false},
		"CWE-abc":   {0, false},
		"not a cwe": {0, false},
	}
	for in, want := range cases {
		got, ok := cweNumber(in)
		if got != want.want || ok != want.ok {
			t.Errorf("cweNumber(%q) = (%d, %v), want (%d, %v)", in, got, ok, want.want, want.ok)
		}
	}
}

func TestSPDXIDIsStableAndSafe(t *testing.T) {
	a := spdxID("pkg:pypi/flask@2.0.0")
	b := spdxID("pkg:pypi/flask@2.0.0")
	if a != b {
		t.Errorf("spdxID is not stable: %q vs %q", a, b)
	}
	if !strings.HasPrefix(a, "SPDXRef-Package-") {
		t.Errorf("spdxID = %q, want the SPDXRef-Package- prefix", a)
	}
	if strings.ContainsAny(a, "/:@ ") {
		t.Errorf("spdxID %q contains characters that are not allowed in an SPDX identifier", a)
	}
}
