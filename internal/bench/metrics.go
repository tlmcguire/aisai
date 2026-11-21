// Package bench measures the AISAI detection engines against a labelled
// corpus. This file holds its detection metrics.
//
// # Detection metrics
//
// The metrics are the confusion matrix, precision, recall, F1, the
// recall-weighted F2 and F3, the false-positive rate on the patched
// corpus, the per-CWE breakdown, the null baselines and the ROC-AUC.
//
// # How "unknown" is handled
//
// "unknown" is a real class of the output contract — the notebooks give it
// its own confusion-matrix column — so it is never folded into "safe".
//
// The metrics are computed over the *decided* samples only: the samples
// the engine answered, with a classification of "vulnerable" or "safe" and
// a completed run. A sample the engine could not answer is *abstained*:
// it is counted in Abstention, with the count and the rate, and it is
// excluded from the confusion matrix. Folding those samples into "safe"
// would inflate the accuracy of an engine that simply gives up, and it
// would hide the failure rate behind a plausible number.
//
// The abstention is always reported next to the metrics it qualifies, and
// the patched-corpus false-positive rate is always reported next to it as
// well: an engine that abstains on everything has a perfect precision and
// a false-positive rate of zero, and only the abstention count tells the
// reader that the engine never answered.
//
// # ROC-AUC
//
// The ROC-AUC is computed from a continuous score. It is reported as *not
// applicable* — never as 0.0 and never as 0.5 — when the engine produces
// no score. See ComputeEngineMetricsWithScores.
package bench

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tlmcguire/aisai/internal/scan"
)

// MetricsSchemaVersion is the version of the JSON metrics schema.
//
// It changes whenever a field of MetricsDocument is removed or renamed, or
// when the meaning of a field changes. A new field that is added with a
// safe default does not change the version.
const MetricsSchemaVersion = "1"

// ColumnVulnerabilityCWEIDs is the corpus column that carries the
// vulnerability families of a row. The notebooks read the same column.
const ColumnVulnerabilityCWEIDs = "vulnerability_type_cwe_ids"

// CWEUnspecified is the group a row falls into when its family cell is
// empty or holds a placeholder such as "N/A".
const CWEUnspecified = "(unspecified)"

// The sides a sample can come from.
const (
	// SampleSideBefore is the vulnerable snippet of a paired row.
	SampleSideBefore = "before"
	// SampleSideAfter is the fixed snippet of a paired row.
	SampleSideAfter = "after"
	// SampleSideSingle is the snippet of a single-mode row.
	SampleSideSingle = "single"
)

// metricsCWEColumns are the column names searched for the vulnerability
// families of a row, in order of preference.
var metricsCWEColumns = []string{ColumnVulnerabilityCWEIDs, "cwe_ids", "cwe_id", "cwe"}

// metricsCWESeparators are the characters a cell holding several
// vulnerability families may separate them with.
const metricsCWESeparators = ",;|"

// ConfusionMatrix is the binary confusion matrix over the decided samples.
//
// The positive class is "vulnerable": a sample is a true positive when the
// engine called it vulnerable and it really is vulnerable.
type ConfusionMatrix struct {
	// TP counts the vulnerable samples the engine called vulnerable.
	TP int `json:"tp"`
	// FP counts the safe samples the engine called vulnerable.
	FP int `json:"fp"`
	// TN counts the safe samples the engine called safe.
	TN int `json:"tn"`
	// FN counts the vulnerable samples the engine called safe.
	FN int `json:"fn"`
}

// Total returns the number of decided samples the matrix covers.
func (c ConfusionMatrix) Total() int {
	return c.TP + c.FP + c.TN + c.FN
}

// Accuracy returns the share of decided samples that were classified
// correctly.
//
// It is 0 when the matrix is empty, because there is nothing to be right
// about.
func (c ConfusionMatrix) Accuracy() float64 {
	if c.Total() == 0 {
		return 0
	}
	return float64(c.TP+c.TN) / float64(c.Total())
}

// Precision returns the share of the flagged samples that really are
// vulnerable.
//
// It is 0 when the engine flagged nothing (TP+FP is 0). The zero is
// reported, not hidden: an engine that never flags anything has a
// precision of zero, not an undefined one.
func (c ConfusionMatrix) Precision() float64 {
	if c.TP+c.FP == 0 {
		return 0
	}
	return float64(c.TP) / float64(c.TP+c.FP)
}

// Recall returns the share of the vulnerable samples the engine found.
//
// It is 0 when there is no vulnerable sample to find (TP+FN is 0).
func (c ConfusionMatrix) Recall() float64 {
	if c.TP+c.FN == 0 {
		return 0
	}
	return float64(c.TP) / float64(c.TP+c.FN)
}

