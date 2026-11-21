// Command aisai-bench-external measures the AISAI detection engines against
// the external vulnerability benchmarks.
//
// The internal corpus of the project asks a yes-or-no question about a
// snippet. The external benchmarks ask a harder one: given a repository, does
// the tool find the vulnerability where it is? They need the file path, the
// line and the CWE of a finding, and they label the false-positive traps.
//
// The corpora are not vendored into this repository: they are fetched at
// benchmark time into a cache directory, at a pinned URL, and the digest of
// the bytes that were used is written into the report.
//
//	go run ./cmd/aisai-bench-external --corpus benchmarkpython --limit 100 --out /tmp/bp.json
//	go run ./cmd/aisai-bench-external --corpus realvuln --offline
//
// The results go to stdout, or to the file of --out. The logs go to stderr.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/tlmcguire/aisai/internal/bench/external"
	"github.com/tlmcguire/aisai/internal/config"
	"github.com/tlmcguire/aisai/internal/llm"
)

const (
	// reportSchemaVersion is the version of the JSON report.
	reportSchemaVersion = "1.0"
	// providerTimeout bounds a single request to the model.
	providerTimeout = 180 * time.Second
	// healthTimeout bounds the reachability probe of the provider.
	healthTimeout = 3 * time.Second
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("aisai-bench-external: ")
	if err := run(os.Args[1:]); err != nil {
		log.Fatalf("fatal: %v", err)
	}
}

