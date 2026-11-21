package bench

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/tlmcguire/aisai/internal/ast"
	"github.com/tlmcguire/aisai/internal/llm"
	"github.com/tlmcguire/aisai/internal/model"
	"github.com/tlmcguire/aisai/internal/sast"
	"github.com/tlmcguire/aisai/internal/scan"
)

// SeverityPolicy is the severity threshold applied to the findings of the
// static engine.
//
// The policy is deliberately "count every finding the engine reports", and it
// is identical to what the application does:
//
//   - The application scans packages with internal/sast (internal/audit/
//     manager.go, Manager.Scan) and stores every finding the rule engine
//     produces. The optional "severity" query parameter of the findings API
//     filters at read time only; it never changes what the engine reports.
//   - The benchmark therefore applies exactly the same rule: a snippet is
//     "vulnerable" when the engine reports at least one finding.
//
// This is the fix for the divergence recorded as F§1.4 in
// NOTES_improvements.md: the legacy app ran Bandit with "-lll" (HIGH only)
// while its own benchmark accepted MEDIUM *and* HIGH, so the benchmark
// measured a configuration the product never shipped. The rewrite has one
// engine and one policy, and the benchmark calls that engine directly.
const SeverityPolicy = model.SeverityLow

// Engine classifies one code snippet.
type Engine interface {
	// Prefix is the column prefix the engine's results are written under.
	Prefix() string
	// Model identifies the engine or model in the model column.
	Model() string
	// Classify classifies one snippet.
	//
	// The returned Outcome always carries a classification. A Completed
	// outcome with the classification "safe" means the engine answered and
	// found nothing; a Completed outcome with the classification "unknown"
	// means the engine could not answer at all.
	Classify(ctx context.Context, code string) Outcome
}

// StaticEngine classifies a snippet with the built-in rule engine.
//
// It calls internal/sast directly, the same engine the application scans
// packages with. Nothing is shelled out to Bandit: the benchmark measures the
// product, not a look-alike.
type StaticEngine struct {
	// engine is the rule engine.
	engine *sast.Engine
	// minSeverity is the severity threshold applied to the findings.
	minSeverity model.Severity
}

// NewStaticEngine returns a static engine that counts findings at or above
// minSeverity. See SeverityPolicy for the threshold the application implies.
func NewStaticEngine(minSeverity model.Severity) *StaticEngine {
	return &StaticEngine{engine: sast.NewEngine(), minSeverity: minSeverity}
}

// Prefix returns the column prefix of the static engine.
func (e *StaticEngine) Prefix() string { return PrefixStatic }

// Model returns the identifier recorded in the model column.
func (e *StaticEngine) Model() string {
	return fmt.Sprintf("aisai-sast(min-severity=%s)", e.minSeverity)
}

// Classify scans the snippet and reports "vulnerable" when the engine
// produced at least one finding.
//
// A snippet that does not parse is still scanned: Tree-sitter recovers what
// it can and the application does the same, so a recoverable syntax error
// does not make the row fail. Only a snippet that cannot be parsed at all
// fails the row.
func (e *StaticEngine) Classify(ctx context.Context, code string) Outcome {
	start := time.Now()
	out := Outcome{Completed: true}

	if strings.TrimSpace(code) == "" {
		out.Classification = ClassificationSafe
		out.Seconds = time.Since(start).Seconds()
		return out
	}

	file, err := ast.ParseString("bench_snippet.py", code)
	if file == nil {
		out.Classification = ClassificationUnknown
		out.Completed = false
		if err != nil {
			out.Error = err.Error()
		} else {
			out.Error = "the snippet could not be parsed"
		}
		out.Seconds = time.Since(start).Seconds()
		return out
	}

	for _, f := range e.engine.Scan([]*ast.File{file}) {
		if f.Severity.AtLeast(e.minSeverity) {
			out.Classification = ClassificationVulnerable
			out.Seconds = time.Since(start).Seconds()
			return out
		}
	}
	out.Classification = ClassificationSafe
	out.Seconds = time.Since(start).Seconds()
	return out
}