// F1 returns the harmonic mean of the precision and the recall.
//
// It is 0 when the matrix holds no true positive: the standard formula
// 2·TP/(2·TP+FP+FN) has a zero denominator there.
func (c ConfusionMatrix) F1() float64 {
	den := 2*c.TP + c.FP + c.FN
	if den == 0 {
		return 0
	}
	return 2 * float64(c.TP) / float64(den)
}

// FBeta returns the F-beta score of the matrix.
//
// Fβ = (1+β²)·P·R / (β²·P + R).
//
// A β of 1 is the F1. A β above 1 weights the recall more heavily than the
// precision: F2 weighs the recall twice as much as the precision, F3
// three times as much.
//
// The choice is deliberate for this tool. A missed vulnerability is worse
// than a false alarm, because the user of a vulnerability auditor acts on
// what they are told — a missed vulnerability is a silent hole in the
// supply chain. But false alarms have a real cost too: a scanner that
// cries wolf on every file gets ignored, and then its true findings are
// ignored as well. F1 weighs the two errors equally, which is the wrong
// balance for a security tool, and F2/F3 make the trade-off explicit
// instead of hiding it in a single number.
//
// It is 0 when the denominator is 0, that is when the engine flagged
// nothing and there is no vulnerable sample either.
func (c ConfusionMatrix) FBeta(beta float64) float64 {
	p := c.Precision()
	r := c.Recall()
	den := beta*beta*p + r
	if den == 0 {
		return 0
	}
	return (1 + beta*beta) * p * r / den
}

// Abstention counts the samples an engine did not answer.
//
// An abstention is a sample the engine could not decide: the run did not
// complete, or the classification is "unknown". Such a sample is excluded
// from the confusion matrix and counted here instead, so that a broken
// engine cannot hide behind a plausible accuracy.
type Abstention struct {
	// Count is the number of samples the engine did not decide.
	Count int `json:"count"`
	// Rate is the share of the samples that were not decided.
	Rate float64 `json:"rate"`
	// Unknown counts the samples whose classification is "unknown".
	Unknown int `json:"unknown"`
	// Failed counts the samples whose run did not complete.
	Failed int `json:"failed"`
}

// PatchedCorpus holds the false-positive rate on the patched side of the
// corpus.
//
// For the paired layout the patched side is the "after" snippet of every
// row — the code after the fix. Those samples are the ones a security
// tool cries wolf on, and the rate it does so at is the number that
// decides whether the tool is used at all.
type PatchedCorpus struct {
	// Applicable is false when the corpus holds no patched sample.
	Applicable bool `json:"applicable"`
	// Samples is the number of patched samples in the corpus.
	Samples int `json:"samples"`
	// Decided is the number of patched samples the engine answered.
	Decided int `json:"decided"`
	// FalsePositives is the number of patched samples the engine called
	// vulnerable.
	FalsePositives int `json:"false_positives"`
	// FPR is the false-positive rate: the patched samples the engine
	// called vulnerable, over the patched samples it decided.
	//
	// It is 0 when the engine decided no patched sample, and the
	// Applicable flag and the Decided count say so.
	FPR float64 `json:"fpr"`
}

// CWEMetrics holds the metrics of one vulnerability family.
//
// A row that carries several families contributes its samples to each of
// them, so the supports of the groups add up to more than the size of the
// corpus.
type CWEMetrics struct {
	// CWE is the vulnerability family.
	CWE string `json:"cwe"`
	// Support is the number of samples of the family.
	Support int `json:"support"`
	// Decided is the number of samples of the family the engine answered.
	Decided int `json:"decided"`
	// Counts is the confusion matrix of the family.
	Counts ConfusionMatrix `json:"confusion_matrix"`
	// Accuracy is the accuracy of the family.
	Accuracy float64 `json:"accuracy"`
	// Precision is the precision of the family.
	Precision float64 `json:"precision"`
	// Recall is the recall of the family.
	Recall float64 `json:"recall"`
	// F1 is the F1 of the family.
	F1 float64 `json:"f1"`
}

// BaselineMetrics holds the metrics of one trivial classifier.
type BaselineMetrics struct {
	// Name is the name of the baseline.
	Name string `json:"name"`
	// Accuracy is the accuracy of the baseline.
	Accuracy float64 `json:"accuracy"`
	// Precision is the precision of the baseline.
	Precision float64 `json:"precision"`
	// Recall is the recall of the baseline.
	Recall float64 `json:"recall"`
	// F1 is the F1 of the baseline.
	F1 float64 `json:"f1"`
	// F2 is the F2 of the baseline.
	F2 float64 `json:"f2"`
	// F3 is the F3 of the baseline.
	F3 float64 `json:"f3"`
	// Counts is the confusion matrix of the baseline.
	Counts ConfusionMatrix `json:"confusion_matrix"`
}

