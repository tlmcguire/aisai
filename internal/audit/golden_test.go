package audit

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/tlmcguire/aisai/internal/ast"
	"github.com/tlmcguire/aisai/internal/bench"
	"github.com/tlmcguire/aisai/internal/findings"
	"github.com/tlmcguire/aisai/internal/model"
	"github.com/tlmcguire/aisai/internal/sast"
)

// This file holds the golden snapshot of the static engine.
//
// The snapshot is what makes a change to internal/sast visible. The engine is
// deterministic, so the findings it produces for a fixed set of fixtures are
// fixed as well: a rule that starts firing, stops firing, or changes the line
// or the message it reports changes the snapshot, and the test fails.
//
// A change that only reorders the findings does not change the snapshot:
// both sides are sorted before they are compared.
//
// The fixtures are vendored in testdata/golden/pkg so that the test runs
// offline. Nothing is downloaded.
//
// Regenerate the snapshot with:
//
//	go test ./internal/audit -update
//
// and review the diff. A snapshot that changes because a rule was tightened
// is expected; a snapshot that changes because a rule stopped firing is a
// regression.

// goldenUpdate rewrites the golden snapshot instead of comparing against it.
var goldenUpdate = flag.Bool("update", false,
	"rewrite the golden findings snapshot in internal/audit/testdata/golden")

const (
	// goldenDir holds the snapshot and the fixtures.
	goldenDir = "testdata/golden"
	// goldenFixtureDir holds the Python fixtures.
	goldenFixtureDir = "testdata/golden/pkg"
	// goldenPath is the snapshot file.
	goldenPath = "testdata/golden/findings.json"
	// cleanFixture is the fixture that must never produce a finding.
	cleanFixture = "pkg/clean.py"
)

