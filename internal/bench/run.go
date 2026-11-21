package bench

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/tlmcguire/aisai/internal/llm"
)

// Options configures a benchmark run.
type Options struct {
	// Input is the path of the input CSV.
	Input string
	// Out is the path of the output CSV.
	Out string
	// Engines selects the engines to run: "llm", "bandit" or "both".
	Engines string
	// Limit caps the number of rows that are evaluated. Zero means all rows.
	Limit int
	// BeforeCol is the name of the column holding the vulnerable snippet.
	BeforeCol string
	// AfterCol is the name of the column holding the fixed snippet.
	AfterCol string
	// CodeCol is the name of the column holding the snippet of a single
	// sample input.
	CodeCol string
	// LabelCol is the name of the column holding the ground truth of a
	// single sample input.
	LabelCol string
	// Model is the name of the LLM model. It is recorded in the model column.
	Model string
	// Concurrency is the number of workers. The LLM path is bound by the
	// HTTP round trip to the model, so a handful of workers keeps the
	// pipeline busy without overwhelming a local Ollama server.
	Concurrency int
	// ModelTag is embedded in the default output file name.
	ModelTag string
	// DryRun replaces the LLM engine with a stub that makes no model call.
	DryRun bool
	// Baseline selects a trivial baseline instead of a real engine. The known
	// names are "always_vulnerable", "always_safe" and "keyword".
	Baseline string
	// Provider is the LLM provider. It may be nil in dry-run and baseline
	// mode; the LLM engine needs one.
	Provider llm.Provider
	// NumCtx is the context window requested from the provider.
	NumCtx int
	// Timeout bounds a single LLM classification.
	Timeout time.Duration
}

// fileSHA256 returns the SHA-256 of a file, or the empty string when it
// cannot be read.
//
// A benchmark run has to be traceable to the exact bytes of the corpus, so
// the digest travels with the results. A corpus that cannot be read is a
// corpus that cannot be run, so the empty string is only a defensive value.
func fileSHA256(path string) string {
	if path == "" {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, f); err != nil {
		return ""
	}
	return hex.EncodeToString(digest.Sum(nil))
}

// Run evaluates the corpus with the selected engines and returns the report.
//
// The report is not written to disk; use RunAndWrite for that.
func Run(ctx context.Context, opts Options) (*Report, error) {
	table, err := ReadTable(opts.Input)
	if err != nil {
		return nil, err
	}
	schema, err := DetectSchema(table.Header, opts.BeforeCol, opts.AfterCol, opts.CodeCol, opts.LabelCol)
	if err != nil {
		return nil, err
	}
	if opts.Limit > 0 && opts.Limit < len(table.Rows) {
		table.Rows = table.Rows[:opts.Limit]
	}

	engines, err := selectEngines(opts)
	if err != nil {
		return nil, err
	}

	rep := &Report{
		Schema:      schema,
		Header:      table.Header,
		Rows:        table.Rows,
		InputPath:   opts.Input,
		InputSHA256: fileSHA256(opts.Input),
	}
	if schema.Mode == ModeSingle {
		rep.ExpectedLabels = make([]string, len(table.Rows))
		for i := range table.Rows {
			rep.ExpectedLabels[i] = NormalizeLabel(table.Cell(i, schema.LabelCol))
		}
	}

	jobs := buildJobs(schema, table)
	for _, eng := range engines {
		outcomes := runJobs(ctx, eng, jobs, opts.Concurrency)
		for i := range outcomes {
			if outcomes[i].Model == "" {
				outcomes[i].Model = eng.Model()
			}
		}
		er := &EngineReport{Prefix: eng.Prefix(), Rows: make([]EngineRow, len(table.Rows))}
		for i := range table.Rows {
			er.Rows[i] = assembleRow(schema, rep.ExpectedLabels, i, outcomes)
		}
		rep.Engines = append(rep.Engines, er)
	}
	return rep, nil
}

// RunAndWrite runs the benchmark and writes the report as CSV at opts.Out.
func RunAndWrite(ctx context.Context, opts Options) (*Report, error) {
	rep, err := Run(ctx, opts)
	if err != nil {
		return nil, err
	}
	if opts.Out == "" {
		return rep, fmt.Errorf("bench: no output path given")
	}
	if err := WriteReport(opts.Out, rep); err != nil {
		return rep, err
	}
	return rep, nil
}