// NullBaselines holds the two trivial classifiers the metrics are read
// against.
//
// On the balanced paired corpus both of them score exactly 50% of the
// samples, whatever the corpus is: one vulnerable snippet and one fixed
// snippet per row. A detector that does not beat 50% is not detecting
// anything.
type NullBaselines struct {
	// AlwaysVulnerable is the classifier that always answers "vulnerable".
	AlwaysVulnerable BaselineMetrics `json:"always_vulnerable"`
	// AlwaysSafe is the classifier that always answers "safe".
	AlwaysSafe BaselineMetrics `json:"always_safe"`
}

// Metrics holds the detection metrics of one engine over one run.
type Metrics struct {
	// Engine is the column prefix of the engine, "llm" or "bandit".
	Engine string `json:"engine"`
	// Model identifies the engine or model that produced the outcomes.
	Model string `json:"model"`
	// InputMode is the layout of the benchmark input.
	InputMode Mode `json:"input_mode"`
	// Samples is the number of snippets the engine was asked about.
	Samples int `json:"samples"`
	// Decided is the number of samples the engine answered.
	Decided int `json:"decided"`
	// Unlabeled is the number of samples whose ground truth is neither
	// vulnerable nor safe, and which therefore cannot be scored.
	Unlabeled int `json:"unlabeled"`
	// Counts is the confusion matrix over the decided samples.
	Counts ConfusionMatrix `json:"confusion_matrix"`
	// Accuracy is the share of the decided samples that were classified
	// correctly.
	Accuracy float64 `json:"accuracy"`
	// Precision is the share of the flagged samples that really are
	// vulnerable.
	Precision float64 `json:"precision"`
	// Recall is the share of the vulnerable samples the engine found.
	Recall float64 `json:"recall"`
	// F1 is the harmonic mean of the precision and the recall.
	F1 float64 `json:"f1"`
	// F2 is the recall-weighted F2.
	F2 float64 `json:"f2"`
	// F3 is the recall-weighted F3.
	F3 float64 `json:"f3"`
	// ROCAUC is the area under the ROC curve.
	//
	// It is nil when the engine produces no continuous score. A missing
	// ROC-AUC means "not applicable", never 0.0 and never 0.5.
	ROCAUC *float64 `json:"roc_auc"`
	// ROCAUCReason explains why the ROC-AUC is not applicable.
	ROCAUCReason string `json:"roc_auc_reason,omitempty"`
	// Abstention counts the samples the engine did not answer.
	Abstention Abstention `json:"abstention"`
	// PerCWE holds the metrics of every vulnerability family, ordered by
	// support.
	PerCWE []CWEMetrics `json:"per_cwe"`
	// Patched holds the false-positive rate on the patched corpus.
	Patched PatchedCorpus `json:"patched_corpus"`
	// NullBaselines holds the trivial classifiers evaluated over the same
	// decided samples, so that the numbers can be read against the floor.
	NullBaselines NullBaselines `json:"null_baselines"`
}

// CorpusStats describes the corpus a set of metrics was computed on.
type CorpusStats struct {
	// Rows is the number of input rows.
	Rows int `json:"rows"`
	// Samples is the number of snippets: two per row in paired mode, one
	// per row in single mode.
	Samples int `json:"samples"`
	// Vulnerable is the number of samples whose ground truth is
	// "vulnerable".
	Vulnerable int `json:"vulnerable_samples"`
	// Safe is the number of samples whose ground truth is "safe".
	Safe int `json:"safe_samples"`
	// Path is the path of the corpus the run was made from.
	Path string `json:"path,omitempty"`
	// SHA256 is the digest of the corpus file, so that a number can be
	// traced back to the exact bytes that produced it.
	SHA256 string `json:"sha256,omitempty"`
}

// MetricsDocument is the machine-readable form of the metrics of a run.
//
// The JSON shape is stable: schema_version names the version of this
// shape, and engines is the array of the per-engine metrics.
type MetricsDocument struct {
	// SchemaVersion is the version of this JSON shape.
	SchemaVersion string `json:"schema_version"`
	// PromptVersion identifies the prompt set the LLM engine used. It is
	// the version of the prompts of internal/scan, and it is recorded so
	// that a number can be traced back to the prompts that produced it.
	PromptVersion string `json:"prompt_version,omitempty"`
	// InputMode is the layout of the benchmark input.
	InputMode Mode `json:"input_mode"`
	// Corpus describes the corpus the metrics were computed on.
	Corpus CorpusStats `json:"corpus"`
	// Engines holds the metrics of every engine of the run.
	Engines []Metrics `json:"engines"`
	// NullBaselines holds the trivial classifiers evaluated over every
	// sample of the corpus.
	NullBaselines NullBaselines `json:"null_baselines"`
}

