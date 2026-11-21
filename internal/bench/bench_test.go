package bench

import (
	"context"
	"encoding/csv"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tlmcguire/aisai/internal/llm"
)

// corpusPath is the real corpus, relative to this package.
const corpusPath = "../../bench/data1551redacted.csv"

// sampleInput is a small paired corpus used by the tests.
const sampleInput = `cve_id,severity,vulnerable_code,fixed_code
CVE-1,HIGH,"import subprocess
subprocess.run(cmd, shell=True)","import subprocess
subprocess.run(['ls'])"
CVE-2,LOW,"x = 1","x = 2"
`

// readCSV reads a CSV file into its header and its rows.
func readCSV(t *testing.T, path string) ([]string, [][]string) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	records, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if len(records) == 0 {
		t.Fatalf("%s is empty", path)
	}
	return records[0], records[1:]
}

func TestDetectSchemaPaired(t *testing.T) {
	schema, err := DetectSchema(
		[]string{"cve_id", "vulnerable_code", "fixed_code"},
		"vulnerable_code", "fixed_code", "", "")
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if schema.Mode != ModePaired {
		t.Errorf("mode = %q, want paired", schema.Mode)
	}
	if schema.BeforeCol != "vulnerable_code" || schema.AfterCol != "fixed_code" {
		t.Errorf("columns = %q/%q", schema.BeforeCol, schema.AfterCol)
	}
}

func TestDetectSchemaSingle(t *testing.T) {
	schema, err := DetectSchema(
		[]string{"code_sample", "sample_label"},
		"vulnerable_code", "fixed_code", "", "")
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if schema.Mode != ModeSingle {
		t.Errorf("mode = %q, want single", schema.Mode)
	}
	if schema.CodeCol != "code_sample" || schema.LabelCol != "sample_label" {
		t.Errorf("columns = %q/%q", schema.CodeCol, schema.LabelCol)
	}
}

func TestDetectSchemaSinglePrefersOverride(t *testing.T) {
	schema, err := DetectSchema(
		[]string{"snippet", "label"},
		"vulnerable_code", "fixed_code", "snippet", "label")
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if schema.CodeCol != "snippet" || schema.LabelCol != "label" {
		t.Errorf("columns = %q/%q", schema.CodeCol, schema.LabelCol)
	}
}

func TestDetectSchemaUnknown(t *testing.T) {
	if _, err := DetectSchema([]string{"cve_id"}, "vulnerable_code", "fixed_code", "", ""); err == nil {
		t.Fatal("expected an error for an unrecognisable schema")
	}
}

// pairedHeader is the header the harness writes for a paired run with one
// engine. It is the contract the notebooks rely on.
func pairedHeader(prefix string, original []string) []string {
	header := append([]string(nil), original...)
	header = append(header, ColumnInputMode, ColumnExpectedBefore, ColumnExpectedAfter)
	header = append(header,
		prefix+"_classification_before",
		prefix+"_classification_after",
		prefix+"_correct_before",
		prefix+"_correct_after",
		prefix+"_success_before",
		prefix+"_success_after",
		prefix+"_completed_before",
		prefix+"_completed_after",
		prefix+"_time_before",
		prefix+"_time_after",
		prefix+"_scan_time_before_seconds",
		prefix+"_scan_time_after_seconds",
		prefix+"_pair_correct",
		prefix+"_model",
	)
	return header
}

