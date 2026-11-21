package external

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRealVulnMatchesEntryPathCWELine(t *testing.T) {
	entry := RealVulnEntry{
		ID:             "rv-001",
		IsVulnerable:   true,
		File:           "app/main.py",
		AcceptableCWEs: []string{"CWE-1336", "CWE-94"},
		StartLine:      41,
		EndLine:        41,
	}
	cases := []struct {
		name string
		f    Finding
		want bool
	}{
		{"exact", Finding{Path: "app/main.py", Line: 41, CWE: "CWE-1336"}, true},
		{"another acceptable cwe", Finding{Path: "app/main.py", Line: 41, CWE: "CWE-94"}, true},
		{"cwe written in lower case", Finding{Path: "app/main.py", Line: 41, CWE: "cwe-1336"}, true},
		{"line ten before", Finding{Path: "app/main.py", Line: 31, CWE: "CWE-1336"}, true},
		{"line ten after", Finding{Path: "app/main.py", Line: 51, CWE: "CWE-1336"}, true},
		{"line eleven before", Finding{Path: "app/main.py", Line: 30, CWE: "CWE-1336"}, false},
		{"line eleven after", Finding{Path: "app/main.py", Line: 52, CWE: "CWE-1336"}, false},
		{"wrong path", Finding{Path: "app/other.py", Line: 41, CWE: "CWE-1336"}, false},
		{"wrong cwe", Finding{Path: "app/main.py", Line: 41, CWE: "CWE-89"}, false},
		{"no cwe", Finding{Path: "app/main.py", Line: 41}, false},
		{"path with a leading dot", Finding{Path: "./app/main.py", Line: 41, CWE: "CWE-1336"}, true},
	}
	for _, tc := range cases {
		if got := RealVulnMatchesEntry(tc.f, entry); got != tc.want {
			t.Errorf("%s: RealVulnMatchesEntry = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestRealVulnMatchesEntryToleranceBoundaries(t *testing.T) {
	entry := RealVulnEntry{
		ID:             "rv-002",
		IsVulnerable:   true,
		File:           "a.py",
		AcceptableCWEs: []string{"CWE-22"},
		StartLine:      100,
		EndLine:        100,
	}
	// Exactly ten lines away on either side is inside the window; eleven is
	// outside it.
	if !RealVulnMatchesEntry(Finding{Path: "a.py", Line: 90, CWE: "CWE-22"}, entry) {
		t.Error("the line 90 must match the entry at 100 (exactly -10)")
	}
	if !RealVulnMatchesEntry(Finding{Path: "a.py", Line: 110, CWE: "CWE-22"}, entry) {
		t.Error("the line 110 must match the entry at 100 (exactly +10)")
	}
	if RealVulnMatchesEntry(Finding{Path: "a.py", Line: 89, CWE: "CWE-22"}, entry) {
		t.Error("the line 89 must not match the entry at 100 (-11)")
	}
	if RealVulnMatchesEntry(Finding{Path: "a.py", Line: 111, CWE: "CWE-22"}, entry) {
		t.Error("the line 111 must not match the entry at 100 (+11)")
	}
}

func TestRealVulnMatchesEntryLineRange(t *testing.T) {
	// The window is the line range of the entry widened by the tolerance,
	// not the tolerance around the start line alone.
	entry := RealVulnEntry{
		ID:             "rv-003",
		IsVulnerable:   true,
		File:           "a.py",
		AcceptableCWEs: []string{"CWE-22"},
		StartLine:      100,
		EndLine:        110,
	}
	if !RealVulnMatchesEntry(Finding{Path: "a.py", Line: 120, CWE: "CWE-22"}, entry) {
		t.Error("the line 120 must match the range 100-110 widened by 10")
	}
	if RealVulnMatchesEntry(Finding{Path: "a.py", Line: 121, CWE: "CWE-22"}, entry) {
		t.Error("the line 121 must not match the range 100-110 widened by 10")
	}
}

func TestRealVulnMatchesEntryAcceptableLocation(t *testing.T) {
	entry := RealVulnEntry{
		ID:             "rv-004",
		IsVulnerable:   true,
		File:           "app/main.py",
		AcceptableCWEs: []string{"CWE-79"},
		StartLine:      38,
		EndLine:        38,
		AcceptableLocations: []RealVulnLocation{
			{File: "app/other.py", StartLine: 7, EndLine: 7},
		},
	}
	if !RealVulnMatchesEntry(Finding{Path: "app/other.py", Line: 7, CWE: "CWE-79"}, entry) {
		t.Error("a finding on an acceptable location must match the entry")
	}
	if RealVulnMatchesEntry(Finding{Path: "app/other.py", Line: 18, CWE: "CWE-79"}, entry) {
		t.Error("a finding on an acceptable location outside the window must not match")
	}
}

func TestRealVulnMatchCounts(t *testing.T) {
	entries := []RealVulnEntry{
		{ID: "vuln-1", IsVulnerable: true, File: "a.py", AcceptableCWEs: []string{"CWE-22"}, StartLine: 10, EndLine: 10},
		{ID: "vuln-2", IsVulnerable: true, File: "b.py", AcceptableCWEs: []string{"CWE-78"}, StartLine: 20, EndLine: 20},
		{ID: "trap-1", IsVulnerable: false, File: "c.py", AcceptableCWEs: []string{"CWE-89"}, StartLine: 30, EndLine: 30},
	}
	findings := []Finding{
		{Path: "a.py", Line: 10, CWE: "CWE-22"},  // true positive
		{Path: "c.py", Line: 30, CWE: "CWE-89"},  // false positive on a trap
		{Path: "z.py", Line: 5, CWE: "CWE-89"},   // false positive, no entry
		{Path: "b.py", Line: 999, CWE: "CWE-78"}, // out of the window: false positive
	}
	res := MatchRealVuln(findings, entries)
	if res.Counts.TP != 1 {
		t.Errorf("true positives = %d, want 1", res.Counts.TP)
	}
	if res.Counts.FP != 3 {
		t.Errorf("false positives = %d, want 3", res.Counts.FP)
	}
	if res.Counts.FN != 1 {
		t.Errorf("false negatives = %d, want 1 (vuln-2 was never reported)", res.Counts.FN)
	}
	if res.Counts.TN != 0 {
		t.Errorf("true negatives = %d, want 0", res.Counts.TN)
	}
	if res.Traps.Traps != 1 {
		t.Errorf("traps = %d, want 1", res.Traps.Traps)
	}
	if res.Traps.TrapsHit != 1 {
		t.Errorf("traps hit = %d, want 1", res.Traps.TrapsHit)
	}
	if res.Traps.TrapsSilent != 0 {
		t.Errorf("traps silent = %d, want 0", res.Traps.TrapsSilent)
	}
}

func TestRealVulnMatchPrefersTheVulnerableEntry(t *testing.T) {
	// A trap and a real vulnerability sit on the same line: the tool must
	// get the credit for the real one.
	entries := []RealVulnEntry{
		{ID: "trap", IsVulnerable: false, File: "a.py", AcceptableCWEs: []string{"CWE-22"}, StartLine: 10, EndLine: 10},
		{ID: "vuln", IsVulnerable: true, File: "a.py", AcceptableCWEs: []string{"CWE-22"}, StartLine: 12, EndLine: 12},
	}
	findings := []Finding{{Path: "a.py", Line: 11, CWE: "CWE-22"}}
	res := MatchRealVuln(findings, entries)
	if res.Counts.TP != 1 {
		t.Errorf("true positives = %d, want 1", res.Counts.TP)
	}
	if res.Counts.FP != 0 {
		t.Errorf("false positives = %d, want 0", res.Counts.FP)
	}
}

func TestRealVulnMatchNonScoringEntryIsWithheld(t *testing.T) {
	entries := []RealVulnEntry{
		{ID: "vuln", IsVulnerable: true, File: "a.py", AcceptableCWEs: []string{"CWE-22"}, StartLine: 10, EndLine: 10},
		{ID: "unsettled", IsVulnerable: false, File: "a.py", AcceptableCWEs: []string{"CWE-89"}, StartLine: 50, EndLine: 50, NonScoring: true},
	}
	findings := []Finding{
		{Path: "a.py", Line: 50, CWE: "CWE-89"},
	}
	res := MatchRealVuln(findings, entries)
	if res.Counts.FP != 0 {
		t.Errorf("false positives = %d, want 0: a non-scoring entry is withheld", res.Counts.FP)
	}
	if res.Counts.FN != 1 {
		t.Errorf("false negatives = %d, want 1", res.Counts.FN)
	}
	found := false
	for _, m := range res.Matches {
		if m.Classification == MatchNS {
			found = true
		}
	}
	if !found {
		t.Error("the withheld finding must be reported as NS")
	}
}

func TestParseRealVulnGroundTruth(t *testing.T) {
	const doc = `{
	  "schema_version": "1.0",
	  "repo_id": "demo",
	  "repo_url": "https://github.com/example/demo",
	  "commit_sha": "abc123",
	  "language": "python",
	  "findings": [
	    {
	      "id": "demo-001",
	      "is_vulnerable": true,
	      "primary_cwe": "CWE-1336",
	      "acceptable_cwes": ["CWE-1336", "CWE-94"],
	      "file": "./app/main.py",
	      "location": {"start_line": 41, "end_line": 41}
	    },
	    {
	      "id": "demo-fp-001",
	      "is_vulnerable": false,
	      "primary_cwe": "CWE-918",
	      "acceptable_cwes": ["CWE-918"],
	      "file": "app/main.py",
	      "location": {"start_line": 19, "end_line": 19}
	    }
	  ]
	}`
	gt, err := ParseRealVulnGroundTruth(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if gt.RepoID != "demo" || gt.Language != "python" {
		t.Errorf("ground truth = %q/%q", gt.RepoID, gt.Language)
	}
	if len(gt.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(gt.Entries))
	}
	if gt.Entries[0].File != "app/main.py" {
		t.Errorf("the path was not normalised: %q", gt.Entries[0].File)
	}
	if len(gt.Entries[0].AcceptableCWEs) != 2 {
		t.Errorf("acceptable CWEs = %v", gt.Entries[0].AcceptableCWEs)
	}
	if gt.Entries[1].IsVulnerable {
		t.Error("the second entry must not be vulnerable")
	}
}

func TestParseBenchmarkExpectedResultsFileLevel(t *testing.T) {
	const doc = `# test name, category, real vulnerability, cwe, Benchmark version: 0.1, 2026-01-9
BenchmarkTest00001,pathtraver,true,22
BenchmarkTest00002,pathtraver,false,22
`
	cases, err := ParseBenchmarkExpectedResults(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(cases) != 2 {
		t.Fatalf("cases = %d, want 2", len(cases))
	}
	if cases[0].Name != "BenchmarkTest00001" || !cases[0].Vulnerable {
		t.Errorf("case 0 = %+v", cases[0])
	}
	if cases[0].CWE != "CWE-22" {
		t.Errorf("the CWE was not normalised: %q", cases[0].CWE)
	}
	if cases[0].Path != "testcode/BenchmarkTest00001.py" {
		t.Errorf("path = %q", cases[0].Path)
	}
	if cases[1].Vulnerable {
		t.Error("the second case must not be vulnerable")
	}
	if cases[0].HasLineExpectations() {
		t.Error("the file-level layout carries no line expectations")
	}
}

func TestParseBenchmarkExpectedResultsPerLine(t *testing.T) {
	const doc = `BenchmarkTest00001,pathtraver,true,22,BenchmarkTest00001.py,47,TP
BenchmarkTest00001,pathtraver,true,22,BenchmarkTest00001.py,52,FP
`
	cases, err := ParseBenchmarkExpectedResults(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(cases) != 1 {
		t.Fatalf("cases = %d, want 1", len(cases))
	}
	c := cases[0]
	if !c.HasLineExpectations() {
		t.Fatal("the per-line layout must carry line expectations")
	}
	if c.Expected[47] != BenchmarkTP {
		t.Errorf("line 47 = %q, want TP", c.Expected[47])
	}
	if c.Expected[52] != BenchmarkFP {
		t.Errorf("line 52 = %q, want FP", c.Expected[52])
	}
	if c.Path != "testcode/BenchmarkTest00001.py" {
		t.Errorf("path = %q", c.Path)
	}
}

func TestParseBenchmarkExpectedResultsRejectsGarbage(t *testing.T) {
	const doc = `BenchmarkTest00001,pathtraver,true,22,BenchmarkTest00001.py,not-a-line,TP
`
	if _, err := ParseBenchmarkExpectedResults(strings.NewReader(doc)); err == nil {
		t.Fatal("expected an error for a line that is not a number")
	}
}

func TestMatchBenchmarkPerLine(t *testing.T) {
	cases := []BenchmarkCase{{
		Name:       "BenchmarkTest00001",
		Path:       "testcode/BenchmarkTest00001.py",
		Vulnerable: true,
		CWE:        "CWE-22",
		Expected:   map[int]string{47: BenchmarkTP, 52: BenchmarkFP},
	}}
	findings := []Finding{
		{Path: "testcode/BenchmarkTest00001.py", Line: 47, CWE: "CWE-22"},
		{Path: "testcode/BenchmarkTest00001.py", Line: 52, CWE: "CWE-22"},
		{Path: "testcode/BenchmarkTest00001.py", Line: 60, CWE: "CWE-22"},
		{Path: "testcode/other.py", Line: 1, CWE: "CWE-22"},
	}
	res := MatchBenchmark(findings, cases)
	if res.Counts.TP != 1 {
		t.Errorf("true positives = %d, want 1", res.Counts.TP)
	}
	if res.Counts.FP != 3 {
		t.Errorf("false positives = %d, want 3", res.Counts.FP)
	}
	if res.Counts.FN != 0 {
		t.Errorf("false negatives = %d, want 0", res.Counts.FN)
	}
}

func TestMatchBenchmarkFileLevel(t *testing.T) {
	cases := []BenchmarkCase{
		{Name: "BenchmarkTest00001", Path: "testcode/BenchmarkTest00001.py", Vulnerable: true, CWE: "CWE-22"},
		{Name: "BenchmarkTest00002", Path: "testcode/BenchmarkTest00002.py", Vulnerable: false, CWE: "CWE-22"},
	}
	findings := []Finding{
		{Path: "testcode/BenchmarkTest00001.py", Line: 47, CWE: "CWE-22"},
		{Path: "testcode/BenchmarkTest00002.py", Line: 12, CWE: "CWE-22"},
	}
	res := MatchBenchmark(findings, cases)
	if res.Counts.TP != 1 {
		t.Errorf("true positives = %d, want 1", res.Counts.TP)
	}
	if res.Counts.FP != 1 {
		t.Errorf("false positives = %d, want 1", res.Counts.FP)
	}
	if res.Counts.FN != 0 {
		t.Errorf("false negatives = %d, want 0", res.Counts.FN)
	}
}

func TestMatchBenchmarkCWGate(t *testing.T) {
	cases := []BenchmarkCase{{
		Name:       "BenchmarkTest00001",
		Path:       "testcode/BenchmarkTest00001.py",
		Vulnerable: true,
		CWE:        "CWE-22",
		Expected:   map[int]string{47: BenchmarkTP},
	}}
	// A finding of another weakness on an expected line is a false
	// positive, and the expectation is missed.
	res := MatchBenchmark([]Finding{
		{Path: "testcode/BenchmarkTest00001.py", Line: 47, CWE: "CWE-89"},
	}, cases)
	if res.Counts.TP != 0 {
		t.Errorf("true positives = %d, want 0", res.Counts.TP)
	}
	if res.Counts.FP != 1 {
		t.Errorf("false positives = %d, want 1", res.Counts.FP)
	}
	if res.Counts.FN != 1 {
		t.Errorf("false negatives = %d, want 1", res.Counts.FN)
	}
}

func TestMatchBenchmarkMissedExpectation(t *testing.T) {
	cases := []BenchmarkCase{{
		Name:       "BenchmarkTest00001",
		Path:       "testcode/BenchmarkTest00001.py",
		Vulnerable: true,
		CWE:        "CWE-22",
		Expected:   map[int]string{47: BenchmarkTP, 51: BenchmarkTP},
	}}
	res := MatchBenchmark(nil, cases)
	if res.Counts.FN != 2 {
		t.Errorf("false negatives = %d, want 2", res.Counts.FN)
	}
}

func TestStaticDetectorFindsTheRuleEngineFindings(t *testing.T) {
	det := NewStaticDetector(SeverityPolicy)
	found, err := det.Detect(context.Background(), "app/x.py", "import subprocess\nsubprocess.run(cmd, shell=True)\n")
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(found) == 0 {
		t.Fatal("the rule engine must report the subprocess call")
	}
	if found[0].Path != "app/x.py" {
		t.Errorf("path = %q", found[0].Path)
	}
	if found[0].CWE != "CWE-78" {
		t.Errorf("CWE = %q, want CWE-78", found[0].CWE)
	}
	if found[0].Line != 2 {
		t.Errorf("line = %d, want 2", found[0].Line)
	}
}

func TestStaticDetectorOnSafeCode(t *testing.T) {
	det := NewStaticDetector(SeverityPolicy)
	found, err := det.Detect(context.Background(), "app/x.py", "import subprocess\nsubprocess.run(['ls', '-l'])\n")
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("the rule engine reported %d findings on safe code", len(found))
	}
}

func TestFetchOfflineWithoutCacheFails(t *testing.T) {
	dir := t.TempDir()
	_, err := Fetch(context.Background(), dir, Artefact{
		Name: "missing.tar.gz",
		URL:  "https://example.invalid/missing.tar.gz",
	}, true)
	if err == nil {
		t.Fatal("expected an error in offline mode")
	}
	if !strings.Contains(err.Error(), "offline") {
		t.Errorf("the error must say that the mode is offline: %v", err)
	}
}

func TestFetchVerifiesThePinnedDigest(t *testing.T) {
	dir := t.TempDir()
	// Write a file in the cache and ask for an artefact with a digest that
	// does not match it.
	name := "artefact.bin"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := Fetch(context.Background(), dir, Artefact{
		Name:   name,
		URL:    "https://example.invalid/artefact.bin",
		SHA256: "0000000000000000000000000000000000000000000000000000000000000000",
	}, true)
	if err == nil {
		t.Fatal("expected an error for a digest that does not match")
	}
	if !strings.Contains(err.Error(), "digest") {
		t.Errorf("the error must name the digest: %v", err)
	}
}

func TestNormalizePathAndCWE(t *testing.T) {
	if got := NormalizePath("./a/b.py"); got != "a/b.py" {
		t.Errorf("path = %q", got)
	}
	if got := NormalizePath("/a/b.py"); got != "a/b.py" {
		t.Errorf("path = %q", got)
	}
	if got := NormalizePath(`a\b.py`); got != "a/b.py" {
		t.Errorf("path = %q", got)
	}
	if got := NormalizeCWE("cwe-78"); got != "CWE-78" {
		t.Errorf("cwe = %q", got)
	}
	if got := NormalizeCWE("78"); got != "CWE-78" {
		t.Errorf("cwe = %q", got)
	}
	if got := NormalizeCWE(""); got != "" {
		t.Errorf("cwe = %q, want the empty string", got)
	}
}