// ScoredSample pairs a ground-truth label with a continuous score.
//
// The positive class is "vulnerable": a higher score must mean a higher
// chance of the sample being vulnerable.
type ScoredSample struct {
	// Label is true for a vulnerable (positive) sample.
	Label bool `json:"label"`
	// Score is the continuous score of the sample.
	Score float64 `json:"score"`
}

// EvaluationSample is one snippet one engine was asked about.
type EvaluationSample struct {
	// Index is the position of the sample in the evaluation order.
	Index int
	// Row is the input row the sample came from.
	Row int
	// Side is the side of the row the sample came from: "before",
	// "after" or "single".
	Side string
	// Actual is the ground-truth label of the sample.
	Actual string
	// Predicted is the classification the engine returned.
	Predicted string
	// Completed reports whether the engine produced an answer.
	Completed bool
	// CWEs holds the vulnerability families of the row.
	CWEs []string
	// Code is the snippet the engine classified.
	Code string
}

// Decided reports whether the engine answered the sample.
//
// A sample is decided when the run completed and the classification is one
// of the two classes that can be scored: "vulnerable" or "safe".
func (s EvaluationSample) Decided() bool {
	if !s.Completed {
		return false
	}
	return s.Predicted == ClassificationVulnerable || s.Predicted == ClassificationSafe
}

// EvaluationSamples returns the samples one engine was asked about, in
// evaluation order.
//
// In paired mode a row contributes two samples: the vulnerable snippet
// ("before") and the fixed snippet ("after"). In single mode a row
// contributes one sample. The order is the order the engine classified
// the snippets in, so that a per-sample score can be lined up with it.
func EvaluationSamples(rep *Report, eng *EngineReport) []EvaluationSample {
	if rep == nil || eng == nil {
		return nil
	}
	paired := rep.Schema.Mode == ModePaired
	cweCol := metricsCWEColumn(rep.Header)
	out := make([]EvaluationSample, 0, len(eng.Rows))
	for i, row := range eng.Rows {
		cwes := metricsRowCWEs(rep, cweCol, i)
		switch {
		case paired:
			out = append(out, EvaluationSample{
				Index:     len(out),
				Row:       i,
				Side:      SampleSideBefore,
				Actual:    ClassificationVulnerable,
				Predicted: row.Before.Classification,
				Completed: row.Before.Completed,
				CWEs:      cwes,
				Code:      metricsCell(rep, i, rep.Schema.BeforeCol),
			})
			out = append(out, EvaluationSample{
				Index:     len(out),
				Row:       i,
				Side:      SampleSideAfter,
				Actual:    ClassificationSafe,
				Predicted: row.After.Classification,
				Completed: row.After.Completed,
				CWEs:      cwes,
				Code:      metricsCell(rep, i, rep.Schema.AfterCol),
			})
		default:
			actual := ""
			if i < len(rep.ExpectedLabels) {
				actual = rep.ExpectedLabels[i]
			}
			out = append(out, EvaluationSample{
				Index:     len(out),
				Row:       i,
				Side:      SampleSideSingle,
				Actual:    actual,
				Predicted: row.Single.Classification,
				Completed: row.Single.Completed,
				CWEs:      cwes,
				Code:      metricsCell(rep, i, rep.Schema.CodeCol),
			})
		}
	}
	return out
}

// ComputeEngineMetrics computes the detection metrics of one engine.
//
// The ROC-AUC is reported as not applicable, because no engine of the
// harness produces a continuous score. See ComputeEngineMetricsWithScores.
func ComputeEngineMetrics(rep *Report, eng *EngineReport) Metrics {
	return ComputeEngineMetricsWithScores(rep, eng, nil)
}

