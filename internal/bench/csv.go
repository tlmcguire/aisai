// Package bench measures the AISAI detection engines against a labelled
// corpus and writes the results as a CSV that the Python notebooks under
// bench/notebooks/ can consume unmodified.
//
// The package deliberately calls the same engine code the application uses:
//
//   - the static engine is internal/sast, the rule engine internal/audit
//     scans packages with;
//   - the LLM engine is internal/scan, the detect→verify pipeline the
//     application scans packages with.
//
// Nothing is shelled out to Bandit or to the legacy Python code. That is the
// point of the rewrite: a severity filter can no longer diverge between what
// the app shows and what the benchmark measures.
//
// The CSV schema is the one the notebooks expect. See the "Output CSV
// contract" below and the notebook helpers _detect_benchmark_input_schema,
// _benchmark_result_mode and _build_benchmark_report_data.
package bench

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Mode is the layout of the benchmark input.
type Mode string

const (
	// ModePaired is the paired layout: one vulnerable and one fixed snippet
	// per row. The corpus in bench/data1551redacted.csv uses this layout.
	ModePaired Mode = "paired"
	// ModeSingle is the single layout: one snippet and one label per row.
	ModeSingle Mode = "single"
)

// Column names that are shared by every engine.
const (
	// ColumnInputMode records the detected input layout, "paired" or "single".
	ColumnInputMode = "benchmark_input_mode"
	// ColumnExpectedBefore is the ground-truth label of the "before" snippet.
	ColumnExpectedBefore = "expected_before"
	// ColumnExpectedAfter is the ground-truth label of the "after" snippet.
	ColumnExpectedAfter = "expected_after"
	// ColumnExpectedLabel is the ground-truth label of a single-mode row.
	ColumnExpectedLabel = "expected_label"
)

// The canonical classifications written to the CSV.
const (
	// ClassificationVulnerable means the engine reported a vulnerability.
	ClassificationVulnerable = "vulnerable"
	// ClassificationSafe means the engine reported no vulnerability.
	ClassificationSafe = "safe"
	// ClassificationUnknown means the engine could not answer. The notebooks
	// treat it as its own confusion-matrix column.
	ClassificationUnknown = "unknown"
)

// The column prefixes the notebooks look for.
//
// The two CSV notebooks pick their metric prefix differently, and both
// orders are deliberate:
//
//   - llmBenchmarkCSV.ipynb checks "llm_" first, so it summarises the LLM
//     engine.
//   - staticBenchmarkCSV.ipynb checks "bandit_" first, so it summarises the
//     static engine.
//
// A combined "both" CSV therefore satisfies both notebooks: each one finds
// its own engine's columns first. Writing llm_ before bandit_ keeps the
// LLM notebook's "first match wins" rule satisfied while still leaving the
// bandit_ columns present.
const (
	// PrefixLLM is the prefix of the LLM engine columns.
	PrefixLLM = "llm"
	// PrefixStatic is the prefix of the static engine columns. The name is
	// "bandit" because that is the prefix the notebooks look for; the values
	// are produced by internal/sast, not by Bandit.
	PrefixStatic = "bandit"
)

// InputSchema describes the detected layout of a benchmark input file.
type InputSchema struct {
	// Mode is the detected layout.
	Mode Mode
	// BeforeCol is the column holding the vulnerable snippet, paired mode.
	BeforeCol string
	// AfterCol is the column holding the fixed snippet, paired mode.
	AfterCol string
	// CodeCol is the column holding the snippet, single mode.
	CodeCol string
	// LabelCol is the column holding the ground truth, single mode.
	LabelCol string
}

// codeCandidates mirrors the notebook's candidate list for the snippet column.
var codeCandidates = []string{"code_sample", "code", "sample_code", "source_code", "snippet", "content"}

// labelCandidates mirrors the notebook's candidate list for the label column.
var labelCandidates = []string{"sample_label", "label", "ground_truth", "target", "class", "vulnerability_label", "status"}

