// Package external runs the AISAI detection engines against external
// vulnerability benchmarks.
//
// The package exists for two reasons.
//
// First, the internal corpus (bench/data1551redacted.csv) is a paired
// snippet corpus: it asks "is this snippet vulnerable?". The external
// benchmarks ask a different and harder question: "given a whole repository,
// does the tool find the vulnerability *where it is*?" That requires file paths,
// line numbers and CWE identifiers, not a binary verdict.
//
// Second, the external corpora are not vendored into this repository. They are
// fetched at benchmark time into a cache directory, with a pinned URL and a
// recorded SHA-256 digest, so that a reported number can be tied to the exact
// bytes it was measured on. Nothing is downloaded implicitly and silently: the
// caller asks for a corpus by name.
//
// The detectors here are the engines of the product, not reimplementations of
// them: the static detector calls internal/sast, the same rule engine the
// application scans packages with, and the LLM detector calls internal/scan,
// the same detect-then-verify pipeline.
//
// The matching semantics of each corpus are implemented as pure functions over
// in-memory records, so that they can be tested without the corpus:
//
//   - RealVuln: the file path must match, the CWE must be in the ground-truth
//     entry's acceptable_cwes set, and the reported line must be within the
//     entry's line range widened by 10 lines on each side.
//   - OWASP BenchmarkPython: the file path must match the test case, and the
//     reported line must be one of the lines the case expects (or, for the
//     file-level layout, the finding must fall inside the test file).
package external

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/tlmcguire/aisai/internal/ast"
	"github.com/tlmcguire/aisai/internal/llm"
	"github.com/tlmcguire/aisai/internal/model"
	"github.com/tlmcguire/aisai/internal/sast"
	"github.com/tlmcguire/aisai/internal/scan"
)

// Finding is one detection produced by a detector.
//
// It carries the three coordinates every external corpus matches on: the file
// path, the line and the CWE.
type Finding struct {
	// Path is the workspace-relative path of the file the finding is in.
	Path string
	// Line is the 1-based line the finding starts at. Zero means the
	// detector did not report a line.
	Line int
	// CWE is the CWE identifier of the finding, "CWE-78" style. It is
	// empty when the detector did not report one.
	CWE string
	// Rule is the identifier of the rule or prompt that produced the
	// finding.
	Rule string
	// Severity is the severity the detector reported.
	Severity string
	// Message is the human-readable description of the finding.
	Message string
}

// Detector produces the findings of one engine for one source file.
//
// The interface is deliberately per-file: the external corpora are
// repositories or directories of whole files, and the ground truth identifies
// a finding by file path and line.
type Detector interface {
	// Name identifies the engine in the report.
	Name() string
	// Detect returns the findings of one file. The path is the
	// corpus-relative path of the file and is used for matching; the
	// content is the source text.
	Detect(ctx context.Context, path string, content string) ([]Finding, error)
}

// StaticDetector scans a file with the built-in rule engine.
//
// It calls internal/sast directly: the same engine, with the same rules and
// the same severity policy, that the application scans packages with. The
// benchmark measures the product, not a look-alike of it.
type StaticDetector struct {
	// engine is the rule engine.
	engine *sast.Engine
	// minSeverity is the severity threshold applied to the findings.
	minSeverity model.Severity
}

// NewStaticDetector returns a detector that counts findings at or above
// minSeverity.
//
// The application stores every finding the rule engine produces and filters by
// severity at read time only, so the default policy of the benchmark is
// model.SeverityLow: count everything the engine reports.
func NewStaticDetector(minSeverity model.Severity) *StaticDetector {
	return &StaticDetector{engine: sast.NewEngine(), minSeverity: minSeverity}
}

// Name identifies the detector in the report.
func (d *StaticDetector) Name() string {
	return fmt.Sprintf("aisai-sast(min-severity=%s)", d.minSeverity)
}

// Detect scans one file and returns the findings of the rule engine.
//
// A file that does not parse is still scanned: Tree-sitter recovers what it
// can, and the application does the same.
func (d *StaticDetector) Detect(ctx context.Context, path string, content string) ([]Finding, error) {
	if strings.TrimSpace(content) == "" {
		return nil, nil
	}
	file, err := ast.ParseString(path, content)
	if file == nil {
		if err == nil {
			err = fmt.Errorf("the file could not be parsed")
		}
		return nil, fmt.Errorf("external: parse %s: %w", path, err)
	}
	var out []Finding
	for _, f := range d.engine.Scan([]*ast.File{file}) {
		if !f.Severity.AtLeast(d.minSeverity) {
			continue
		}
		out = append(out, Finding{
			Path:     f.File,
			Line:     f.StartLine,
			CWE:      f.CWE,
			Rule:     f.Rule,
			Severity: string(f.Severity),
			Message:  f.Message,
		})
	}
	return out, nil
}

// LLMTDetector scans a file with the LLM detect-then-verify pipeline.
//
// It calls internal/scan directly, the same pipeline the application scans
// packages with. The verification stage is kept: a candidate the verifier
// rejects as a false positive is dropped, which is what keeps the
// false-positive rate on the trap files of the corpora low.
//
// The application frames a chunk with its absolute file lines and asks the
// model for "the exact line numbers within the snippet". This detector
// reframes the chunk so that the numbers the model reports are relative to
// the chunk, and maps them back to absolute file lines itself. The reason is
// the external corpora: their ground truth identifies a finding by its
// absolute line in a file, and a chunk-relative number would never match.
type LLMTDetector struct {
	// detector runs the two-stage pipeline.
	detector *scan.Detector
	// model is the model name recorded in the report.
	model string
	// timeout bounds one file.
	timeout time.Duration
}