// ComputeEngineMetricsWithScores computes the detection metrics of one
// engine, using a continuous score for the ROC-AUC.
//
// scores holds one score per sample, in the order of EvaluationSamples. A
// nil or empty slice means the engine produces no score: the ROC-AUC is
// then reported as not applicable, with the reason, and never as 0.0 or
// 0.5.
//
// A score slice whose length does not match the number of samples is
// ignored, and the ROC-AUC is reported as not applicable.
func ComputeEngineMetricsWithScores(rep *Report, eng *EngineReport, scores []float64) Metrics {
	m := Metrics{}
	if eng == nil {
		return m
	}
	m.Engine = eng.Prefix
	m.Model = metricsEngineModel(rep, eng)
	if rep != nil {
		m.InputMode = rep.Schema.Mode
	}
	samples := EvaluationSamples(rep, eng)
	m.Samples = len(samples)

	decided := make([]EvaluationSample, 0, len(samples))
	for _, s := range samples {
		if s.Actual != ClassificationVulnerable && s.Actual != ClassificationSafe {
			m.Unlabeled++
			continue
		}
		if !s.Decided() {
			m.Abstention.Count++
			if s.Predicted == ClassificationUnknown {
				m.Abstention.Unknown++
			}
			if !s.Completed {
				m.Abstention.Failed++
			}
			continue
		}
		decided = append(decided, s)
		switch {
		case s.Actual == ClassificationVulnerable && s.Predicted == ClassificationVulnerable:
			m.Counts.TP++
		case s.Actual == ClassificationSafe && s.Predicted == ClassificationVulnerable:
			m.Counts.FP++
		case s.Actual == ClassificationSafe && s.Predicted == ClassificationSafe:
			m.Counts.TN++
		default:
			m.Counts.FN++
		}
	}
	m.Decided = len(decided)
	if m.Samples > 0 {
		m.Abstention.Rate = float64(m.Abstention.Count) / float64(m.Samples)
	}
	m.Accuracy = m.Counts.Accuracy()
	m.Precision = m.Counts.Precision()
	m.Recall = m.Counts.Recall()
	m.F1 = m.Counts.F1()
	m.F2 = m.Counts.FBeta(2)
	m.F3 = m.Counts.FBeta(3)
	m.Patched = metricsPatchedCorpus(samples, decided)
	m.PerCWE = metricsPerCWE(samples, decided)
	m.NullBaselines = metricsNullBaselines(decided)

	switch {
	case len(scores) == 0:
		m.ROCAUCReason = "the engine produces no continuous score: the outcome carries a class, not a score"
	case len(scores) != len(samples):
		m.ROCAUCReason = fmt.Sprintf("the score count (%d) does not match the number of samples (%d)", len(scores), len(samples))
	default:
		scored := make([]ScoredSample, 0, len(samples))
		for i, s := range samples {
			if s.Actual != ClassificationVulnerable && s.Actual != ClassificationSafe {
				continue
			}
			scored = append(scored, ScoredSample{
				Label: s.Actual == ClassificationVulnerable,
				Score: scores[i],
			})
		}
		value, ok := ROCAUC(scored)
		if !ok {
			m.ROCAUCReason = "the scores do not separate the two classes: the sample set holds no positive or no negative sample"
			break
		}
		m.ROCAUC = &value
	}
	return m
}

// ComputeMetrics computes the detection metrics of every engine of a
// report.
func ComputeMetrics(rep *Report) []Metrics {
	if rep == nil {
		return nil
	}
	out := make([]Metrics, 0, len(rep.Engines))
	for _, eng := range rep.Engines {
		out = append(out, ComputeEngineMetrics(rep, eng))
	}
	return out
}

// BuildMetricsDocument builds the machine-readable metrics of a report.
//
// scores holds the optional per-sample scores of the engines, keyed by the
// engine prefix. An engine without an entry produces no score, and its
// ROC-AUC is reported as not applicable.
func BuildMetricsDocument(rep *Report, scores map[string][]float64) MetricsDocument {
	doc := MetricsDocument{SchemaVersion: MetricsSchemaVersion, PromptVersion: scan.PromptVersion}
	if rep == nil {
		return doc
	}
	doc.InputMode = rep.Schema.Mode
	doc.Corpus = metricsCorpusStats(rep)
	doc.Engines = make([]Metrics, 0, len(rep.Engines))
	for _, eng := range rep.Engines {
		doc.Engines = append(doc.Engines, ComputeEngineMetricsWithScores(rep, eng, scores[eng.Prefix]))
	}
	doc.NullBaselines = metricsNullBaselines(metricsLabeledSamples(rep))
	return doc
}

// WriteMetricsJSON writes the metrics of a run as JSON at path, creating
// the parent directory when it does not exist.
func WriteMetricsJSON(path string, doc MetricsDocument) error {
	if path == "" {
		return fmt.Errorf("bench: no metrics path given")
	}
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("bench: create %s: %w", dir, err)
		}
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("bench: encode the metrics: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("bench: write %s: %w", path, err)
	}
	return nil
}