// Default column names of the benchmark input. They mirror the defaults of
// the notebooks.
const (
	// DefaultBeforeCol holds the vulnerable snippet of a paired row.
	DefaultBeforeCol = "vulnerable_code"
	// DefaultAfterCol holds the fixed snippet of a paired row.
	DefaultAfterCol = "fixed_code"
)

// DetectSchema mirrors the notebook's _detect_benchmark_input_schema.
//
// A file that has both the before and the after column is paired; otherwise a
// code column and a label column are looked up, preferring the explicit
// overrides over the notebook's candidate names.
//
// Empty column names fall back to the notebook defaults, vulnerable_code and
// fixed_code.
func DetectSchema(header []string, beforeCol, afterCol, codeCol, labelCol string) (InputSchema, error) {
	if beforeCol == "" {
		beforeCol = DefaultBeforeCol
	}
	if afterCol == "" {
		afterCol = DefaultAfterCol
	}
	columns := make(map[string]bool, len(header))
	for _, c := range header {
		columns[c] = true
	}
	if beforeCol != "" && afterCol != "" && columns[beforeCol] && columns[afterCol] {
		return InputSchema{Mode: ModePaired, BeforeCol: beforeCol, AfterCol: afterCol}, nil
	}
	resolvedCode := firstPresent(columns, codeCol, codeCandidates)
	resolvedLabel := firstPresent(columns, labelCol, labelCandidates)
	if resolvedCode != "" && resolvedLabel != "" {
		return InputSchema{Mode: ModeSingle, CodeCol: resolvedCode, LabelCol: resolvedLabel}, nil
	}
	return InputSchema{}, fmt.Errorf(
		"bench: could not detect benchmark schema: expected paired columns (%q, %q) or a single code/label pair",
		beforeCol, afterCol)
}

// firstPresent returns the first candidate that exists in columns.
func firstPresent(columns map[string]bool, override string, candidates []string) string {
	if override != "" && columns[override] {
		return override
	}
	for _, c := range candidates {
		if columns[c] {
			return c
		}
	}
	return ""
}

// Table is the input CSV: its header and its raw rows.
type Table struct {
	// Header holds the column names in file order.
	Header []string
	// Rows holds the raw cell values in file order.
	Rows [][]string
	// index maps a column name to its position in a row.
	index map[string]int
}

// ReadTable reads a benchmark input CSV.
//
// The corpus has quoted cells with embedded newlines, so the reader is a
// plain RFC 4180 reader with lazy quoting.
func ReadTable(path string) (*Table, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("bench: read %s: %w", path, err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.LazyQuotes = true
	// The corpus is well formed, but a ragged input must not stop a run:
	// short rows are padded and long rows are kept as they are.
	r.FieldsPerRecord = -1
	records, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("bench: parse %s: %w", path, err)
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("bench: %s is empty", path)
	}
	t := &Table{Header: records[0], Rows: records[1:], index: make(map[string]int, len(records[0]))}
	for i, c := range t.Header {
		if _, dup := t.index[c]; dup {
			continue
		}
		t.index[c] = i
	}
	for i, row := range t.Rows {
		for len(row) < len(t.Header) {
			row = append(row, "")
		}
		t.Rows[i] = row
	}
	return t, nil
}

// Cell returns the value of a column in a row, or the empty string when the
// column or the row is missing.
func (t *Table) Cell(row int, column string) string {
	if t == nil || row < 0 || row >= len(t.Rows) {
		return ""
	}
	i, ok := t.index[column]
	if !ok {
		return ""
	}
	if i >= len(t.Rows[row]) {
		return ""
	}
	return t.Rows[row][i]
}

// Outcome is the result of classifying one snippet with one engine.
type Outcome struct {
	// Classification is "vulnerable", "safe" or "unknown".
	Classification string
	// Correct reports whether the classification matches the ground truth.
	Correct bool
	// Success mirrors the notebook's "success" column. The legacy runner
	// wrote the same value as "correct"; it is kept as a separate column
	// because the notebooks read it.
	Success bool
	// Completed reports whether the engine produced an answer at all. The
	// notebooks count the rows where it is not True as failed tests, so a
	// failure must be recorded as False and never as a missing column.
	Completed bool
	// Seconds is the wall-clock time the classification took.
	Seconds float64
	// Model identifies the engine or model that produced the outcome.
	Model string
	// Error explains a failed classification. It is empty when the engine
	// answered, including when it abstained.
	Error string
}

