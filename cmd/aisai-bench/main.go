// Command aisai-bench measures the AISAI detection engines against the
// labelled corpus in bench/data1551redacted.csv and writes the results as a
// CSV that the notebooks under bench/notebooks/ can read unmodified.
//
// The harness calls the same engine code the application uses: internal/sast
// for the static engine and internal/scan for the LLM engine. Nothing is
// shelled out to Bandit or to the legacy Python code.
//
//	go run ./cmd/aisai-bench --engine bandit --limit 20 --dry-run --out /tmp/bench.csv
//	go run ./cmd/aisai-bench --engine llm --limit 50
//	go run ./cmd/aisai-bench --baseline always_safe --limit 50
//
// The output columns follow the notebooks: "llm_" for the LLM engine,
// "bandit_" for the static engine, and "benchmark_input_mode" with
// "expected_before"/"expected_after" for the ground truth.
//
// A run with both engines writes the llm_ columns before the bandit_ columns.
// The two CSV notebooks pick their metric prefix in different orders — the
// LLM one looks for "llm_" first, the static one for "bandit_" first — so
// this order satisfies both: each notebook finds its own engine first.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/tlmcguire/aisai/internal/bench"
	"github.com/tlmcguire/aisai/internal/config"
	"github.com/tlmcguire/aisai/internal/llm"
)

const (
	// defaultInput is the corpus the benchmark runs on.
	defaultInput = "bench/data1551redacted.csv"
	// defaultConcurrency is the number of workers of the LLM path. The
	// pipeline waits on the model for every snippet, so a handful of workers
	// keeps the run busy without swamping a local Ollama server.
	defaultConcurrency = 4
	// providerTimeout bounds a single request to the model.
	providerTimeout = 180 * time.Second
	// healthTimeout bounds the reachability probe of the provider.
	healthTimeout = 3 * time.Second
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("aisai-bench: ")
	if err := run(os.Args[1:]); err != nil {
		log.Fatalf("fatal: %v", err)
	}
}

