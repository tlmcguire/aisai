package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRejectsAnUnknownFormat(t *testing.T) {
	err := run("jsonld", "", "", false, "", true, "1.0.0", "abc")
	if err == nil {
		t.Fatal("an unknown format must be rejected")
	}
	if !strings.Contains(err.Error(), "unknown --format") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunRejectsBothWithoutOutDir(t *testing.T) {
	err := run("cyclonedx", "", "", true, "", true, "1.0.0", "abc")
	if err == nil {
		t.Fatal("--both without --out-dir must be rejected")
	}
	if !strings.Contains(err.Error(), "--out-dir") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunRejectsBothWithOut(t *testing.T) {
	err := run("cyclonedx", "x.json", "", true, "", true, "1.0.0", "abc")
	if err == nil {
		t.Fatal("--both with --out must be rejected")
	}
}

func TestRunWritesBothDocuments(t *testing.T) {
	dir := t.TempDir()
	if err := run("", "", dir, true, "", true, "1.0.0", "abc123"); err != nil {
		t.Fatalf("run: %v", err)
	}

	for _, name := range []string{"aisai.cyclonedx.json", "aisai.spdx.json"} {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		var doc map[string]any
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatalf("%s is not valid JSON: %v", name, err)
		}
		if name == "aisai.cyclonedx.json" {
			if doc["bomFormat"] != "CycloneDX" || doc["specVersion"] != "1.5" {
				t.Fatalf("unexpected CycloneDX header: %v", doc)
			}
		} else {
			if doc["spdxVersion"] != "SPDX-2.3" {
				t.Fatalf("unexpected SPDX header: %v", doc)
			}
		}
	}
}

func TestCollectModulesUsesTheBuildInfoOfTheBinary(t *testing.T) {
	mods, mainModule, err := collectModules("", true)
	if err != nil {
		t.Fatalf("collectModules: %v", err)
	}
	if mainModule == "" {
		t.Fatal("the main module must be reported")
	}
	// The test binary links the standard library only, so the list may be
	// empty; what matters is that the main module is never a component.
	for _, m := range mods {
		if m.Path == mainModule {
			t.Fatal("the main module must not be a component of its own document")
		}
	}
}

func TestCollectModulesReadsAnotherBinary(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Skipf("the test binary path is unknown: %v", err)
	}
	mods, mainModule, err := collectModules(self, true)
	if err != nil {
		t.Fatalf("collectModules: %v", err)
	}
	if mainModule == "" {
		t.Fatal("the main module must be reported")
	}
	for _, m := range mods {
		if m.Path == mainModule {
			t.Fatal("the main module must not be a component of its own document")
		}
	}
}

func TestCollectModulesRejectsAMissingBinary(t *testing.T) {
	if _, _, err := collectModules(filepath.Join(t.TempDir(), "nope"), true); err == nil {
		t.Fatal("a missing binary must be an error")
	}
}
