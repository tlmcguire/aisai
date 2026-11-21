package findings

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/tlmcguire/aisai/internal/model"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "findings.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func sampleFinding(id, file string, line int, sev model.Severity) model.Finding {
	return model.Finding{
		ID:         id,
		Rule:       "test-rule",
		Tool:       "aisai",
		CWE:        "CWE-798",
		Severity:   sev,
		Confidence: 0.8,
		File:       file,
		StartLine:  line,
		EndLine:    line,
		Code:       "secret = 'x'",
		Message:    "hardcoded secret",
		Evidence:   model.Evidence{Source: "src", Sink: "sink", Flow: []string{"a", "b"}},
		State:      model.StateOpen,
	}
}

func TestUpsertAndList(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	fs := []model.Finding{
		sampleFinding("f1", "pkg/a.py", 10, model.SeverityLow),
		sampleFinding("f2", "pkg/b.py", 20, model.SeverityCritical),
		sampleFinding("f3", "pkg/c.py", 30, model.SeverityMedium),
	}
	if _, err := s.Upsert(ctx, "audit1", fs); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := s.List(ctx, "audit1", Filter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d findings, want 3", len(got))
	}
	// Most severe first.
	if got[0].ID != "f2" || got[1].ID != "f3" || got[2].ID != "f1" {
		t.Errorf("ordering wrong: %s, %s, %s", got[0].ID, got[1].ID, got[2].ID)
	}
	if got[0].Severity != model.SeverityCritical {
		t.Errorf("severity = %q, want critical", got[0].Severity)
	}
	if len(got[0].Evidence.Flow) != 2 {
		t.Errorf("evidence flow lost: %#v", got[0].Evidence.Flow)
	}
}

func TestUpsertPreservesTriageState(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	f := sampleFinding("f1", "pkg/a.py", 10, model.SeverityHigh)
	if _, err := s.Upsert(ctx, "audit1", []model.Finding{f}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	until := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second)
	if err := s.SetState(ctx, "audit1", "f1", model.StateSuppressed, &until); err != nil {
		t.Fatalf("set state: %v", err)
	}

	// Re-scan the same finding: the triage decision must survive.
	f.Message = "hardcoded secret (updated)"
	if _, err := s.Upsert(ctx, "audit1", []model.Finding{f}); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}

	got, err := s.Get(ctx, "audit1", "f1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.State != model.StateSuppressed {
		t.Errorf("state = %q, want suppressed", got.State)
	}
	if got.SuppressedUntil == nil {
		t.Error("suppressedUntil was lost on re-scan")
	}
	if got.Message != "hardcoded secret (updated)" {
		t.Errorf("message was not refreshed: %q", got.Message)
	}
}