func TestBuildRowsPairedContract(t *testing.T) {
	rep := &Report{
		Schema: InputSchema{Mode: ModePaired, BeforeCol: "vulnerable_code", AfterCol: "fixed_code"},
		Header: []string{"cve_id", "severity", "vulnerable_code", "fixed_code"},
		Rows:   [][]string{{"CVE-1", "HIGH", "import os\nos.system(cmd)", "import os\nos.system(['ls'])"}},
		Engines: []*EngineReport{{
			Prefix: PrefixStatic,
			Rows: []EngineRow{{
				Before: Outcome{Classification: ClassificationVulnerable, Correct: true, Success: true, Completed: true, Seconds: 1.23456, Model: "aisai-sast"},
				After:  Outcome{Classification: ClassificationSafe, Correct: true, Success: true, Completed: true, Seconds: 0.5, Model: "aisai-sast"},
			}},
		}},
	}

	header, rows := BuildRows(rep)
	want := pairedHeader(PrefixStatic, rep.Header)
	if len(header) != len(want) {
		t.Fatalf("header has %d columns, want %d:\n%v", len(header), len(want), header)
	}
	for i := range want {
		if header[i] != want[i] {
			t.Errorf("column %d = %q, want %q", i, header[i], want[i])
		}
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	row := rows[0]
	// The original columns come first and are untouched.
	for i, v := range rep.Rows[0] {
		if row[i] != v {
			t.Errorf("original column %d = %q, want %q", i, row[i], v)
		}
	}
	index := map[string]int{}
	for i, c := range header {
		index[c] = i
	}
	if got := row[index[ColumnInputMode]]; got != "paired" {
		t.Errorf("%s = %q, want paired", ColumnInputMode, got)
	}
	if got := row[index[ColumnExpectedBefore]]; got != "vulnerable" {
		t.Errorf("%s = %q, want vulnerable", ColumnExpectedBefore, got)
	}
	if got := row[index[ColumnExpectedAfter]]; got != "safe" {
		t.Errorf("%s = %q, want safe", ColumnExpectedAfter, got)
	}
	if got := row[index[PrefixStatic+"_classification_before"]]; got != ClassificationVulnerable {
		t.Errorf("classification before = %q", got)
	}
	if got := row[index[PrefixStatic+"_classification_after"]]; got != ClassificationSafe {
		t.Errorf("classification after = %q", got)
	}
	// Booleans must be spelled the way pandas reads them back as booleans.
	for _, col := range []string{
		PrefixStatic + "_correct_before",
		PrefixStatic + "_correct_after",
		PrefixStatic + "_success_before",
		PrefixStatic + "_success_after",
		PrefixStatic + "_completed_before",
		PrefixStatic + "_completed_after",
		PrefixStatic + "_pair_correct",
	} {
		if got := row[index[col]]; got != "True" {
			t.Errorf("%s = %q, want True", col, got)
		}
	}
	// Timings are rounded to three decimals.
	if got := row[index[PrefixStatic+"_time_before"]]; got != "1.235" {
		t.Errorf("time before = %q, want 1.235", got)
	}
	if got := row[index[PrefixStatic+"_scan_time_before_seconds"]]; got != "1.235" {
		t.Errorf("scan time before = %q, want 1.235", got)
	}
	if got := row[index[PrefixStatic+"_model"]]; got != "aisai-sast" {
		t.Errorf("model = %q", got)
	}
}

func TestBuildRowsSingleContract(t *testing.T) {
	rep := &Report{
		Schema:         InputSchema{Mode: ModeSingle, CodeCol: "code_sample", LabelCol: "label"},
		Header:         []string{"code_sample", "label"},
		Rows:           [][]string{{"import os\nos.system(cmd)", "vulnerable"}},
		ExpectedLabels: []string{ClassificationVulnerable},
		Engines: []*EngineReport{{
			Prefix: PrefixLLM,
			Rows: []EngineRow{{
				Single: Outcome{Classification: ClassificationVulnerable, Correct: true, Success: true, Completed: true, Seconds: 0.25, Model: "test-model"},
			}},
		}},
	}

	header, rows := BuildRows(rep)
	index := map[string]int{}
	for i, c := range header {
		index[c] = i
	}
	for _, col := range []string{
		PrefixLLM + "_classification",
		PrefixLLM + "_correct",
		PrefixLLM + "_success",
		PrefixLLM + "_completed",
		PrefixLLM + "_time",
		PrefixLLM + "_scan_time_seconds",
		PrefixLLM + "_expected_label",
		PrefixLLM + "_code_column",
		PrefixLLM + "_label_column",
		PrefixLLM + "_model",
		ColumnExpectedLabel,
	} {
		if _, ok := index[col]; !ok {
			t.Errorf("the single-mode header is missing %q", col)
		}
	}
	row := rows[0]
	if got := row[index[PrefixLLM+"_classification"]]; got != ClassificationVulnerable {
		t.Errorf("classification = %q", got)
	}
	if got := row[index[PrefixLLM+"_expected_label"]]; got != ClassificationVulnerable {
		t.Errorf("expected label = %q", got)
	}
	if got := row[index[PrefixLLM+"_code_column"]]; got != "code_sample" {
		t.Errorf("code column = %q", got)
	}
	if got := row[index[PrefixLLM+"_label_column"]]; got != "label" {
		t.Errorf("label column = %q", got)
	}
	if got := row[index[ColumnExpectedLabel]]; got != ClassificationVulnerable {
		t.Errorf("%s = %q", ColumnExpectedLabel, got)
	}
}

func TestEngineOrderPutsLLMFirst(t *testing.T) {
	opts := Options{
		Engines: "both",
		DryRun:  true,
	}
	engines, err := selectEngines(opts)
	if err != nil {
		t.Fatalf("select engines: %v", err)
	}
	if len(engines) != 2 {
		t.Fatalf("got %d engines, want 2", len(engines))
	}
	if engines[0].Prefix() != PrefixLLM {
		t.Errorf("the first engine is %q, want %q", engines[0].Prefix(), PrefixLLM)
	}
	if engines[1].Prefix() != PrefixStatic {
		t.Errorf("the second engine is %q, want %q", engines[1].Prefix(), PrefixStatic)
	}
}

func TestWriteReportKeepsInputColumns(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.csv")
	if err := os.WriteFile(in, []byte(sampleInput), 0o600); err != nil {
		t.Fatalf("write input: %v", err)
	}
	out := filepath.Join(dir, "nested", "out.csv")

	rep, err := Run(context.Background(), Options{
		Input:   in,
		Engines: "bandit",
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(rep.Rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rep.Rows))
	}
	if err := WriteReport(out, rep); err != nil {
		t.Fatalf("write report: %v", err)
	}

	header, rows := readCSV(t, out)
	for _, col := range []string{"cve_id", "severity", "vulnerable_code", "fixed_code"} {
		found := false
		for _, c := range header {
			if c == col {
				found = true
			}
		}
		if !found {
			t.Errorf("the output is missing the input column %q", col)
		}
	}
	if len(rows) != 2 {
		t.Fatalf("got %d data rows, want 2", len(rows))
	}
	index := map[string]int{}
	for i, c := range header {
		index[c] = i
	}
	if got := rows[0][index[ColumnInputMode]]; got != "paired" {
		t.Errorf("%s = %q", ColumnInputMode, got)
	}
	if got := rows[0][index[ColumnExpectedBefore]]; got != "vulnerable" {
		t.Errorf("%s = %q", ColumnExpectedBefore, got)
	}
	if got := rows[0][index[ColumnExpectedAfter]]; got != "safe" {
		t.Errorf("%s = %q", ColumnExpectedAfter, got)
	}
}

func TestRunDryRunWritesEveryColumn(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.csv")
	if err := os.WriteFile(in, []byte(sampleInput), 0o600); err != nil {
		t.Fatalf("write input: %v", err)
	}
	out := filepath.Join(dir, "out.csv")

	rep, err := RunAndWrite(context.Background(), Options{
		Input:   in,
		Out:     out,
		Engines: "both",
		DryRun:  true,
		Model:   "stub-model",
	})
	if err != nil {
		t.Fatalf("run and write: %v", err)
	}
	header, rows := readCSV(t, out)

	// The llm columns must come before the bandit columns so that the
	// notebook's "startswith("llm_")" prefix pick resolves to the llm engine.
	llmAt, banditAt := -1, -1
	for i, c := range header {
		if llmAt < 0 && strings.HasPrefix(c, PrefixLLM+"_") {
			llmAt = i
		}
		if banditAt < 0 && strings.HasPrefix(c, PrefixStatic+"_") {
			banditAt = i
		}
	}
	if llmAt < 0 || banditAt < 0 {
		t.Fatalf("the output is missing engine columns: %v", header)
	}
	if llmAt > banditAt {
		t.Errorf("the llm columns come after the bandit columns: %v", header)
	}
	for _, col := range pairedHeader(PrefixLLM, []string{"cve_id", "severity", "vulnerable_code", "fixed_code"}) {
		found := false
		for _, c := range header {
			if c == col {
				found = true
			}
		}
		if !found {
			t.Errorf("the output is missing the column %q", col)
		}
	}
	if len(rows) != 2 {
		t.Fatalf("got %d data rows, want 2", len(rows))
	}
	if len(rep.Engines) != 2 {
		t.Fatalf("got %d engines, want 2", len(rep.Engines))
	}
}

func TestRunAppliesTheLimit(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.csv")
	if err := os.WriteFile(in, []byte(sampleInput), 0o600); err != nil {
		t.Fatalf("write input: %v", err)
	}
	rep, err := Run(context.Background(), Options{Input: in, Engines: "bandit", Limit: 1})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(rep.Rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rep.Rows))
	}
}