// run parses the flags and executes the benchmark.
func run(args []string) error {
	fs := flag.NewFlagSet("aisai-bench", flag.ContinueOnError)
	var (
		input       = fs.String("input", defaultInput, "path of the input CSV")
		out         = fs.String("out", "", "path of the output CSV (default: bench/results/bench_<engine>_<model>_<timestamp>.csv)")
		engine      = fs.String("engine", "", "engine to run: llm | bandit | both (default: both when an LLM is reachable, else bandit)")
		limit       = fs.Int("limit", 0, "maximum number of rows to evaluate; 0 means all rows, which is the default")
		beforeCol   = fs.String("before-col", "vulnerable_code", "name of the column holding the vulnerable snippet")
		afterCol    = fs.String("after-col", "fixed_code", "name of the column holding the fixed snippet")
		codeCol     = fs.String("code-col", "", "name of the column holding the snippet of a single-sample input")
		labelCol    = fs.String("label-col", "", "name of the column holding the ground truth of a single-sample input")
		modelName   = fs.String("model", "", "name of the LLM model (default: the configured scan model, OLLAMA_PRIMARY_MODEL)")
		concurrency = fs.Int("concurrency", defaultConcurrency, "number of workers of the LLM path")
		modelTag    = fs.String("model-tag", "", "string embedded in the default output file name")
		dryRun      = fs.Bool("dry-run", false, "run without any model call: the LLM path is exercised with a stub classifier")
		baseline    = fs.String("baseline", "", "run a trivial baseline instead of a model: always_vulnerable | always_safe | keyword | tfidf")
		metrics     = fs.Bool("metrics", true, "print the detection metrics of every engine to stderr")
		noMetrics   = fs.Bool("no-metrics", false, "do not print the detection metrics")
		metricsOut  = fs.String("metrics-out", "", "path of the JSON file the metrics are written to")
		tfidfSeed   = fs.Int64("tfidf-seed", bench.DefaultSplitSeed, "seed of the deterministic train/test split of the tfidf baseline")
		tfidfTest   = fs.Float64("tfidf-test-fraction", bench.DefaultTestFraction, "share of the corpus groups held out of the tfidf training split")
		tfidfMode   = fs.String("tfidf-mode", "classifier", "tfidf baseline mode: classifier | keyword")
		cfgPath     = fs.String("config", "", "path of the config file")
	)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "usage: aisai-bench [flags]\n\n")
		fmt.Fprintf(fs.Output(), "Flags:\n")
		fs.PrintDefaults()
		fmt.Fprintf(fs.Output(), `
The output CSV keeps every column of the input and adds the metric columns of
the selected engine(s):

  llm_*      the LLM engine (internal/scan, detect then verify)
  bandit_*   the static engine (internal/sast, the rule engine of the app)

The "bandit_" prefix is the one the notebooks look for; the findings are
produced by the AISAI rule engine, not by Bandit. A baseline run is written
under the llm_ prefix as well, because bandit_ belongs to the static engine,
and its model column reads "baseline:<name>" so that it can never be mistaken
for a real engine run.

The detection metrics are printed to stderr, so that stdout stays clean:
the confusion matrix, the precision, the recall, F1, the recall-weighted F2
and F3, the abstention count and rate, the false-positive rate on the patched
corpus, the per-CWE breakdown and the null baselines. --metrics-out writes
the same numbers as JSON. The CSV schema is not changed by any of this.

The default row limit is 0, which means every row of the corpus. A full LLM
run over the 1,551-row corpus costs 3,102 detection calls plus one
verification call per candidate, so use --limit for a smoke test.

The "tfidf" baseline is the one that matters for honesty: it is a TF-IDF
featurisation with a multinomial Naive Bayes classifier, trained on a
deterministic train split of the same corpus and evaluated on the held-out
split. If a trivial method finds as much as the real engine, that is the
finding. Use --tfidf-seed and --tfidf-test-fraction to change the split; the
same values always give the same split.
`)
	}
	if err := fs.Parse(args); err != nil {
		return err
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
	selected := *engine
	if *baseline != "" {
		// A baseline runs no model at all, so no provider is needed.
		selected = ""
	} else if selected == "" {
		selected = resolveEngine(ctx, cfg, provider, *dryRun)
	}
	if *baseline == "tfidf" {
		// The tfidf baseline needs a trained model, and the model is trained
		// on a deterministic split of the same corpus. The split is
		// reported, because a baseline evaluated on its own training data
		// measures nothing.
		model, split, err := bench.TrainTFIDFBaseline(*input, bench.TFIDFOptions{
			Split: bench.SplitOptions{
				Seed:         *tfidfSeed,
				TestFraction: *tfidfTest,
			},
			Mode: *tfidfMode,
		})
		if err != nil {
			return err
		}
		log.Printf("tfidf baseline trained: %d train rows, %d held-out rows, seed %d",
			len(split.Train), len(split.Test), split.Seed)
		_ = model
	}

	opts := bench.Options{
		Input:       *input,
		Engines:     selected,
		Limit:       *limit,
		BeforeCol:   *beforeCol,
		AfterCol:    *afterCol,
		CodeCol:     *codeCol,
		LabelCol:    *labelCol,
		Model:       model,
		Concurrency: *concurrency,
		ModelTag:    *modelTag,
		DryRun:      *dryRun,
		Baseline:    *baseline,
		Provider:    provider,
		NumCtx:      cfg.NumCtx,
		Timeout:     providerTimeout,
	}

	label, tag := outputLabel(selected, *baseline, model, *modelTag)
	switch {
	case *baseline != "":
		log.Printf("baseline %s (no model), input %s", *baseline, opts.Input)
	case selected == bench.PrefixStatic:
		log.Printf("engine %s, model %s, input %s", selected, "aisai-sast", opts.Input)
	default:
		log.Printf("engine %s, model %s, input %s", selected, describeModel(cfg, model), opts.Input)
	}
	if *out == "" {
		opts.Out = defaultOutputPath(label, tag)
	} else {
		opts.Out = *out
	}

	rep, err := bench.RunAndWrite(ctx, opts)
	if err != nil {
		return err
	}
	log.Printf("wrote %d rows to %s", len(rep.Rows), opts.Out)
	summarize(rep)

	// The metrics are additive: the CSV the notebooks read is not changed
	// by any of this. They go to stderr, so that stdout stays clean.
	if *metrics || *metricsOut != "" {
		doc := bench.BuildMetricsDocument(rep, nil)
		if *metricsOut != "" {
			if err := bench.WriteMetricsJSON(*metricsOut, doc); err != nil {
				return err
			}
			log.Printf("wrote the metrics to %s", *metricsOut)
		}
		if *metrics && !*noMetrics {
			fmt.Fprint(os.Stderr, doc.String())
		}
	}
	return nil
}