// LLMEngine classifies a snippet with the LLM detect→verify pipeline.
//
// It calls internal/scan directly, the same pipeline the application scans
// packages with (internal/audit/manager.go, Manager.ScanLLM).
//
// The two outcomes of the pipeline are kept apart on purpose:
//
//   - An abstention, or a candidate the verifier rejected as a false
//     positive, is a completed run that found nothing: the classification is
//     "safe". A candidate that cannot be verified is dropped, not
//     downgraded, which is what keeps the false-positive rate on safe code
//     low.
//   - A hard error — the model is unreachable, the request timed out, the
//     answer did not conform to the schema — is a failed run: the
//     classification is "unknown" and Completed is false. Those rows are
//     counted as failed tests by the notebooks.
type LLMEngine struct {
	// detector runs the two-stage pipeline.
	detector *scan.Detector
	// model is the model name recorded in the model column.
	model string
	// timeout bounds a single classification.
	timeout time.Duration
}

// NewLLMEngine returns an LLM engine driving the given provider.
func NewLLMEngine(provider llm.Provider, modelName string, numCtx int, timeout time.Duration) *LLMEngine {
	if numCtx <= 0 {
		numCtx = llm.DefaultNumCtx
	}
	if timeout <= 0 {
		timeout = scan.Timeout()
	}
	return &LLMEngine{
		detector: scan.NewDetector(provider, modelName, numCtx),
		model:    modelName,
		timeout:  timeout,
	}
}

// Prefix returns the column prefix of the LLM engine.
func (e *LLMEngine) Prefix() string { return PrefixLLM }

// Model returns the identifier recorded in the model column.
func (e *LLMEngine) Model() string {
	if e.model == "" {
		return "unknown"
	}
	return e.model
}

// Classify runs the pipeline over one snippet.
//
// The stages are driven explicitly instead of through DetectAndVerify so
// that a verification failure is surfaced. DetectAndVerify keeps the
// candidate and records the failure in the verifier note, which is right for
// the application — the user still sees the finding — but a benchmark has to
// tell an abstention apart from a broken run.
func (e *LLMEngine) Classify(ctx context.Context, code string) Outcome {
	start := time.Now()
	out := Outcome{Completed: true}

	if strings.TrimSpace(code) == "" {
		out.Classification = ClassificationSafe
		out.Seconds = time.Since(start).Seconds()
		return out
	}

	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	chunk := snippetChunk(code)
	candidates, err := e.detector.Detect(ctx, chunk)
	if err != nil {
		out.Classification = ClassificationUnknown
		out.Completed = false
		out.Error = err.Error()
		out.Seconds = time.Since(start).Seconds()
		return out
	}

	kept := 0
	for _, c := range candidates {
		_, keep, err := e.detector.Verify(ctx, chunk, c)
		if err != nil {
			out.Classification = ClassificationUnknown
			out.Completed = false
			out.Error = err.Error()
			out.Seconds = time.Since(start).Seconds()
			return out
		}
		if keep {
			kept++
		}
	}

	if kept > 0 {
		out.Classification = ClassificationVulnerable
	} else {
		out.Classification = ClassificationSafe
	}
	out.Seconds = time.Since(start).Seconds()
	return out
}

// StubEngine is the deterministic stand-in used by --dry-run.
//
// It makes no network call: it classifies with the trivial keyword heuristic
// so that the whole pipeline — timing, completion, correctness, CSV schema —
// can be exercised without a model.
type StubEngine struct {
	// model is the identifier recorded in the model column.
	model string
}

// NewStubEngine returns a stub engine that makes no model call.
func NewStubEngine() *StubEngine {
	return &StubEngine{model: "dry-run-stub"}
}

// Prefix returns the column prefix of the stub engine.
func (e *StubEngine) Prefix() string { return PrefixLLM }

// Model returns the identifier recorded in the model column.
func (e *StubEngine) Model() string {
	if e.model == "" {
		return "dry-run-stub"
	}
	return e.model
}

// Classify classifies the snippet with the keyword heuristic.
func (e *StubEngine) Classify(ctx context.Context, code string) Outcome {
	out := Outcome{Completed: true}
	if keywordMatch(code) {
		out.Classification = ClassificationVulnerable
	} else {
		out.Classification = ClassificationSafe
	}
	return out
}