// selectEngines builds the engine list for the requested mode.
//
// The llm engine comes first so that a "both" run writes the llm_ columns
// before the bandit_ columns, which is what the notebooks assume when they
// pick their metric prefix.
func selectEngines(opts Options) ([]Engine, error) {
	if opts.Baseline != "" {
		eng, err := NewBaselineEngine(opts.Baseline)
		if err != nil {
			return nil, err
		}
		return []Engine{eng}, nil
	}
	switch strings.ToLower(strings.TrimSpace(opts.Engines)) {
	case "", "both":
		llmEng, err := newLLMEngine(opts)
		if err != nil {
			return nil, err
		}
		return []Engine{llmEng, NewStaticEngine(SeverityPolicy)}, nil
	case "llm":
		llmEng, err := newLLMEngine(opts)
		if err != nil {
			return nil, err
		}
		return []Engine{llmEng}, nil
	case "bandit", "static", "sast":
		return []Engine{NewStaticEngine(SeverityPolicy)}, nil
	}
	return nil, fmt.Errorf("bench: unknown engine %q: want llm, bandit or both", opts.Engines)
}

// newLLMEngine returns the LLM engine, or the dry-run stub.
func newLLMEngine(opts Options) (Engine, error) {
	if opts.DryRun {
		return NewStubEngine(), nil
	}
	if opts.Provider == nil {
		return nil, fmt.Errorf("bench: no LLM provider configured: use --dry-run to run without a model")
	}
	return NewLLMEngine(opts.Provider, opts.Model, opts.NumCtx, opts.Timeout), nil
}

// job is one snippet to classify.
type job struct {
	// code is the snippet.
	code string
}

// buildJobs returns the snippets to classify, in the order the results are
// stored: two per row in paired mode, one per row in single mode.
func buildJobs(schema InputSchema, table *Table) []job {
	jobs := make([]job, 0, len(table.Rows)*2)
	for i := range table.Rows {
		if schema.Mode == ModePaired {
			jobs = append(jobs,
				job{code: table.Cell(i, schema.BeforeCol)},
				job{code: table.Cell(i, schema.AfterCol)},
			)
			continue
		}
		jobs = append(jobs, job{code: table.Cell(i, schema.CodeCol)})
	}
	return jobs
}

// assembleRow turns the raw outcomes of a row into the reported values.
func assembleRow(schema InputSchema, expectedLabels []string, row int, outcomes []Outcome) EngineRow {
	var er EngineRow
	if schema.Mode == ModePaired {
		before := outcomes[2*row]
		after := outcomes[2*row+1]
		before.Correct = before.Classification == ClassificationVulnerable
		before.Success = before.Correct
		after.Correct = after.Classification == ClassificationSafe
		after.Success = after.Correct
		er.Before = before
		er.After = after
		return er
	}
	single := outcomes[row]
	expected := ""
	if row < len(expectedLabels) {
		expected = expectedLabels[row]
	}
	single.Correct = single.Classification == expected
	single.Success = single.Correct
	er.Single = single
	return er
}

// runJobs classifies every job with the given engine.
//
// The work is spread over a pool of workers: the LLM engine waits on the
// model for every snippet, the static engine is CPU bound but cheap.
func runJobs(ctx context.Context, eng Engine, jobs []job, concurrency int) []Outcome {
	outcomes := make([]Outcome, len(jobs))
	if concurrency < 1 {
		concurrency = 1
	}
	if len(jobs) == 0 {
		return outcomes
	}

	ch := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range ch {
				outcomes[i] = eng.Classify(ctx, jobs[i].code)
			}
		}()
	}

	cancelled := false
	for i := range jobs {
		select {
		case <-ctx.Done():
			cancelled = true
		case ch <- i:
			continue
		}
		break
	}
	close(ch)
	wg.Wait()

	if cancelled {
		// The run was cut short: the rows that were never evaluated are
		// failures, not answers.
		err := ctx.Err()
		for i := range outcomes {
			if outcomes[i].Classification == "" {
				outcomes[i] = Outcome{
					Classification: ClassificationUnknown,
					Completed:      false,
					Error:          err.Error(),
				}
			}
		}
	}
	return outcomes
}