func TestStaticEngineClassifies(t *testing.T) {
	eng := NewStaticEngine(SeverityPolicy)

	// The rule engine has a rule for subprocess calls with shell=True; it
	// has no rule for os.system.
	vulnerable := "import subprocess\nsubprocess.run(cmd, shell=True)\n"
	out := eng.Classify(context.Background(), vulnerable)
	if out.Classification != ClassificationVulnerable {
		t.Errorf("classification = %q, want vulnerable", out.Classification)
	}
	if !out.Completed {
		t.Error("the run must be completed")
	}

	safe := "import subprocess\nsubprocess.run(['ls', '-l'])\n"
	out = eng.Classify(context.Background(), safe)
	if out.Classification != ClassificationSafe {
		t.Errorf("classification = %q, want safe", out.Classification)
	}
	if !out.Completed {
		t.Error("the run must be completed")
	}
}

func TestStaticEngineEmptySnippet(t *testing.T) {
	eng := NewStaticEngine(SeverityPolicy)
	out := eng.Classify(context.Background(), "   \n")
	if out.Classification != ClassificationSafe {
		t.Errorf("classification = %q, want safe", out.Classification)
	}
	if !out.Completed {
		t.Error("the run must be completed")
	}
}

// fakeProvider returns canned completions, like the test of internal/scan.
type fakeProvider struct {
	completions []*llm.Completion
	errs        []error
	err         error
}