// EngineRow holds the outcomes of one engine for one input row.
type EngineRow struct {
	// Before is the outcome for the vulnerable snippet, paired mode.
	Before Outcome
	// After is the outcome for the fixed snippet, paired mode.
	After Outcome
	// Single is the outcome for the single snippet, single mode.
	Single Outcome
}

// EngineReport holds the results of one engine over every input row.
type EngineReport struct {
	// Prefix is the column prefix, "llm" or "bandit".
	Prefix string
	// Rows holds one EngineRow per input row, in input order.
	Rows []EngineRow
}

// Report is a complete benchmark run.
type Report struct {
	// Schema is the detected input layout.
	Schema InputSchema
	// Header is the original input header.
	Header []string
	// Rows holds the original input rows.
	Rows [][]string
	// ExpectedLabels holds the normalized ground-truth label of every row.
	// It is only filled in single mode.
	ExpectedLabels []string
	// Engines holds the per-engine results, in the order their columns are
	// written. The llm engine comes first so that the notebook's
	// "startswith("llm_")" prefix pick resolves to it.
	Engines []*EngineReport
	// InputPath is the path of the corpus the run was made from.
	InputPath string
	// InputSHA256 is the SHA-256 of the corpus file.
	//
	// It travels with the results so that a number can always be traced
	// back to the exact bytes that produced it.
	InputSHA256 string
}

// columnNames returns the columns an engine contributes, in write order.
//
// The "_time_" columns carry the timings the task contract names, and the
// "_scan_time_seconds" columns are the aliases the notebooks read for their
// timing statistics. Both hold the same rounded value.
func (e *EngineReport) columnNames(paired bool) []string {
	p := e.Prefix
	if paired {
		return []string{
			p + "_classification_before",
			p + "_classification_after",
			p + "_correct_before",
			p + "_correct_after",
			p + "_success_before",
			p + "_success_after",
			p + "_completed_before",
			p + "_completed_after",
			p + "_time_before",
			p + "_time_after",
			p + "_scan_time_before_seconds",
			p + "_scan_time_after_seconds",
			p + "_pair_correct",
			p + "_model",
		}
	}
	return []string{
		p + "_classification",
		p + "_correct",
		p + "_success",
		p + "_completed",
		p + "_time",
		p + "_scan_time_seconds",
		p + "_expected_label",
		p + "_code_column",
		p + "_label_column",
		p + "_model",
	}
}

// values returns the cell values an engine contributes for one row.
func (e *EngineReport) values(row int, paired bool, expectedLabel string) []string {
	out := make([]string, 0, 16)
	if paired {
		r := e.rowAt(row)
		b, a := r.Before, r.After
		out = append(out,
			b.Classification, a.Classification,
			boolText(b.Correct), boolText(a.Correct),
			boolText(b.Success), boolText(a.Success),
			boolText(b.Completed), boolText(a.Completed),
			formatSeconds(b.Seconds), formatSeconds(a.Seconds),
			formatSeconds(b.Seconds), formatSeconds(a.Seconds),
			boolText(b.Correct && a.Correct),
			rowModel(b, a),
		)
		return out
	}
	s := e.rowAt(row).Single
	out = append(out,
		s.Classification,
		boolText(s.Correct),
		boolText(s.Success),
		boolText(s.Completed),
		formatSeconds(s.Seconds),
		formatSeconds(s.Seconds),
		expectedLabel,
		"", // filled in by the caller, the code column name
		"", // filled in by the caller, the label column name
		rowModelSingle(s),
	)
	return out
}

// rowAt returns the engine row for an index, or the zero value when it is
// missing.
func (e *EngineReport) rowAt(row int) EngineRow {
	if row < 0 || row >= len(e.Rows) {
		return EngineRow{}
	}
	return e.Rows[row]
}

