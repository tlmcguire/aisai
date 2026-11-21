package main

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeTag(t *testing.T) {
	cases := map[string]string{
		"gemma3n:e4b":  "gemma3ne4b",
		"qwen3:8b":     "qwen38b",
		"model/name":   "modelname",
		"":             "",
		"llama-3.2-3b": "llama323b",
	}
	for in, want := range cases {
		if got := sanitizeTag(in); got != want {
			t.Errorf("sanitizeTag(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDefaultOutputPath(t *testing.T) {
	path := defaultOutputPath("bandit", "sast")
	if !strings.HasPrefix(path, filepath.Join("bench", "results")) {
		t.Errorf("the default path is %q, want it under bench/results", path)
	}
	if !strings.HasPrefix(filepath.Base(path), "bench_bandit_sast_") {
		t.Errorf("the default file name is %q", filepath.Base(path))
	}
	if !strings.HasSuffix(path, ".csv") {
		t.Errorf("the default path is %q, want a .csv", path)
	}
}

func TestOutputLabel(t *testing.T) {
	label, tag := outputLabel("llm", "", "gemma3n:e4b", "")
	if label != "llm" || tag != "gemma3n:e4b" {
		t.Errorf("label/tag = %q/%q", label, tag)
	}
	label, tag = outputLabel("bandit", "", "gemma3n:e4b", "")
	if label != "bandit" || tag != "sast" {
		t.Errorf("label/tag = %q/%q", label, tag)
	}
	label, tag = outputLabel("llm", "always_safe", "gemma3n:e4b", "")
	if label != "baseline_always_safe" || tag != "baseline" {
		t.Errorf("label/tag = %q/%q", label, tag)
	}
	label, tag = outputLabel("llm", "", "gemma3n:e4b", "custom")
	if tag != "custom" {
		t.Errorf("tag = %q, want custom", tag)
	}
}

// TestRunDryRun writes a benchmark CSV through the command and checks the
// schema of the result.
func TestRunDryRun(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.csv")
	input := "cve_id,vulnerable_code,fixed_code\n" +
		"CVE-1,\"import subprocess\nsubprocess.run(cmd, shell=True)\",\"import subprocess\nsubprocess.run(['ls'])\"\n"
	if err := os.WriteFile(in, []byte(input), 0o600); err != nil {
		t.Fatalf("write input: %v", err)
	}
	out := filepath.Join(dir, "out.csv")

	if err := run([]string{
		"--input", in,
		"--out", out,
		"--engine", "both",
		"--dry-run",
		"--model", "stub-model",
	}); err != nil {
		t.Fatalf("run: %v", err)
	}

	f, err := os.Open(out)
	if err != nil {
		t.Fatalf("open output: %v", err)
	}
	defer f.Close()
	records, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("got %d records, want a header and one row", len(records))
	}
	header := records[0]
	index := map[string]int{}
	for i, c := range header {
		index[c] = i
	}
	for _, col := range []string{
		"cve_id", "vulnerable_code", "fixed_code",
		"benchmark_input_mode", "expected_before", "expected_after",
		"llm_classification_before", "llm_classification_after",
		"llm_completed_before", "llm_completed_after",
		"llm_time_before", "llm_time_after", "llm_model",
		"bandit_classification_before", "bandit_classification_after",
		"bandit_completed_before", "bandit_completed_after",
		"bandit_time_before", "bandit_time_after", "bandit_model",
	} {
		if _, ok := index[col]; !ok {
			t.Errorf("the output is missing the column %q", col)
		}
	}
	row := records[1]
	if got := row[index["benchmark_input_mode"]]; got != "paired" {
		t.Errorf("benchmark_input_mode = %q", got)
	}
	if got := row[index["expected_before"]]; got != "vulnerable" {
		t.Errorf("expected_before = %q", got)
	}
	if got := row[index["expected_after"]]; got != "safe" {
		t.Errorf("expected_after = %q", got)
	}
	if got := row[index["bandit_classification_before"]]; got != "vulnerable" {
		t.Errorf("bandit_classification_before = %q", got)
	}
	if got := row[index["llm_classification_before"]]; got != "vulnerable" {
		t.Errorf("llm_classification_before = %q", got)
	}
}

// TestRunBaseline checks that a baseline run writes the llm_ prefix.
func TestRunBaseline(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.csv")
	input := "cve_id,vulnerable_code,fixed_code\n" +
		"CVE-1,\"import subprocess\nsubprocess.run(cmd, shell=True)\",\"import subprocess\nsubprocess.run(['ls'])\"\n"
	if err := os.WriteFile(in, []byte(input), 0o600); err != nil {
		t.Fatalf("write input: %v", err)
	}
	out := filepath.Join(dir, "out.csv")

	if err := run([]string{
		"--input", in,
		"--out", out,
		"--baseline", "always_safe",
	}); err != nil {
		t.Fatalf("run: %v", err)
	}

	f, err := os.Open(out)
	if err != nil {
		t.Fatalf("open output: %v", err)
	}
	defer f.Close()
	records, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	header := records[0]
	index := map[string]int{}
	for i, c := range header {
		index[c] = i
	}
	if _, ok := index["llm_classification_before"]; !ok {
		t.Errorf("the baseline output is missing the llm_ columns: %v", header)
	}
	if got := records[1][index["llm_model"]]; got != "baseline:always_safe" {
		t.Errorf("llm_model = %q, want baseline:always_safe", got)
	}
	if got := records[1][index["llm_classification_before"]]; got != "safe" {
		t.Errorf("llm_classification_before = %q, want safe", got)
	}
}