// BaselineEngine is one of the trivial baselines.
//
// The baselines run no model at all. They exist so that the benchmark can be
// compared against what a naive detector achieves: "always_vulnerable" is a
// deliberate null model that exposes whether the corpus is balanced, and
// "keyword" is a small heuristic over the snippet text.
//
// The baselines are written under the llm_ prefix because the notebooks only
// look for llm_ or bandit_ columns, and the bandit_ prefix belongs to the
// static engine. The model column carries "baseline:<name>" so that a
// baseline run can never be mistaken for a real engine run.
type BaselineEngine struct {
	// name is the baseline name.
	name string
	// detail describes the configuration of the baseline. It is written
	// into the model column of every result row, so that a result can never
	// be separated from the settings that produced it. The seed of a split
	// and the size of the split belong here: a baseline trained on a
	// different split is a different baseline.
	detail string
	// predict returns the classification for a snippet.
	predict func(string) string
}

// NewBaselineEngine returns the baseline with the given name.
//
// The known names are "always_vulnerable", "always_safe" and "keyword".
func NewBaselineEngine(name string) (*BaselineEngine, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "always_vulnerable":
		return &BaselineEngine{
			name:    "always_vulnerable",
			predict: func(string) string { return ClassificationVulnerable },
		}, nil
	case "always_safe":
		return &BaselineEngine{
			name:    "always_safe",
			predict: func(string) string { return ClassificationSafe },
		}, nil
	case "keyword":
		return &BaselineEngine{
			name: "keyword",
			predict: func(code string) string {
				if keywordMatch(code) {
					return ClassificationVulnerable
				}
				return ClassificationSafe
			},
		}, nil
	case "tfidf":
		// The TF-IDF baseline is trained on the training split of the
		// corpus, so it needs a trained model. The model is installed
		// with SetTFIDFModel, which TrainTFIDFBaseline does.
		model := CurrentTFIDFModel()
		if model == nil {
			return nil, fmt.Errorf(
				"bench: the tfidf baseline needs a trained model: call bench.TrainTFIDFBaseline first")
		}
		return &BaselineEngine{
			name:   "tfidf",
			detail: model.SplitDescription(),
			predict: func(code string) string {
				return model.ClassifyOutcome(code).Classification
			},
		}, nil
	}
	return nil, fmt.Errorf("bench: unknown baseline %q: want always_vulnerable, always_safe, keyword or tfidf", name)
}

// Prefix returns the column prefix of the baseline.
func (e *BaselineEngine) Prefix() string { return PrefixLLM }

// Model returns the identifier recorded in the model column.
//
// For a baseline it is "baseline:<name>", and for a baseline with a
// configuration — the TF-IDF one, whose result depends on the split it was
// trained on — the configuration follows, so that the seed and the size of
// the split travel with every result row.
func (e *BaselineEngine) Model() string {
	if e.detail == "" {
		return "baseline:" + e.name
	}
	return "baseline:" + e.name + ";" + e.detail
}

// Classify classifies the snippet with the baseline heuristic.
func (e *BaselineEngine) Classify(ctx context.Context, code string) Outcome {
	return Outcome{
		Classification: e.predict(code),
		Completed:      true,
	}
}

// keywordPatterns are the markers of the trivial keyword baseline.
var keywordPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\beval\s*\(`),
	regexp.MustCompile(`\bexec\s*\(`),
	regexp.MustCompile(`\bpickle\.loads?\s*\(`),
	regexp.MustCompile(`\bos\.system\s*\(`),
	regexp.MustCompile(`\bsubprocess\.[A-Za-z_]\w*\s*\([^)]*shell\s*=\s*True`),
}

// keywordMatch reports whether the snippet trips the keyword heuristic.
//
// The heuristic looks for eval(, exec(, pickle.loads, os.system, a
// subprocess call with shell=True and a yaml.load without SafeLoader.
func keywordMatch(code string) bool {
	for _, line := range strings.Split(code, "\n") {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "yaml.load(") && !strings.Contains(lower, "safeloader") {
			return true
		}
	}
	for _, re := range keywordPatterns {
		if re.MatchString(code) {
			return true
		}
	}
	return false
}

// snippetChunk wraps a code snippet in the chunk the scanner expects.
func snippetChunk(code string) model.Chunk {
	end := strings.Count(code, "\n") + 1
	return model.Chunk{
		ID:         "bench",
		File:       "bench_snippet.py",
		StartLine:  1,
		EndLine:    end,
		Type:       model.ChunkFile,
		SymbolPath: "bench_snippet",
		Content:    code,
	}
}