// rowModel returns the model name recorded for a paired row.
func rowModel(before, after Outcome) string {
	if before.Model != "" {
		return before.Model
	}
	if after.Model != "" {
		return after.Model
	}
	return "unknown"
}

// rowModelSingle returns the model name recorded for a single-mode row.
func rowModelSingle(o Outcome) string {
	if o.Model != "" {
		return o.Model
	}
	return "unknown"
}

// BuildRows renders a report as a CSV header and data rows.
//
// Every original input column is kept, so the per-vulnerability-family
// analysis of the notebooks, which reads vulnerability_type_cwe_ids and
// vulnerability_type_names, still works on the output.
func BuildRows(rep *Report) ([]string, [][]string) {
	paired := rep.Schema.Mode == ModePaired

	header := append([]string(nil), rep.Header...)
	header = append(header, ColumnInputMode)
	if paired {
		header = append(header, ColumnExpectedBefore, ColumnExpectedAfter)
	} else {
		header = append(header, ColumnExpectedLabel)
	}
	for _, e := range rep.Engines {
		header = append(header, e.columnNames(paired)...)
	}

	rows := make([][]string, 0, len(rep.Rows))
	for i, src := range rep.Rows {
		row := make([]string, 0, len(rep.Header)+16)
		row = append(row, src...)
		// A row longer than the header carries fields without a column
		// name; they are dropped so that every record has the same width.
		if len(row) > len(rep.Header) {
			row = row[:len(rep.Header)]
		}
		for len(row) < len(rep.Header) {
			row = append(row, "")
		}
		row = append(row, string(rep.Schema.Mode))
		expected := ""
		if !paired && i < len(rep.ExpectedLabels) {
			expected = rep.ExpectedLabels[i]
		}
		if paired {
			row = append(row, ClassificationVulnerable, ClassificationSafe)
		} else {
			row = append(row, expected)
		}
		for _, e := range rep.Engines {
			values := e.values(i, paired, expected)
			if !paired {
				// The code and label column names are properties of the
				// input, not of the row.
				values[len(values)-3] = rep.Schema.CodeCol
				values[len(values)-2] = rep.Schema.LabelCol
			}
			row = append(row, values...)
		}
		rows = append(rows, row)
	}
	return header, rows
}

// WriteReport writes a report as CSV at path, creating the parent directory
// when it does not exist.
func WriteReport(path string, rep *Report) error {
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("bench: create %s: %w", dir, err)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("bench: create %s: %w", path, err)
	}
	header, rows := BuildRows(rep)

	w := csv.NewWriter(f)
	if err := w.Write(header); err != nil {
		f.Close()
		return fmt.Errorf("bench: write %s: %w", path, err)
	}
	for _, row := range rows {
		if err := w.Write(row); err != nil {
			f.Close()
			return fmt.Errorf("bench: write %s: %w", path, err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		f.Close()
		return fmt.Errorf("bench: write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("bench: close %s: %w", path, err)
	}
	return nil
}

// boolText renders a boolean the way pandas reads it back as a boolean.
//
// The notebooks count the rows where a completed column is not True as failed
// tests, so the spelling matters: "True" and "False" are read as booleans,
// "true" and "1" are not.
func boolText(b bool) string {
	if b {
		return "True"
	}
	return "False"
}

// formatSeconds renders a duration with the three decimals the legacy runner
// wrote.
func formatSeconds(v float64) string {
	return strconv.FormatFloat(v, 'f', 3, 64)
}

// NormalizeLabel maps a raw ground-truth label onto the canonical labels.
//
// It mirrors the notebook's _normalize_benchmark_label so that a label column
// written in any of the spellings the legacy corpus used is understood.
func NormalizeLabel(value string) string {
	text := strings.ToLower(strings.TrimSpace(value))
	switch text {
	case "":
		return ClassificationUnknown
	case "vulnerable", "vuln", "unsafe", "malicious", "true", "1":
		return ClassificationVulnerable
	case "safe", "patched", "benign", "non-vulnerable", "nonvulnerable", "not vulnerable", "false", "0":
		return ClassificationSafe
	}
	return text
}