// ROCAUC computes the area under the ROC curve of a scored sample set.
//
// It uses the rank-based (Mann–Whitney U) formulation: every positive is
// compared with every negative, a positive that outranks a negative
// contributes one, and a tie contributes one half. Ties are therefore
// handled exactly and are never rounded away.
//
// The second return value is false when the area is undefined, that is
// when the sample set holds no positive or no negative sample. A caller
// must report that case as "not applicable" and never as 0.0 or 0.5.
func ROCAUC(samples []ScoredSample) (float64, bool) {
	positives, negatives := 0, 0
	for _, s := range samples {
		if s.Label {
			positives++
		} else {
			negatives++
		}
	}
	if positives == 0 || negatives == 0 {
		return 0, false
	}
	sorted := make([]ScoredSample, len(samples))
	copy(sorted, samples)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Score < sorted[j].Score })

	// The rank of a sample is one plus the number of samples with a lower
	// score. Samples with the same score share their average rank.
	rankSum := 0.0
	for i := 0; i < len(sorted); {
		j := i
		for j+1 < len(sorted) && sorted[j+1].Score == sorted[i].Score {
			j++
		}
		mid := (float64(i+1) + float64(j+1)) / 2
		for k := i; k <= j; k++ {
			if sorted[k].Label {
				rankSum += mid
			}
		}
		i = j + 1
	}
	area := (rankSum - float64(positives*(positives+1))/2) / float64(positives*negatives)
	return area, true
}

// String renders the metrics of one engine as a human-readable block.
func (m Metrics) String() string {
	var b strings.Builder
	model := m.Model
	if model == "" {
		model = "unknown"
	}
	fmt.Fprintf(&b, "engine %s (%s), input mode %s\n", m.Engine, model, m.InputMode)
	fmt.Fprintf(&b, "  samples        %d, decided %d, unlabeled %d\n", m.Samples, m.Decided, m.Unlabeled)
	fmt.Fprintf(&b, "  abstention     %d (%.1f%% of the samples) [unknown %d, failed %d]\n",
		m.Abstention.Count, m.Abstention.Rate*100, m.Abstention.Unknown, m.Abstention.Failed)
	fmt.Fprintf(&b, "  confusion      tp %d, fp %d, tn %d, fn %d\n", m.Counts.TP, m.Counts.FP, m.Counts.TN, m.Counts.FN)
	fmt.Fprintf(&b, "  accuracy       %s\n", metricsPct(m.Accuracy))
	fmt.Fprintf(&b, "  precision      %s\n", metricsPct(m.Precision))
	fmt.Fprintf(&b, "  recall         %s\n", metricsPct(m.Recall))
	fmt.Fprintf(&b, "  f1 / f2 / f3   %s / %s / %s\n", metricsPct(m.F1), metricsPct(m.F2), metricsPct(m.F3))
	if m.ROCAUC != nil {
		fmt.Fprintf(&b, "  roc-auc        %.4f\n", *m.ROCAUC)
	} else {
		fmt.Fprintf(&b, "  roc-auc        n/a (%s)\n", m.ROCAUCReason)
	}
	if m.Patched.Applicable {
		fmt.Fprintf(&b, "  patched fpr    %s (%d false positives over %d decided patched samples)\n",
			metricsPct(m.Patched.FPR), m.Patched.FalsePositives, m.Patched.Decided)
	} else {
		fmt.Fprintf(&b, "  patched fpr    n/a (the corpus holds no patched sample)\n")
	}
	fmt.Fprintf(&b, "  null baseline  always_vulnerable %s, always_safe %s\n",
		metricsPct(m.NullBaselines.AlwaysVulnerable.Accuracy),
		metricsPct(m.NullBaselines.AlwaysSafe.Accuracy))
	if len(m.PerCWE) > 0 {
		shown := len(m.PerCWE)
		if shown > 10 {
			shown = 10
		}
		fmt.Fprintf(&b, "  per-cwe        %d groups, the %d with the most support:\n", len(m.PerCWE), shown)
		for _, g := range m.PerCWE[:shown] {
			fmt.Fprintf(&b, "    %-22s support %4d  decided %4d  p %7s  r %7s  f1 %7s\n",
				g.CWE, g.Support, g.Decided, metricsPct(g.Precision), metricsPct(g.Recall), metricsPct(g.F1))
		}
	}
	return b.String()
}

// String renders the metrics of a whole run as a human-readable block.
func (d MetricsDocument) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "benchmark metrics (schema %s): %d rows, %d samples (%d vulnerable, %d safe)\n",
		d.SchemaVersion, d.Corpus.Rows, d.Corpus.Samples, d.Corpus.Vulnerable, d.Corpus.Safe)
	for _, e := range d.Engines {
		b.WriteString(e.String())
	}
	fmt.Fprintf(&b, "null baselines over the whole corpus: always_vulnerable %s accuracy, always_safe %s accuracy\n",
		metricsPct(d.NullBaselines.AlwaysVulnerable.Accuracy),
		metricsPct(d.NullBaselines.AlwaysSafe.Accuracy))
	return b.String()
}