func TestRescanDoesNotWipeOtherFindings(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	// Two findings from one scan.
	if _, err := s.Upsert(ctx, "audit1", []model.Finding{
		sampleFinding("f1", "pkg/a.py", 10, model.SeverityHigh),
		sampleFinding("f2", "pkg/b.py", 20, model.SeverityHigh),
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// A later scan reports only one of them. The other must survive:
	// this is the bug that made the legacy app wipe results on every scan.
	if _, err := s.Upsert(ctx, "audit1", []model.Finding{
		sampleFinding("f1", "pkg/a.py", 10, model.SeverityHigh),
	}); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	total, err := s.Total(ctx, "audit1")
	if err != nil {
		t.Fatalf("total: %v", err)
	}
	if total != 2 {
		t.Fatalf("total = %d, want 2 (findings were wiped by the rescan)", total)
	}
}

func TestFindingsAreScopedPerAudit(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	if _, err := s.Upsert(ctx, "audit1", []model.Finding{sampleFinding("f1", "pkg/a.py", 1, model.SeverityHigh)}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := s.Upsert(ctx, "audit2", []model.Finding{sampleFinding("f2", "pkg/b.py", 2, model.SeverityHigh)}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	a1, _ := s.List(ctx, "audit1", Filter{})
	a2, _ := s.List(ctx, "audit2", Filter{})
	if len(a1) != 1 || a1[0].ID != "f1" {
		t.Errorf("audit1 findings = %#v", a1)
	}
	if len(a2) != 1 || a2[0].ID != "f2" {
		t.Errorf("audit2 findings = %#v", a2)
	}
}

func TestFilterBySeverityAndCWE(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	if _, err := s.Upsert(ctx, "a1", []model.Finding{
		sampleFinding("f1", "x.py", 1, model.SeverityCritical),
		sampleFinding("f2", "y.py", 2, model.SeverityLow),
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	crit, err := s.List(ctx, "a1", Filter{Severities: []string{"critical"}})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(crit) != 1 || crit[0].ID != "f1" {
		t.Errorf("severity filter returned %#v", crit)
	}

	byCWE, err := s.List(ctx, "a1", Filter{CWEs: []string{"CWE-798"}})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(byCWE) != 2 {
		t.Errorf("cwe filter returned %d, want 2", len(byCWE))
	}

	none, err := s.List(ctx, "a1", Filter{CWEs: []string{"CWE-000"}})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("expected no findings, got %d", len(none))
	}
}

func TestSetStateValidation(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	if _, err := s.Upsert(ctx, "a1", []model.Finding{sampleFinding("f1", "x.py", 1, model.SeverityHigh)}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	if err := s.SetState(ctx, "a1", "f1", "bogus", nil); err == nil {
		t.Error("expected an error for an invalid state")
	}
	if err := s.SetState(ctx, "a1", "f1", model.StateSuppressed, nil); err == nil {
		t.Error("expected an error when suppressedUntil is missing")
	}
	if err := s.SetState(ctx, "a1", "missing", model.StateConfirmed, nil); err == nil {
		t.Error("expected ErrNotFound for a missing finding")
	}
}

func TestCountBySeverity(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	if _, err := s.Upsert(ctx, "a1", []model.Finding{
		sampleFinding("f1", "x.py", 1, model.SeverityCritical),
		sampleFinding("f2", "y.py", 2, model.SeverityCritical),
		sampleFinding("f3", "z.py", 3, model.SeverityLow),
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	counts, err := s.CountBySeverity(ctx, "a1")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if counts["critical"] != 2 || counts["low"] != 1 {
		t.Errorf("counts = %#v", counts)
	}
}

func TestSARIFIsValid(t *testing.T) {
	fs := []model.Finding{
		sampleFinding("f1", "pkg/a.py", 10, model.SeverityCritical),
		sampleFinding("f2", "pkg/b.py", 20, model.SeverityLow),
	}
	data, err := WriteSARIF(fs, SARIFOptions{})
	if err != nil {
		t.Fatalf("write sarif: %v", err)
	}
	if err := ValidateSARIF(data); err != nil {
		t.Fatalf("validate sarif: %v", err)
	}

	var generic map[string]any
	if err := json.Unmarshal(data, &generic); err != nil {
		t.Fatalf("sarif is not JSON: %v", err)
	}
	if generic["version"] != "2.1.0" {
		t.Errorf("version = %v, want 2.1.0", generic["version"])
	}
	runs := generic["runs"].([]any)
	run := runs[0].(map[string]any)
	results := run["results"].([]any)
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	// Critical must be an error, low must be a note.
	first := results[0].(map[string]any)
	if first["level"] != "error" {
		t.Errorf("critical level = %v, want error", first["level"])
	}
	loc := first["locations"].([]any)[0].(map[string]any)
	phys := loc["physicalLocation"].(map[string]any)
	region := phys["region"].(map[string]any)
	if region["startLine"].(float64) != 10 {
		t.Errorf("startLine = %v, want 10", region["startLine"])
	}
	uri := phys["artifactLocation"].(map[string]any)["uri"].(string)
	if uri != "pkg/a.py" {
		t.Errorf("uri = %q, want pkg/a.py", uri)
	}
}

func TestSARIFEmptyFindings(t *testing.T) {
	data, err := WriteSARIF(nil, SARIFOptions{})
	if err != nil {
		t.Fatalf("write sarif: %v", err)
	}
	if err := ValidateSARIF(data); err != nil {
		t.Fatalf("validate sarif: %v", err)
	}
}