// goldenFinding is one recorded finding.
//
// The record holds the fields that make a finding joinable and triageable:
// the rule that produced it, the CWE it maps to, where it is, how severe it
// is and what it says. The code snippet, the confidence and the evidence are
// deliberately not recorded: they change without the finding changing, and a
// snapshot that churns is a snapshot nobody reads.
type goldenFinding struct {
	Rule     string `json:"rule"`
	CWE      string `json:"cwe"`
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

// goldenSnapshot is the on-disk snapshot.
type goldenSnapshot struct {
	// Policy records the severity threshold the snapshot was taken under.
	Policy string `json:"policy"`
	// Findings are the recorded findings, sorted.
	Findings []goldenFinding `json:"findings"`
}

// goldenFixtures parses the Python fixtures of the snapshot.
//
// The path recorded on each finding is the path relative to the fixture
// directory, so that the snapshot does not depend on where the repository
// lives on disk.
func goldenFixtures(t *testing.T) []*ast.File {
	t.Helper()

	var paths []string
	err := filepath.WalkDir(goldenFixtureDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".py") {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", goldenFixtureDir, err)
	}
	if len(paths) == 0 {
		t.Fatalf("%s holds no fixtures", goldenFixtureDir)
	}
	sort.Strings(paths)

	files := make([]*ast.File, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		rel, err := filepath.Rel(goldenDir, path)
		if err != nil {
			t.Fatalf("rel %s: %v", path, err)
		}
		rel = filepath.ToSlash(rel)
		file, _ := ast.ParseString(rel, string(data))
		if file == nil {
			t.Fatalf("the fixture %s could not be parsed", rel)
		}
		files = append(files, file)
	}
	return files
}

// goldenScan runs the static engine over the given files and normalizes the
// findings the same way audit.Manager.Scan normalizes them.
//
// The normalization mirrors internal/audit/manager.go: the severity is
// replaced when it is not a known value, the confidence is clamped to
// (0,1], the state defaults to open and the path is made relative. The
// package attribution is left out: the fixtures are not packages.
func goldenScan(t *testing.T, files []*ast.File) []model.Finding {
	t.Helper()

	found := sast.NewEngine().Scan(files)
	out := make([]model.Finding, 0, len(found))
	for _, f := range found {
		if !f.Severity.Valid() {
			f.Severity = model.SeverityInfo
		}
		if f.Confidence <= 0 || f.Confidence > 1 {
			f.Confidence = 0.5
		}
		if f.State == "" {
			f.State = model.StateOpen
		}
		f.File = model.RelPath(f.File)
		if f.ID == "" {
			f.ID = model.FindingID(f.File, f.StartLine, f.CWE, f.Rule)
		}
		out = append(out, f)
	}
	return out
}

// goldenRecord turns the findings into the recorded form and sorts them.
//
// The sort is by path, then line, then rule, then CWE. Sorting both sides
// before the comparison is what makes a cosmetic reordering of the engine
// output invisible to the snapshot.
func goldenRecord(findings []model.Finding) []goldenFinding {
	out := make([]goldenFinding, 0, len(findings))
	for _, f := range findings {
		out = append(out, goldenFinding{
			Rule:     f.Rule,
			CWE:      f.CWE,
			Path:     f.File,
			Line:     f.StartLine,
			Severity: string(f.Severity),
			Message:  f.Message,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Rule != b.Rule {
			return a.Rule < b.Rule
		}
		return a.CWE < b.CWE
	})
	return out
}

// loadGolden reads the snapshot from disk.
func loadGolden(t *testing.T) goldenSnapshot {
	t.Helper()
	data, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read %s: %v (run: go test ./internal/audit -update)", goldenPath, err)
	}
	var g goldenSnapshot
	if err := json.Unmarshal(data, &g); err != nil {
		t.Fatalf("parse %s: %v", goldenPath, err)
	}
	return g
}

// writeGolden writes the snapshot to disk.
func writeGolden(t *testing.T, g goldenSnapshot) {
	t.Helper()
	data, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		t.Fatalf("marshal the snapshot: %v", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(goldenPath, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", goldenPath, err)
	}
}

// TestGoldenFindings compares the findings of the static engine over the
// vendored fixtures with the checked-in snapshot.
func TestGoldenFindings(t *testing.T) {
	found := goldenScan(t, goldenFixtures(t))
	got := goldenRecord(found)

	if *goldenUpdate {
		writeGolden(t, goldenSnapshot{
			Policy:   string(model.SeverityLow),
			Findings: got,
		})
		t.Logf("rewrote %s with %d findings", goldenPath, len(got))
		return
	}

	want := loadGolden(t)
	if want.Policy != string(model.SeverityLow) {
		t.Errorf("the snapshot was taken under the severity policy %q, want %q", want.Policy, model.SeverityLow)
	}

	wantJSON, err := json.MarshalIndent(want.Findings, "", "  ")
	if err != nil {
		t.Fatalf("marshal the snapshot: %v", err)
	}
	gotJSON, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatalf("marshal the findings: %v", err)
	}
	if string(wantJSON) != string(gotJSON) {
		t.Errorf("the findings changed.\n\nwant:\n%s\n\ngot:\n%s\n\nif the change is intended, regenerate the snapshot with:\n\n\tgo test ./internal/audit -update\n",
			wantJSON, gotJSON)
	}
}

// TestGoldenSnapshotIsNotEmpty guards against a snapshot that records
// nothing.
//
// A golden file that silently emptied itself would make every later change
// invisible, and an empty result is exactly what a broken engine produces.
func TestGoldenSnapshotIsNotEmpty(t *testing.T) {
	found := goldenScan(t, goldenFixtures(t))
	if len(found) == 0 {
		t.Fatal("the static engine reported nothing over the golden fixtures; the fixtures or the engine are broken")
	}

	got := goldenRecord(found)
	rules := map[string]bool{}
	for _, f := range got {
		rules[f.Rule] = true
	}
	// The snapshot has to cover several rules, not just one.
	if len(rules) < 3 {
		t.Errorf("the fixtures trigger %d rules (%v), want at least 3", len(rules), rules)
	}
}

// TestGoldenCleanFixtureTriggersNothing checks the control fixture: the
// module that does nothing dangerous must not produce a finding.
func TestGoldenCleanFixtureTriggersNothing(t *testing.T) {
	found := goldenScan(t, goldenFixtures(t))
	for _, f := range found {
		if f.File == cleanFixture {
			t.Errorf("the clean fixture produced a finding: %s at line %d (%s)", f.Rule, f.StartLine, f.Message)
		}
	}
}

// TestSeverityPolicyCountsLowFindings is the regression test for the
// divergence recorded as F.1.4.
//
// The legacy application ran Bandit at "-lll", which reports HIGH findings
// only, while its own benchmark accepted MEDIUM and HIGH. The rewrite has one
// engine and one policy: bench.SeverityPolicy is model.SeverityLow, which
// means "count every finding the engine reports" and filters out only the
// informational ones.
//
// This test asserts three things:
//
//  1. the policy really is model.SeverityLow;
//  2. the snapshot contains a LOW finding, so a filter that dropped the LOW
//     findings would change it;
//  3. every recorded finding is at or above the policy threshold, so the
//     snapshot cannot hold a finding the policy would have filtered.
func TestSeverityPolicyCountsLowFindings(t *testing.T) {
	if bench.SeverityPolicy != model.SeverityLow {
		t.Fatalf("the severity policy is %q, want %q", bench.SeverityPolicy, model.SeverityLow)
	}

	got := goldenRecord(goldenScan(t, goldenFixtures(t)))

	low := 0
	for _, f := range got {
		sev := model.Severity(f.Severity)
		if !sev.AtLeast(model.SeverityLow) {
			t.Errorf("the finding %s in %s is %q, which is below the policy threshold %q",
				f.Rule, f.Path, f.Severity, model.SeverityLow)
		}
		if sev == model.SeverityLow {
			low++
		}
	}
	if low == 0 {
		t.Fatalf("the snapshot holds no LOW finding; the fixture set must exercise one so that the severity policy is actually tested")
	}

	// The legacy behaviour: report the HIGH findings only. The policy counts
	// the LOW findings as well, so the two disagree, and that disagreement is
	// the bug this test exists to keep from coming back.
	legacy := 0
	for _, f := range got {
		if model.Severity(f.Severity).Rank() >= model.SeverityHigh.Rank() {
			legacy++
		}
	}
	if legacy == len(got) {
		t.Errorf("the snapshot holds no finding below HIGH; the policy test cannot see the difference between %q and a HIGH-only filter",
			model.SeverityLow)
	}
}

// TestGoldenIsIndependentOfFileOrder checks that the snapshot does not depend
// on the order in which the engine is handed the files.
//
// A reordering of the input is a cosmetic change; the snapshot must not move
// because of it.
func TestGoldenIsIndependentOfFileOrder(t *testing.T) {
	files := goldenFixtures(t)

	forward := goldenRecord(goldenScan(t, files))

	reversed := make([]*ast.File, 0, len(files))
	for i := len(files) - 1; i >= 0; i-- {
		reversed = append(reversed, files[i])
	}
	backward := goldenRecord(goldenScan(t, reversed))

	forwardJSON, err := json.Marshal(forward)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	backwardJSON, err := json.Marshal(backward)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(forwardJSON) != string(backwardJSON) {
		t.Errorf("the findings depend on the order of the input files:\n%s\n%s", forwardJSON, backwardJSON)
	}
}

// goldenSARIFPath holds the SARIF snapshot.
const goldenSARIFPath = "testdata/golden/sarif.json"

// TestGoldenSARIF pins the SARIF document the engine produces.
//
// The findings snapshot above pins what the engine *finds*. This one pins
// what the exporter *emits*: the document structure, the rule catalogue, the
// level mapping and the result locations. A change to the SARIF emitter —
// a renamed field, a rule that stops being declared, a severity that stops
// mapping to a SARIF level — changes this snapshot and fails the test.
//
// The snapshot is taken over the vendored fixture package, not over packages
// downloaded from PyPI: a test that needs the network is a test that fails
// for reasons that have nothing to do with the code.
//
// Regenerate it with:
//
//	go test ./internal/audit -update
func TestGoldenSARIF(t *testing.T) {
	found := goldenScan(t, goldenFixtures(t))

	data, err := findings.WriteSARIF(found, findings.SARIFOptions{
		ToolName:    "AISAI",
		ToolVersion: "golden",
	})
	if err != nil {
		t.Fatalf("write the SARIF document: %v", err)
	}
	if err := findings.ValidateSARIF(data); err != nil {
		t.Fatalf("the SARIF document is not valid: %v", err)
	}

	if *goldenUpdate {
		if err := os.WriteFile(goldenSARIFPath, data, 0o644); err != nil {
			t.Fatalf("write %s: %v", goldenSARIFPath, err)
		}
		t.Logf("rewrote %s", goldenSARIFPath)
		return
	}

	want, err := os.ReadFile(goldenSARIFPath)
	if err != nil {
		t.Fatalf("read %s: %v (run: go test ./internal/audit -update)", goldenSARIFPath, err)
	}
	if string(want) != string(data) {
		t.Errorf("the SARIF document changed.\n\nwant:\n%s\n\ngot:\n%s\n\nif the change is intended, regenerate the snapshot with:\n\n\tgo test ./internal/audit -update\n",
			want, data)
	}
}