// resolveEngine picks the default engine set: both engines when an LLM is
// reachable, the static engine alone otherwise.
//
// A dry run needs no model, so it always uses both engines with a stub in
// place of the LLM.
func resolveEngine(ctx context.Context, cfg *config.Config, provider llm.Provider, dryRun bool) string {
	if dryRun {
		return "both"
	}
	if provider == nil {
		log.Printf("the LLM is disabled (DISABLE_LLM): running the static engine only")
		return "bandit"
	}
	probe, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()
	if err := provider.Health(probe); err != nil {
		log.Printf("no LLM reachable (%v): running the static engine only", err)
		return "bandit"
	}
	return "both"
}

// buildProvider returns the provider for the LLM path, or nil when the LLM is
// disabled.
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

// outputLabel returns the file name fragment and the model tag of a run.
func outputLabel(engine, baseline, model, modelTag string) (string, string) {
	if baseline != "" {
		return "baseline_" + baseline, "baseline"
	}
	tag := modelTag
	if tag == "" {
		switch engine {
		case "bandit":
			tag = "sast"
		default:
			tag = model
		}
	}
	return engine, tag
}

// defaultOutputPath derives the output CSV path from the run parameters.
func defaultOutputPath(label, tag string) string {
	safe := sanitizeTag(tag)
	if safe == "" {
		safe = "model"
	}
	stamp := time.Now().Format("20060102-150405")
	return filepath.Join("bench", "results", fmt.Sprintf("bench_%s_%s_%s.csv", label, safe, stamp))
}

// sanitizeTag strips everything that is not a letter or a digit, the way the
// legacy notebook did with re.sub(r"[^A-Za-z0-9]+", "", model).
func sanitizeTag(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// describeModel names the model without ever leaking the API key.
func describeModel(cfg *config.Config, model string) string {
	if cfg.BYOKBaseURL != "" {
		return model + " at " + cfg.Redacted().BYOKBaseURL
	}
	return model
}

// summarize prints the accuracy of every engine of the run.
func summarize(rep *bench.Report) {
	for _, e := range rep.Engines {
		if rep.Schema.Mode == bench.ModePaired {
			correct, total := 0, 0
			for _, row := range e.Rows {
				if row.Before.Completed {
					total++
					if row.Before.Correct {
						correct++
					}
				}
				if row.After.Completed {
					total++
					if row.After.Correct {
						correct++
					}
				}
			}
			log.Printf("%s: %d/%d correct (%.1f%%) over the completed evaluations", e.Prefix, correct, total, percent(correct, total))
			continue
		}
		correct, total := 0, 0
		for _, row := range e.Rows {
			if row.Single.Completed {
				total++
				if row.Single.Correct {
					correct++
				}
			}
		}
		log.Printf("%s: %d/%d correct (%.1f%%) over the completed evaluations", e.Prefix, correct, total, percent(correct, total))
	}
}

// percent returns the share of correct evaluations, as a percentage.
func percent(correct, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(correct) / float64(total) * 100
}
