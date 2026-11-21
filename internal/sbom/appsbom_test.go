package sbom

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func testModules() []GoModule {
	return []GoModule{
		{Path: "github.com/tree-sitter/go-tree-sitter", Version: "v0.25.0", Sum: "h1:aaa", Direct: true},
		{Path: "github.com/tree-sitter/tree-sitter-python", Version: "v0.25.0", Sum: "h1:bbb", Direct: true},
		{Path: "golang.org/x/sys", Version: "v0.30.0", Sum: "h1:ccc"},
		{
			Path:    "github.com/old/module",
			Version: "v1.0.0",
			Replace: &GoModule{Path: "github.com/new/module", Version: "v1.2.0", Sum: "h1:ddd"},
		},
	}
}

func testOptions() AppOptions {
	return AppOptions{
		ToolName:    "aisai-sbom",
		ToolVersion: "1.2.3",
		Timestamp:   time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC),
		AppName:     DefaultAppName,
		AppVersion:  "0.1.0",
		AppCommit:   "abc123",
	}
}

func TestWriteAppCycloneDX(t *testing.T) {
	data, err := WriteAppCycloneDX(testModules(), testOptions())
	if err != nil {
		t.Fatalf("WriteAppCycloneDX: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("the document is not valid JSON: %v", err)
	}
	if doc["bomFormat"] != "CycloneDX" {
		t.Fatalf("bomFormat = %v, want CycloneDX", doc["bomFormat"])
	}
	if doc["specVersion"] != "1.5" {
		t.Fatalf("specVersion = %v, want 1.5", doc["specVersion"])
	}

	metadata, _ := doc["metadata"].(map[string]any)
	subject, _ := metadata["component"].(map[string]any)
	if subject == nil {
		t.Fatal("the metadata has no component")
	}
	if subject["type"] != "application" {
		t.Fatalf("the subject type = %v, want application", subject["type"])
	}
	if subject["purl"] != "pkg:golang/github.com/tlmcguire/aisai@0.1.0" {
		t.Fatalf("the subject purl = %v", subject["purl"])
	}

	components, _ := doc["components"].([]any)
	if len(components) != len(testModules()) {
		t.Fatalf("the document has %d components, want %d", len(components), len(testModules()))
	}

	byName := map[string]map[string]any{}
	for _, raw := range components {
		c, _ := raw.(map[string]any)
		name, _ := c["name"].(string)
		byName[name] = c
	}

	treeSitter, ok := byName["github.com/tree-sitter/go-tree-sitter"]
	if !ok {
		t.Fatal("the tree-sitter module is missing")
	}
	if treeSitter["purl"] != "pkg:golang/github.com/tree-sitter/go-tree-sitter@v0.25.0" {
		t.Fatalf("the tree-sitter purl = %v", treeSitter["purl"])
	}
	if treeSitter["scope"] != "required" {
		t.Fatalf("a direct requirement must have the required scope, got %v", treeSitter["scope"])
	}
	if treeSitter["version"] != "v0.25.0" {
		t.Fatalf("the tree-sitter version = %v", treeSitter["version"])
	}

	// The replacement is recorded under the replacement's identity.
	replaced, ok := byName["github.com/new/module"]
	if !ok {
		t.Fatalf("the replaced module is missing, got %v", byName)
	}
	props, _ := replaced["properties"].([]any)
	found := false
	for _, raw := range props {
		p, _ := raw.(map[string]any)
		if p["name"] == "aisai:go:replaced" && p["value"] == "github.com/old/module@v1.0.0" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the replacement is not recorded in the properties: %v", props)
	}

	// The application depends on its direct requirements.
	deps, _ := doc["dependencies"].([]any)
	if len(deps) == 0 {
		t.Fatal("the document has no dependency graph")
	}
	first, _ := deps[0].(map[string]any)
	if first["ref"] != "pkg:golang/github.com/tlmcguire/aisai@0.1.0" {
		t.Fatalf("the first dependency ref = %v", first["ref"])
	}
	dependsOn, _ := first["dependsOn"].([]any)
	if len(dependsOn) != 2 {
		t.Fatalf("the application depends on %d modules, want the 2 direct ones: %v", len(dependsOn), dependsOn)
	}
}

func TestWriteAppSPDX(t *testing.T) {
	data, err := WriteAppSPDX(testModules(), testOptions())
	if err != nil {
		t.Fatalf("WriteAppSPDX: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("the document is not valid JSON: %v", err)
	}
	if doc["spdxVersion"] != "SPDX-2.3" {
		t.Fatalf("spdxVersion = %v, want SPDX-2.3", doc["spdxVersion"])
	}
	if doc["dataLicense"] != "CC0-1.0" {
		t.Fatalf("dataLicense = %v, want CC0-1.0", doc["dataLicense"])
	}

	packages, _ := doc["packages"].([]any)
	// The application itself plus the four modules.
	if len(packages) != len(testModules())+1 {
		t.Fatalf("the document has %d packages, want %d", len(packages), len(testModules())+1)
	}

	rels, _ := doc["relationships"].([]any)
	dependencyOf := 0
	describes := 0
	for _, raw := range rels {
		r, _ := raw.(map[string]any)
		switch r["relationshipType"] {
		case "DEPENDENCY_OF":
			dependencyOf++
		case "DESCRIBES":
			describes++
		}
	}
	if dependencyOf != len(testModules()) {
		t.Fatalf("%d DEPENDENCY_OF relationships, want %d", dependencyOf, len(testModules()))
	}
	if describes != 1 {
		t.Fatalf("%d DESCRIBES relationships, want 1", describes)
	}

	// The purl of a module is its external reference.
	body := string(data)
	if !strings.Contains(body, "pkg:golang/golang.org/x/sys@v0.30.0") {
		t.Fatal("the x/sys module purl is missing")
	}
}

func TestWriteAppSPDXMarksIndirect(t *testing.T) {
	data, err := WriteAppSPDX([]GoModule{{Path: "golang.org/x/sys", Version: "v0.30.0"}}, testOptions())
	if err != nil {
		t.Fatalf("WriteAppSPDX: %v", err)
	}
	if !strings.Contains(string(data), "indirect dependency") {
		t.Fatal("an indirect module must be marked as such")
	}
}

func TestGoPURL(t *testing.T) {
	if got := goPURL("golang.org/x/sys", "v0.30.0"); got != "pkg:golang/golang.org/x/sys@v0.30.0" {
		t.Fatalf("goPURL = %q", got)
	}
	if got := goPURL("golang.org/x/sys", ""); got != "pkg:golang/golang.org/x/sys" {
		t.Fatalf("goPURL without a version = %q", got)
	}
}

func TestSerialNumberIsAUuid(t *testing.T) {
	s := serialNumber()
	if !strings.HasPrefix(s, "urn:uuid:") {
		t.Fatalf("serialNumber = %q", s)
	}
	if s == serialNumber() {
		t.Fatal("two serial numbers must not be equal")
	}
}
