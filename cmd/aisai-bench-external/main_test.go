package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This file covers the CLI of the external benchmark harness.
//
// The harness downloads two corpora and runs the engines over them, so most
// of its behaviour can only be exercised with the network. The parts that
// can be tested offline are the ones that decide whether a measurement is
// interpretable: the date handling of the leakage metadata, the report
// shape, and the refusal to run without a corpus.

func TestLeakageMetadataParsesTheCutoffDates(t *testing.T) {
	m, err := leakageMetadata("2023-06-30", "2022-01-01", "qwen2.5-coder:7b", "abc123")
	if err != nil {
		t.Fatalf("leakage metadata: %v", err)
	}
	if m.Cutoff != "2023-06-30T00:00:00Z" {
		t.Errorf("Cutoff = %q", m.Cutoff)
	}
	if m.ModelCutoff != "2022-01-01T00:00:00Z" {
		t.Errorf("ModelCutoff = %q", m.ModelCutoff)
	}
	if m.CorpusSHA256 != "abc123" {
		t.Errorf("CorpusSHA256 = %q", m.CorpusSHA256)
	}
	if m.Model != "qwen2.5-coder:7b" {
		t.Errorf("Model = %q", m.Model)
	}
	// The note has to say that the cutoff cannot be inferred, because a
	// report that quietly leaves it out invites the reader to assume one.
	if !strings.Contains(m.Note, "cannot be inferred") {
		t.Errorf("the note does not say that the cutoff cannot be inferred: %q", m.Note)
	}
}

func TestLeakageMetadataAcceptsNoCutoff(t *testing.T) {
	m, err := leakageMetadata("", "", "model", "sha")
	if err != nil {
		t.Fatalf("leakage metadata: %v", err)
	}
	if m.Cutoff != "" || m.ModelCutoff != "" {
		t.Errorf("Cutoff = %q, ModelCutoff = %q, want both empty", m.Cutoff, m.ModelCutoff)
	}
}

func TestLeakageMetadataRejectsAMalformedDate(t *testing.T) {
	if _, err := leakageMetadata("30-06-2023", "", "model", "sha"); err == nil {
		t.Error("a malformed cutoff must be refused")
	}
	if _, err := leakageMetadata("", "not a date", "model", "sha"); err == nil {
		t.Error("a malformed model cutoff must be refused")
	}
}

func TestWriteReportProducesValidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	report := Report{
		SchemaVersion: "1",
		Corpus:        "realvuln",
		CorpusSHA256:  "deadbeef",
	}
	if err := writeReport(path, report); err != nil {
		t.Fatalf("write the report: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the report: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("the report is not valid JSON: %v", err)
	}
	if decoded["corpus"] != "realvuln" {
		t.Errorf("the corpus is %v, want %q", decoded["corpus"], "realvuln")
	}
}

func TestRunRefusesAnUnknownCorpus(t *testing.T) {
	// The LLM is disabled so that the command does not spend seconds
	// probing for a provider that is not there.
	t.Setenv("DISABLE_LLM", "1")

	err := run([]string{"--corpus", "not-a-corpus"})
	if err == nil {
		t.Fatal("an unknown corpus must be refused")
	}
	if !strings.Contains(err.Error(), "not-a-corpus") {
		t.Errorf("the error does not name the corpus: %v", err)
	}
}

func TestRunRefusesAMissingCorpusInOfflineMode(t *testing.T) {
	t.Setenv("DISABLE_LLM", "1")

	// The offline mode reads only the cache, so with an empty cache there
	// is nothing to measure. The command has to say so rather than report
	// an empty result, which would look like "no vulnerabilities found".
	err := run([]string{
		"--corpus", "realvuln",
		"--offline",
		"--cache-dir", filepath.Join(t.TempDir(), "empty-cache"),
	})
	if err == nil {
		t.Fatal("an empty cache in offline mode must be an error")
	}
}