// metricsLabeledSamples returns every sample of the corpus whose ground
// truth is a class that can be scored.
//
// The samples are derived from the layout of the input alone, not from an
// engine, so that the corpus statistics and the null baselines do not
// depend on the engines of the run.
func metricsLabeledSamples(rep *Report) []EvaluationSample {
	if rep == nil {
		return nil
	}
	paired := rep.Schema.Mode == ModePaired
	cweCol := metricsCWEColumn(rep.Header)
	out := make([]EvaluationSample, 0, len(rep.Rows)*2)
	for i := range rep.Rows {
		cwes := metricsRowCWEs(rep, cweCol, i)
		if paired {
			out = append(out, EvaluationSample{
				Index:  len(out),
				Row:    i,
				Side:   SampleSideBefore,
				Actual: ClassificationVulnerable,
				CWEs:   cwes,
			})
			out = append(out, EvaluationSample{
				Index:  len(out),
				Row:    i,
				Side:   SampleSideAfter,
				Actual: ClassificationSafe,
				CWEs:   cwes,
			})
			continue
		}
		actual := ""
		if i < len(rep.ExpectedLabels) {
			actual = rep.ExpectedLabels[i]
		}
		out = append(out, EvaluationSample{
			Index:  len(out),
			Row:    i,
			Side:   SampleSideSingle,
			Actual: actual,
			CWEs:   cwes,
		})
	}
	labeled := out[:0]
	for _, s := range out {
		if s.Actual == ClassificationVulnerable || s.Actual == ClassificationSafe {
			labeled = append(labeled, s)
		}
	}
	return labeled
}

// metricsCorpusStats describes the corpus of a report.
func metricsCorpusStats(rep *Report) CorpusStats {
	stats := CorpusStats{Rows: len(rep.Rows)}
	if rep != nil {
		stats.Path = rep.InputPath
		stats.SHA256 = rep.InputSHA256
	}
	for _, s := range metricsLabeledSamples(rep) {
		stats.Samples++
		if s.Actual == ClassificationVulnerable {
			stats.Vulnerable++
		} else {
			stats.Safe++
		}
	}
	return stats
}

// metricsPatchedCorpus computes the false-positive rate on the patched
// side of the corpus.
func metricsPatchedCorpus(samples, decided []EvaluationSample) PatchedCorpus {
	out := PatchedCorpus{}
	for _, s := range samples {
		if s.Actual != ClassificationSafe {
			continue
		}
		out.Samples++
	}
	if out.Samples == 0 {
		return out
	}
	out.Applicable = true
	for _, s := range decided {
		if s.Actual != ClassificationSafe {
			continue
		}
		out.Decided++
		if s.Predicted == ClassificationVulnerable {
			out.FalsePositives++
		}
	}
	if out.Decided > 0 {
		out.FPR = float64(out.FalsePositives) / float64(out.Decided)
	}
	return out
}