func (f *fakeProvider) ID() string   { return "fake" }
func (f *fakeProvider) Kind() string { return "fake" }

func (f *fakeProvider) Chat(ctx context.Context, req llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	ch := make(chan llm.StreamChunk)
	close(ch)
	return ch, nil
}

func (f *fakeProvider) Complete(ctx context.Context, req llm.CompletionRequest) (*llm.Completion, error) {
	if len(f.errs) > 0 {
		err := f.errs[0]
		f.errs = f.errs[1:]
		if err != nil {
			return nil, err
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	if len(f.completions) == 0 {
		return &llm.Completion{JSON: map[string]any{}}, nil
	}
	c := f.completions[0]
	if len(f.completions) > 1 {
		f.completions = f.completions[1:]
	}
	return c, nil
}

func (f *fakeProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	return nil, nil
}
func (f *fakeProvider) Models(ctx context.Context) ([]string, error) { return nil, nil }
func (f *fakeProvider) Health(ctx context.Context) error             { return nil }

// detection is a detection reply with one candidate finding.
func detection() *llm.Completion {
	return &llm.Completion{JSON: map[string]any{
		"abstained": false,
		"findings": []any{map[string]any{
			"cwe_id": "CWE-78", "severity": "high", "confidence": 0.9,
			"start_line": 2, "end_line": 2, "message": "command injection",
		}},
	}}
}

func TestLLMEngineVerifiedFindingIsVulnerable(t *testing.T) {
	provider := &fakeProvider{completions: []*llm.Completion{
		detection(),
		{JSON: map[string]any{"verdict": "confirmed", "reason": "yes", "confidence": 0.9}},
	}}
	eng := NewLLMEngine(provider, "test-model", 8192, 0)
	out := eng.Classify(context.Background(), "import os\nos.system(cmd)\n")
	if out.Classification != ClassificationVulnerable {
		t.Errorf("classification = %q, want vulnerable", out.Classification)
	}
	if !out.Completed {
		t.Error("the run must be completed")
	}
}

func TestLLMEngineAbstentionIsSafe(t *testing.T) {
	provider := &fakeProvider{completions: []*llm.Completion{
		{JSON: map[string]any{"abstained": true, "findings": []any{}}},
	}}
	eng := NewLLMEngine(provider, "test-model", 8192, 0)
	out := eng.Classify(context.Background(), "import os\nos.system(cmd)\n")
	if out.Classification != ClassificationSafe {
		t.Errorf("classification = %q, want safe", out.Classification)
	}
	if !out.Completed {
		t.Error("an abstention is a completed run")
	}
}

func TestLLMEngineDroppedCandidateIsSafe(t *testing.T) {
	provider := &fakeProvider{completions: []*llm.Completion{
		detection(),
		{JSON: map[string]any{"verdict": "false_positive", "reason": "no", "confidence": 0.9}},
	}}
	eng := NewLLMEngine(provider, "test-model", 8192, 0)
	out := eng.Classify(context.Background(), "import os\nos.system(cmd)\n")
	if out.Classification != ClassificationSafe {
		t.Errorf("classification = %q, want safe", out.Classification)
	}
	if !out.Completed {
		t.Error("a dropped candidate is a completed run")
	}
}

func TestLLMEngineHardErrorIsUnknown(t *testing.T) {
	provider := &fakeProvider{err: errors.New("the model is unreachable")}
	eng := NewLLMEngine(provider, "test-model", 8192, 0)
	out := eng.Classify(context.Background(), "import os\nos.system(cmd)\n")
	if out.Classification != ClassificationUnknown {
		t.Errorf("classification = %q, want unknown", out.Classification)
	}
	if out.Completed {
		t.Error("a hard error must not be a completed run")
	}
	if out.Error == "" {
		t.Error("a failed run must record the reason")
	}
}

func TestLLMEngineVerificationErrorIsUnknown(t *testing.T) {
	// The detection succeeds, the verification fails: the run did not
	// complete, which is not the same as an abstention.
	provider := &fakeProvider{
		completions: []*llm.Completion{detection()},
		errs:        []error{nil, errors.New("the model is unreachable")},
	}
	eng := NewLLMEngine(provider, "test-model", 8192, 0)
	out := eng.Classify(context.Background(), "import os\nos.system(cmd)\n")
	if out.Classification != ClassificationUnknown {
		t.Errorf("classification = %q, want unknown", out.Classification)
	}
	if out.Completed {
		t.Error("a verification failure must not be a completed run")
	}
}

func TestBaselines(t *testing.T) {
	cases := []struct {
		name string
		code string
		want string
	}{
		{"always_vulnerable", "x = 1", ClassificationVulnerable},
		{"always_safe", "import os\nos.system(cmd)", ClassificationSafe},
		{"keyword", "import os\nos.system(cmd)", ClassificationVulnerable},
		// The keyword heuristic is deliberately naive: any os.system call
		// counts, however it is used.
		{"keyword", "import os\nos.system(['ls'])", ClassificationVulnerable},
		{"keyword", "eval(user_input)", ClassificationVulnerable},
		{"keyword", "exec(payload)", ClassificationVulnerable},
		{"keyword", "data = pickle.loads(blob)", ClassificationVulnerable},
		{"keyword", "subprocess.run(cmd, shell=True)", ClassificationVulnerable},
		{"keyword", "yaml.load(blob)", ClassificationVulnerable},
		{"keyword", "yaml.load(blob, Loader=yaml.SafeLoader)", ClassificationSafe},
	}
	for _, tc := range cases {
		eng, err := NewBaselineEngine(tc.name)
		if err != nil {
			t.Fatalf("baseline %s: %v", tc.name, err)
		}
		out := eng.Classify(context.Background(), tc.code)
		if out.Classification != tc.want {
			t.Errorf("%s on %q = %q, want %q", tc.name, tc.code, out.Classification, tc.want)
		}
		if !out.Completed {
			t.Errorf("%s must be a completed run", tc.name)
		}
		if !strings.HasPrefix(eng.Model(), "baseline:") {
			t.Errorf("the model column of a baseline must say so, got %q", eng.Model())
		}
		if eng.Prefix() != PrefixLLM {
			t.Errorf("a baseline is written under the %q prefix, got %q", PrefixLLM, eng.Prefix())
		}
	}
}

func TestUnknownBaseline(t *testing.T) {
	if _, err := NewBaselineEngine("nope"); err == nil {
		t.Fatal("expected an error for an unknown baseline")
	}
}

func TestNormalizeLabel(t *testing.T) {
	cases := map[string]string{
		"true":       ClassificationVulnerable,
		"1":          ClassificationVulnerable,
		"vuln":       ClassificationVulnerable,
		"false":      ClassificationSafe,
		"0":          ClassificationSafe,
		"patched":    ClassificationSafe,
		"vulnerable": ClassificationVulnerable,
		"":           ClassificationUnknown,
	}
	for in, want := range cases {
		if got := NormalizeLabel(in); got != want {
			t.Errorf("NormalizeLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestCorpusIsReadable proves the harness can read the real corpus.
func TestCorpusIsReadable(t *testing.T) {
	if _, err := os.Stat(corpusPath); err != nil {
		t.Skipf("the corpus is not available: %v", err)
	}
	table, err := ReadTable(corpusPath)
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	schema, err := DetectSchema(table.Header, "vulnerable_code", "fixed_code", "", "")
	if err != nil {
		t.Fatalf("detect schema: %v", err)
	}
	if schema.Mode != ModePaired {
		t.Errorf("mode = %q, want paired", schema.Mode)
	}
	if len(table.Rows) != 1551 {
		t.Errorf("the corpus has %d rows, want 1551", len(table.Rows))
	}
}