// run parses the flags and executes the benchmark.
func run(args []string) error {
	fs := flag.NewFlagSet("aisai-bench-external", flag.ContinueOnError)
	var (
		corpus      = fs.String("corpus", "", "corpus to run: realvuln | benchmarkpython")
		cacheDir    = fs.String("cache-dir", external.DefaultCacheDir, "directory the corpora are cached in")
		offline     = fs.Bool("offline", false, "do not download anything: use only what the cache already holds")
		limit       = fs.Int("limit", 0, "maximum number of files to scan; 0 means every file")
		out         = fs.String("out", "", "path of the JSON report (default: stdout)")
		engine      = fs.String("engine", "", "engine to run: static | llm | both (default: static, plus the LLM when one is reachable)")
		modelName   = fs.String("model", "", "name of the LLM model (default: the configured scan model)")
		language    = fs.String("language", "python", "language of the RealVuln repositories to run")
		cfgPath     = fs.String("config", "", "path of the config file")
		cutoff      = fs.String("cutoff", "", "cutoff date of the corpus, as YYYY-MM-DD (recorded in the report)")
		modelCutoff = fs.String("model-cutoff", "", "training cutoff of the evaluated model, as YYYY-MM-DD (recorded in the report)")
	)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "usage: aisai-bench-external [flags]\n\n")
		fmt.Fprintf(fs.Output(), "Flags:\n")
		fs.PrintDefaults()
		fmt.Fprintf(fs.Output(), `
The corpora are fetched at benchmark time into the cache directory. The
RealVuln benchmark is licensed under Apache-2.0 and the OWASP Benchmark under
GPL-3.0; neither is vendored into this repository.

The report carries the digest of the artefact the measurement was made on, so
that a number can always be tied to the bytes it was read from.
`)
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *corpus == "" {
		return fmt.Errorf("no corpus given: use --corpus realvuln or --corpus benchmarkpython")
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	model := *modelName
	if model == "" {
		model = cfg.ScanModel
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	provider := buildProvider(cfg)
	detectors, err := selectDetectors(ctx, cfg, provider, *engine, model)
	if err != nil {
		return err
	}
	for _, d := range detectors {
		log.Printf("engine %s", d.Name())
	}

	report := Report{
		SchemaVersion: reportSchemaVersion,
		Corpus:        strings.ToLower(*corpus),
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
	}

	switch strings.ToLower(*corpus) {
	case external.RealVulnBenchmarkName:
		corpus, digest, err := fetchRealVuln(ctx, *cacheDir, *language, *offline)
		if err != nil {
			return err
		}
		report.CorpusSHA256 = digest
		for _, d := range detectors {
			run, err := external.RunRealVuln(ctx, corpus, d, *limit)
			if err != nil {
				return err
			}
			report.Engines = append(report.Engines, engineReport(run.Detector, run.Files, run.Findings,
				run.Counts, run.Traps, run.Errors))
		}
	case external.BenchmarkPythonName:
		corpus, digest, err := fetchBenchmarkPython(ctx, *cacheDir, *offline)
		if err != nil {
			return err
		}
		report.CorpusSHA256 = digest
		for _, d := range detectors {
			run, err := external.RunBenchmarkPython(ctx, corpus, d, *limit)
			if err != nil {
				return err
			}
			report.Engines = append(report.Engines, engineReport(run.Detector, run.Files, run.Findings,
				run.Counts, external.RealVulnTrapCounts{}, run.Errors))
		}
	default:
		return fmt.Errorf("unknown corpus %q: want realvuln or benchmarkpython", *corpus)
	}

	report.Leakage, err = leakageMetadata(*cutoff, *modelCutoff, model, report.CorpusSHA256)
	if err != nil {
		return err
	}

	return writeReport(*out, report)
}

// fetchRealVuln fetches the RealVuln corpus and returns it with the digest
// of the benchmark archive it was read from.
func fetchRealVuln(ctx context.Context, cacheDir, language string, offline bool) (*external.RealVulnCorpus, string, error) {
	corpus, err := external.FetchRealVuln(ctx, cacheDir, language, offline)
	if err != nil {
		return nil, "", err
	}
	log.Printf("the RealVuln corpus holds %d repositories", len(corpus.Repos))
	return corpus, corpus.Digest, nil
}

// fetchBenchmarkPython fetches the BenchmarkPython corpus and returns it
// with the digest of the archive it was read from.
func fetchBenchmarkPython(ctx context.Context, cacheDir string, offline bool) (*external.BenchmarkPythonCorpus, string, error) {
	corpus, err := external.FetchBenchmarkPython(ctx, cacheDir, offline)
	if err != nil {
		return nil, "", err
	}
	log.Printf("the BenchmarkPython corpus holds %d test cases", len(corpus.Cases))
	return corpus, corpus.Digest, nil
}

// selectDetectors builds the detector list for the requested mode.
//
// The static engine is always available. The LLM engine is added when a
// provider is reachable, unless the caller asked for the static engine only.
func selectDetectors(ctx context.Context, cfg *config.Config, provider llm.Provider, engine, model string) ([]external.Detector, error) {
	static := external.NewStaticDetector(external.SeverityPolicy)
	switch strings.ToLower(strings.TrimSpace(engine)) {
	case "", "static", "sast":
		if engine != "" {
			return []external.Detector{static}, nil
		}
	case "llm":
		if provider == nil {
			return nil, fmt.Errorf("no LLM provider configured: the LLM is disabled")
		}
		return []external.Detector{external.NewLLMTDetector(provider, model, cfg.NumCtx, providerTimeout)}, nil
	case "both":
		if provider == nil {
			return []external.Detector{static}, nil
		}
		return []external.Detector{
			external.NewLLMTDetector(provider, model, cfg.NumCtx, providerTimeout),
			static,
		}, nil
	default:
		return nil, fmt.Errorf("unknown engine %q: want static, llm or both", engine)
	}

	// No engine was named: add the LLM one when a provider answers.
	if provider == nil {
		log.Printf("the LLM is disabled (DISABLE_LLM): running the static engine only")
		return []external.Detector{static}, nil
	}
	probe, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()
	if err := provider.Health(probe); err != nil {
		log.Printf("no LLM reachable (%v): running the static engine only", err)
		return []external.Detector{static}, nil
	}
	return []external.Detector{
		external.NewLLMTDetector(provider, model, cfg.NumCtx, providerTimeout),
		static,
	}, nil
}

// buildProvider returns the provider for the LLM path, or nil when the LLM is
// disabled.
//
// It mirrors the provider construction of cmd/aisai-bench: the same
// configuration, the same BYOK handling and the same local Ollama fallback.
func buildProvider(cfg *config.Config) llm.Provider {
	if cfg.DisableLLM {
		return nil
	}
	if cfg.BYOKBaseURL != "" && cfg.BYOKAPIKey != "" {
		return llm.NewOpenAICompat(llm.Options{
			URL:     cfg.BYOKBaseURL,
			Model:   cfg.BYOKModel,
			APIKey:  cfg.BYOKAPIKey,
			NumCtx:  cfg.NumCtx,
			Timeout: providerTimeout,
		})
	}
	return llm.NewOllama(llm.Options{
		URL:        strings.Join(cfg.OllamaURLs, ","),
		Model:      cfg.ChatModel,
		EmbedModel: cfg.EmbedModel,
		NumCtx:     cfg.NumCtx,
		Timeout:    providerTimeout,
	})
}

// TrapReport holds the outcome on the false-positive traps of a corpus.
type TrapReport struct {
	// Traps is the number of false-positive traps of the ground truth.
	Traps int `json:"traps"`
	// Hit is the number of traps the engine reported on.
	Hit int `json:"hit"`
	// Silent is the number of traps the engine did not report on.
	Silent int `json:"silent"`
}

// EngineReport holds the results of one engine.
type EngineReport struct {
	// Engine identifies the engine.
	Engine string `json:"engine"`
	// FilesScanned is the number of files that were scanned.
	FilesScanned int `json:"files_scanned"`
	// Findings is the number of findings the engine reported.
	Findings int `json:"findings"`
	// TruePositives is the number of findings that hit the ground truth.
	TruePositives int `json:"true_positives"`
	// FalsePositives is the number of findings that hit nothing.
	FalsePositives int `json:"false_positives"`
	// FalseNegatives is the number of ground-truth entries that were not
	// reported.
	FalseNegatives int `json:"false_negatives"`
	// TrueNegatives is the number of ground-truth entries that were
	// correctly left alone.
	TrueNegatives int `json:"true_negatives"`
	// Precision is the share of the findings that were correct.
	Precision float64 `json:"precision"`
	// Recall is the share of the ground truth that was found.
	Recall float64 `json:"recall"`
	// F1 is the harmonic mean of the precision and the recall.
	F1 float64 `json:"f1"`
	// FalsePositiveTraps holds the outcome on the traps. It is empty for
	// the corpora that have no traps.
	FalsePositiveTraps *TrapReport `json:"false_positive_traps,omitempty"`
	// Errors holds the files that could not be scanned.
	Errors []string `json:"errors,omitempty"`
}

// LeakageReport records the provenance of the run in the context of the
// training cutoff of the evaluated model.
type LeakageReport struct {
	// CorpusSHA256 is the digest of the corpus the run was made on.
	CorpusSHA256 string `json:"corpus_sha256"`
	// Model identifies the evaluated model.
	Model string `json:"model"`
	// Cutoff is the cutoff the corpus was split on, when one was given.
	Cutoff string `json:"cutoff,omitempty"`
	// ModelCutoff is the training cutoff of the evaluated model, when one
	// was given.
	ModelCutoff string `json:"model_cutoff,omitempty"`
	// Note explains what the cutoffs mean.
	Note string `json:"note"`
}

// Report is the JSON report of a run.
type Report struct {
	// SchemaVersion is the version of the report.
	SchemaVersion string `json:"schema_version"`
	// Corpus is the name of the corpus.
	Corpus string `json:"corpus"`
	// CorpusSHA256 is the digest of the artefact the corpus was read from.
	CorpusSHA256 string `json:"corpus_sha256"`
	// GeneratedAt is the moment of the run.
	GeneratedAt string `json:"generated_at"`
	// Engines holds the results of every engine of the run.
	Engines []EngineReport `json:"engines"`
	// Leakage records the provenance of the run.
	Leakage LeakageReport `json:"leakage"`
}

// engineReport renders the results of one engine.
func engineReport(name string, files, findings int, counts external.Counts, traps external.RealVulnTrapCounts, errs []string) EngineReport {
	r := EngineReport{
		Engine:         name,
		FilesScanned:   files,
		Findings:       findings,
		TruePositives:  counts.TP,
		FalsePositives: counts.FP,
		FalseNegatives: counts.FN,
		TrueNegatives:  counts.TN,
		Precision:      counts.Precision(),
		Recall:         counts.Recall(),
		F1:             counts.F1(),
		Errors:         errs,
	}
	if traps.Traps > 0 {
		r.FalsePositiveTraps = &TrapReport{
			Traps:  traps.Traps,
			Hit:    traps.TrapsHit,
			Silent: traps.TrapsSilent,
		}
	}
	return r
}

// leakageMetadata builds the provenance of the run.
//
// The cutoffs are supplied by the operator: neither the training cutoff of a
// model nor the date of the corpus can be inferred.
func leakageMetadata(cutoff, modelCutoff, model, corpusSHA string) (LeakageReport, error) {
	m := LeakageReport{
		CorpusSHA256: corpusSHA,
		Model:        model,
		Note: "the training cutoff of the evaluated model is supplied by the operator; " +
			"it cannot be inferred, and a model that was trained later than the corpus " +
			"sees rows it has already seen",
	}
	if cutoff != "" {
		date, err := time.Parse("2006-01-02", cutoff)
		if err != nil {
			return m, fmt.Errorf("the cutoff %q is not a YYYY-MM-DD date: %w", cutoff, err)
		}
		m.Cutoff = date.UTC().Format(time.RFC3339)
	}
	if modelCutoff != "" {
		date, err := time.Parse("2006-01-02", modelCutoff)
		if err != nil {
			return m, fmt.Errorf("the model cutoff %q is not a YYYY-MM-DD date: %w", modelCutoff, err)
		}
		m.ModelCutoff = date.UTC().Format(time.RFC3339)
	}
	return m, nil
}

// writeReport writes the report to the given path, or to stdout.
func writeReport(path string, report Report) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("render the report: %w", err)
	}
	data = append(data, '\n')
	if path == "" {
		if _, err := os.Stdout.Write(data); err != nil {
			return fmt.Errorf("write the report to stdout: %w", err)
		}
		return nil
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write the report to %s: %w", path, err)
	}
	log.Printf("wrote the report to %s", path)
	return nil
}