// metricsPerCWE groups the samples by vulnerability family.
func metricsPerCWE(samples, decided []EvaluationSample) []CWEMetrics {
	type group struct {
		support int
		decided int
		counts  ConfusionMatrix
	}
	groups := map[string]*group{}
	order := []string{}
	touch := func(cwe string) *group {
		g, ok := groups[cwe]
		if !ok {
			g = &group{}
			groups[cwe] = g
			order = append(order, cwe)
		}
		return g
	}
	for _, s := range samples {
		for _, cwe := range s.CWEs {
			touch(cwe).support++
		}
	}
	for _, s := range decided {
		for _, cwe := range s.CWEs {
			g := touch(cwe)
			g.decided++
			switch {
			case s.Actual == ClassificationVulnerable && s.Predicted == ClassificationVulnerable:
				g.counts.TP++
			case s.Actual == ClassificationSafe && s.Predicted == ClassificationVulnerable:
				g.counts.FP++
			case s.Actual == ClassificationSafe && s.Predicted == ClassificationSafe:
				g.counts.TN++
			default:
				g.counts.FN++
			}
		}
	}
	out := make([]CWEMetrics, 0, len(groups))
	for _, cwe := range order {
		g := groups[cwe]
		out = append(out, CWEMetrics{
			CWE:       cwe,
			Support:   g.support,
			Decided:   g.decided,
			Counts:    g.counts,
			Accuracy:  g.counts.Accuracy(),
			Precision: g.counts.Precision(),
			Recall:    g.counts.Recall(),
			F1:        g.counts.F1(),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Support != out[j].Support {
			return out[i].Support > out[j].Support
		}
		return out[i].CWE < out[j].CWE
	})
	return out
}

// metricsNullBaselines evaluates the two trivial classifiers over a set of
// samples.
//
// The baselines always answer, so they never abstain: they are the floor
// the metrics of an engine are read against.
func metricsNullBaselines(samples []EvaluationSample) NullBaselines {
	return NullBaselines{
		AlwaysVulnerable: metricsBaseline("always_vulnerable", samples, ClassificationVulnerable),
		AlwaysSafe:       metricsBaseline("always_safe", samples, ClassificationSafe),
	}
}

// metricsBaseline evaluates a constant classifier over a set of samples.
func metricsBaseline(name string, samples []EvaluationSample, prediction string) BaselineMetrics {
	out := BaselineMetrics{Name: name}
	for _, s := range samples {
		if s.Actual != ClassificationVulnerable && s.Actual != ClassificationSafe {
			continue
		}
		switch {
		case s.Actual == ClassificationVulnerable && prediction == ClassificationVulnerable:
			out.Counts.TP++
		case s.Actual == ClassificationSafe && prediction == ClassificationVulnerable:
			out.Counts.FP++
		case s.Actual == ClassificationSafe && prediction == ClassificationSafe:
			out.Counts.TN++
		default:
			out.Counts.FN++
		}
	}
	out.Accuracy = out.Counts.Accuracy()
	out.Precision = out.Counts.Precision()
	out.Recall = out.Counts.Recall()
	out.F1 = out.Counts.F1()
	out.F2 = out.Counts.FBeta(2)
	out.F3 = out.Counts.FBeta(3)
	return out
}

// metricsEngineModel returns the model name an engine recorded.
//
// It is the identifier the CSV carries in its model column: the model name
// for the LLM engine, "aisai-sast(min-severity=...)" for the static one
// and "baseline:<name>" for a baseline.
func metricsEngineModel(rep *Report, eng *EngineReport) string {
	if eng == nil {
		return ""
	}
	paired := rep != nil && rep.Schema.Mode == ModePaired
	for _, row := range eng.Rows {
		if paired {
			if model := metricsRowModel(row.Before, row.After); model != "" && model != "unknown" {
				return model
			}
			continue
		}
		if model := metricsRowModelSingle(row.Single); model != "" && model != "unknown" {
			return model
		}
	}
	return ""
}

// metricsRowModel returns the model name recorded for a paired row.
func metricsRowModel(before, after Outcome) string {
	if before.Model != "" {
		return before.Model
	}
	if after.Model != "" {
		return after.Model
	}
	return "unknown"
}

// metricsRowModelSingle returns the model name recorded for a single-mode
// row.
func metricsRowModelSingle(o Outcome) string {
	if o.Model != "" {
		return o.Model
	}
	return "unknown"
}

// metricsCWEColumn returns the index of the vulnerability-family column of
// a header, or -1 when the header has none.
func metricsCWEColumn(header []string) int {
	for _, candidate := range metricsCWEColumns {
		for i, c := range header {
			if c == candidate {
				return i
			}
		}
	}
	return -1
}

// metricsRowCWEs returns the vulnerability families of a row.
func metricsRowCWEs(rep *Report, column, row int) []string {
	if rep == nil || column < 0 || row < 0 || row >= len(rep.Rows) || column >= len(rep.Rows[row]) {
		return nil
	}
	return metricsSplitCWEs(rep.Rows[row][column])
}

// metricsSplitCWEs splits a vulnerability-family cell into its families.
//
// The corpus writes several families in one cell, separated by commas
// ("CWE-20,CWE-918"). An empty cell, or a cell holding a placeholder such
// as "N/A", yields the single group CWEUnspecified.
func metricsSplitCWEs(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return strings.ContainsRune(metricsCWESeparators, r)
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		switch strings.ToLower(f) {
		case "na", "n/a", "none", "nan", "unknown":
			f = CWEUnspecified
		}
		out = append(out, f)
	}
	if len(out) == 0 {
		return []string{CWEUnspecified}
	}
	return out
}

// metricsCell returns the value of a column of an input row.
func metricsCell(rep *Report, row int, column string) string {
	if rep == nil || column == "" || row < 0 || row >= len(rep.Rows) {
		return ""
	}
	for i, c := range rep.Header {
		if c == column && i < len(rep.Rows[row]) {
			return rep.Rows[row][i]
		}
	}
	return ""
}

// metricsPct renders a ratio as a percentage with one decimal.
func metricsPct(v float64) string {
	return fmt.Sprintf("%.1f%%", v*100)
}