// NewLLMTDetector returns a detector driving the given provider.
func NewLLMTDetector(provider llm.Provider, modelName string, numCtx int, timeout time.Duration) *LLMTDetector {
	if numCtx <= 0 {
		numCtx = llm.DefaultNumCtx
	}
	if timeout <= 0 {
		timeout = scan.Timeout()
	}
	return &LLMTDetector{
		detector: scan.NewDetector(provider, modelName, numCtx),
		model:    modelName,
		timeout:  timeout,
	}
}

// Name identifies the detector in the report.
func (d *LLMTDetector) Name() string {
	if d.model == "" {
		return "aisai-llm(unknown)"
	}
	return "aisai-llm(" + d.model + ")"
}

// Detect runs the pipeline over every chunk of one file.
//
// The chunks of a file are disjoint, so a vulnerability is reported once. The
// whole-file chunk of a small file is skipped when the file has other chunks,
// so that no line is scanned twice.
func (d *LLMTDetector) Detect(ctx context.Context, path string, content string) ([]Finding, error) {
	if strings.TrimSpace(content) == "" {
		return nil, nil
	}
	file, err := ast.ParseString(path, content)
	if file == nil {
		if err == nil {
			err = fmt.Errorf("the file could not be parsed")
		}
		return nil, fmt.Errorf("external: parse %s: %w", path, err)
	}

	chunks := file.Chunks
	if len(chunks) > 1 {
		filtered := chunks[:0:0]
		for _, c := range chunks {
			if c.Type == model.ChunkFile {
				continue
			}
			filtered = append(filtered, c)
		}
		chunks = filtered
	}

	ctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()

	var out []Finding
	for _, c := range chunks {
		// The chunk is reframed so that the line numbers the model reports
		// are relative to it; the absolute line is restored below.
		rel := c
		rel.StartLine = 1
		rel.EndLine = strings.Count(c.Content, "\n") + 1

		candidates, err := d.detector.Detect(ctx, rel)
		if err != nil {
			return out, fmt.Errorf("external: detect %s: %w", path, err)
		}
		for _, f := range candidates {
			_, keep, err := d.detector.Verify(ctx, rel, f)
			if err != nil {
				return out, fmt.Errorf("external: verify %s: %w", path, err)
			}
			if !keep {
				continue
			}
			out = append(out, Finding{
				Path:     path,
				Line:     c.StartLine + f.StartLine - 1,
				CWE:      f.CWE,
				Rule:     f.Rule,
				Severity: string(f.Severity),
				Message:  f.Message,
			})
		}
	}
	return out, nil
}

// Counts is the confusion matrix of a matching run.
type Counts struct {
	// TP is the number of true positives.
	TP int
	// FP is the number of false positives.
	FP int
	// FN is the number of false negatives.
	FN int
	// TN is the number of true negatives.
	TN int
}

// Precision returns the fraction of the reported findings that are correct.
//
// A run without a single reported finding has no precision: it is reported as
// zero rather than as an undefined value, and the caller can see the empty
// numerator in the counts.
func (c Counts) Precision() float64 {
	if c.TP+c.FP == 0 {
		return 0
	}
	return float64(c.TP) / float64(c.TP+c.FP)
}

// Recall returns the fraction of the ground truth that was found.
func (c Counts) Recall() float64 {
	if c.TP+c.FN == 0 {
		return 0
	}
	return float64(c.TP) / float64(c.TP+c.FN)
}

// F1 returns the harmonic mean of the precision and the recall.
func (c Counts) F1() float64 {
	p, r := c.Precision(), c.Recall()
	if p+r == 0 {
		return 0
	}
	return 2 * p * r / (p + r)
}

// Total returns the number of decisions the counts cover.
func (c Counts) Total() int { return c.TP + c.FP + c.FN + c.TN }

// NormalizePath normalises a file path the way the external corpora do.
//
// The rules are the ones of the RealVuln scorer: backslashes become forward
// slashes, a leading "./" or "/" is stripped, and the result is relative to
// the root of the repository. The comparison is case sensitive, which is the
// semantics of a Linux filesystem.
func NormalizePath(path string) string {
	p := strings.ReplaceAll(path, "\\", "/")
	for strings.HasPrefix(p, "./") {
		p = p[2:]
	}
	p = strings.TrimPrefix(p, "/")
	return p
}

// NormalizeCWE normalises a CWE identifier to the "CWE-78" form.
//
// The corpora write the identifiers in that form; a detector that reports
// "cwe-78" or "78" still means the same weakness. An identifier that does not
// carry the "CWE-" prefix is returned unchanged when it is empty, so that a
// finding without a CWE never matches an acceptable_cwes set by accident.
func NormalizeCWE(cwe string) string {
	c := strings.ToUpper(strings.TrimSpace(cwe))
	if c == "" {
		return ""
	}
	if !strings.HasPrefix(c, "CWE-") {
		if _, err := strconv.Atoi(c); err == nil {
			return "CWE-" + c
		}
		return c
	}
	return c
}
