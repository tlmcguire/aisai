package bench

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

// outcome builds an outcome for the tests.
//
// The Correct field is deliberately left false: the metrics must be
// computed from the ground truth, not read back from the CSV.
func outcome(class string, completed bool) Outcome {
	return Outcome{Classification: class, Completed: completed}
}

// pairedReport builds a paired report with one engine.
func pairedReport(header []string, rows [][]string, outcomes []EngineRow) *Report {
	return &Report{
		Schema: InputSchema{Mode: ModePaired, BeforeCol: "vulnerable_code", AfterCol: "fixed_code"},
		Header: header,
		Rows:   rows,
		Engines: []*EngineReport{{
			Prefix: PrefixStatic,
			Rows:   outcomes,
		}},
	}
}

// pair is a shorthand for the two outcomes of a paired row.
func pair(before, after string) EngineRow {
	return EngineRow{Before: outcome(before, true), After: outcome(after, true)}
}

// floatAlmostEqual reports whether two ratios agree to twelve decimals.
func floatAlmostEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-12
}

func TestConfusionMatrixHandComputed(t *testing.T) {
	// 3 true positives, 1 false positive, 4 true negatives, 2 false negatives.
	c := ConfusionMatrix{TP: 3, FP: 1, TN: 4, FN: 2}

	if got := c.Total(); got != 10 {
		t.Errorf("total = %d, want 10", got)
	}
	// 7 of the 10 samples are right.
	if got := c.Accuracy(); !floatAlmostEqual(got, 0.7) {
		t.Errorf("accuracy = %v, want 0.7", got)
	}
	// 3 of the 4 flagged samples are really vulnerable.
	if got := c.Precision(); !floatAlmostEqual(got, 0.75) {
		t.Errorf("precision = %v, want 0.75", got)
	}
	// 3 of the 5 vulnerable samples were found.
	if got := c.Recall(); !floatAlmostEqual(got, 0.6) {
		t.Errorf("recall = %v, want 0.6", got)
	}
	// 2*TP / (2*TP + FP + FN) = 6 / 9.
	if got := c.F1(); !floatAlmostEqual(got, 2.0/3.0) {
		t.Errorf("f1 = %v, want %v", got, 2.0/3.0)
	}
	// (1+4)*0.75*0.6 / (4*0.75+0.6) = 2.25 / 3.6.
	if got := c.FBeta(2); !floatAlmostEqual(got, 0.625) {
		t.Errorf("f2 = %v, want 0.625", got)
	}
	// (1+9)*0.75*0.6 / (9*0.75+0.6) = 4.5 / 7.35.
	if got := c.FBeta(3); !floatAlmostEqual(got, 4.5/7.35) {
		t.Errorf("f3 = %v, want %v", got, 4.5/7.35)
	}
}

func TestConfusionMatrixFBetaOrdering(t *testing.T) {
	// The recall is below the precision, so the recall-weighted scores
	// must fall below F1.
	c := ConfusionMatrix{TP: 3, FP: 1, TN: 0, FN: 6}
	if c.Recall() >= c.Precision() {
		t.Fatalf("the test needs a recall below the precision: r=%v p=%v", c.Recall(), c.Precision())
	}
	f1, f2, f3 := c.F1(), c.FBeta(2), c.FBeta(3)
	if !(f3 <= f2 && f2 <= f1) {
		t.Errorf("f3=%v f2=%v f1=%v: want f3 <= f2 <= f1", f3, f2, f1)
	}
	// The recall is above the precision, so the recall-weighted scores
	// must rise above F1.
	c = ConfusionMatrix{TP: 6, FP: 3, TN: 0, FN: 1}
	f1, f2, f3 = c.F1(), c.FBeta(2), c.FBeta(3)
	if !(f3 >= f2 && f2 >= f1) {
		t.Errorf("f3=%v f2=%v f1=%v: want f3 >= f2 >= f1", f3, f2, f1)
	}
	// An exact check of the F3 formula on the first matrix.
	c = ConfusionMatrix{TP: 3, FP: 1, TN: 4, FN: 2}
	want := (1 + 9.0) * c.Precision() * c.Recall() / (9*c.Precision() + c.Recall())
	if got := c.FBeta(3); !floatAlmostEqual(got, want) {
		t.Errorf("f3 = %v, want %v", got, want)
	}
}

func TestConfusionMatrixZeroDenominators(t *testing.T) {
	cases := []struct {
		name string
		c    ConfusionMatrix
	}{
		{"empty", ConfusionMatrix{}},
		{"no predicted positive", ConfusionMatrix{TN: 5, FN: 3}},
		{"no actual positive", ConfusionMatrix{FP: 2, TN: 3}},
		{"nothing but false positives", ConfusionMatrix{FP: 4}},
	}
	for _, tc := range cases {
		values := map[string]float64{
			"precision": tc.c.Precision(),
			"recall":    tc.c.Recall(),
			"f1":        tc.c.F1(),
			"f2":        tc.c.FBeta(2),
			"f3":        tc.c.FBeta(3),
		}
		for name, v := range values {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				t.Errorf("%s: %s = %v, want a finite number", tc.name, name, v)
			}
			if v != 0 {
				t.Errorf("%s: %s = %v, want 0", tc.name, name, v)
			}
		}
	}
	// The accuracy has its own denominator and is not zero when the
	// matrix is not empty.
	if got := (ConfusionMatrix{TN: 5, FN: 3}).Accuracy(); !floatAlmostEqual(got, 0.625) {
		t.Errorf("accuracy = %v, want 0.625", got)
	}
	if got := (ConfusionMatrix{}).Accuracy(); got != 0 {
		t.Errorf("accuracy of an empty matrix = %v, want 0", got)
	}
}

func TestEvaluationSamplesPairedOrder(t *testing.T) {
	rep := pairedReport(
		[]string{"cve_id", "vulnerable_code", "fixed_code"},
		[][]string{{"CVE-1", "bad", "good"}, {"CVE-2", "bad2", "good2"}},
		[]EngineRow{pair(ClassificationVulnerable, ClassificationSafe), pair(ClassificationSafe, ClassificationVulnerable)},
	)
	samples := EvaluationSamples(rep, rep.Engines[0])
	if len(samples) != 4 {
		t.Fatalf("got %d samples, want 4", len(samples))
	}
	want := []struct {
		side   string
		actual string
		row    int
	}{
		{SampleSideBefore, ClassificationVulnerable, 0},
		{SampleSideAfter, ClassificationSafe, 0},
		{SampleSideBefore, ClassificationVulnerable, 1},
		{SampleSideAfter, ClassificationSafe, 1},
	}
	for i, w := range want {
		s := samples[i]
		if s.Side != w.side || s.Actual != w.actual || s.Row != w.row {
			t.Errorf("sample %d = %s/%s/row %d, want %s/%s/row %d",
				i, s.Side, s.Actual, s.Row, w.side, w.actual, w.row)
		}
		if s.Index != i {
			t.Errorf("sample %d has index %d", i, s.Index)
		}
	}
}

func TestComputeEngineMetricsConfusion(t *testing.T) {
	rep := pairedReport(
		[]string{"cve_id", "vulnerable_code", "fixed_code"},
		[][]string{
			{"CVE-1", "v1", "f1"},
			{"CVE-2", "v2", "f2"},
			{"CVE-3", "v3", "f3"},
		},
		[]EngineRow{
			pair(ClassificationVulnerable, ClassificationSafe),
			// The second vulnerable snippet could not be decided.
			{Before: outcome(ClassificationUnknown, false), After: outcome(ClassificationSafe, true)},
			// The third row is the other way round.
			pair(ClassificationSafe, ClassificationVulnerable),
		},
	)
	m := ComputeEngineMetrics(rep, rep.Engines[0])

	if m.Samples != 6 {
		t.Errorf("samples = %d, want 6", m.Samples)
	}
	if m.Decided != 5 {
		t.Errorf("decided = %d, want 5", m.Decided)
	}
	want := ConfusionMatrix{TP: 1, FP: 1, TN: 2, FN: 1}
	if m.Counts != want {
		t.Errorf("confusion matrix = %+v, want %+v", m.Counts, want)
	}
	if !floatAlmostEqual(m.Precision, 0.5) {
		t.Errorf("precision = %v, want 0.5", m.Precision)
	}
	if !floatAlmostEqual(m.Recall, 0.5) {
		t.Errorf("recall = %v, want 0.5", m.Recall)
	}
	if !floatAlmostEqual(m.F1, 0.5) {
		t.Errorf("f1 = %v, want 0.5", m.F1)
	}
	if !floatAlmostEqual(m.Accuracy, 0.6) {
		t.Errorf("accuracy = %v, want 0.6", m.Accuracy)
	}
}

func TestComputeEngineMetricsAbstention(t *testing.T) {
	rep := pairedReport(
		[]string{"cve_id", "vulnerable_code", "fixed_code"},
		[][]string{{"CVE-1", "v1", "f1"}, {"CVE-2", "v2", "f2"}},
		[]EngineRow{
			{Before: outcome(ClassificationUnknown, false), After: outcome(ClassificationUnknown, false)},
			pair(ClassificationVulnerable, ClassificationSafe),
		},
	)
	m := ComputeEngineMetrics(rep, rep.Engines[0])

	if m.Abstention.Count != 2 {
		t.Errorf("abstention count = %d, want 2", m.Abstention.Count)
	}
	if !floatAlmostEqual(m.Abstention.Rate, 0.5) {
		t.Errorf("abstention rate = %v, want 0.5", m.Abstention.Rate)
	}
	if m.Abstention.Unknown != 2 {
		t.Errorf("unknown count = %d, want 2", m.Abstention.Unknown)
	}
	if m.Abstention.Failed != 2 {
		t.Errorf("failed count = %d, want 2", m.Abstention.Failed)
	}
	// The abstained samples are excluded from the confusion matrix: only
	// the second row counts.
	if m.Decided != 2 {
		t.Errorf("decided = %d, want 2", m.Decided)
	}
	want := ConfusionMatrix{TP: 1, TN: 1}
	if m.Counts != want {
		t.Errorf("confusion matrix = %+v, want %+v", m.Counts, want)
	}
	// An unknown sample is never counted as a false negative.
	if m.Counts.FN != 0 {
		t.Errorf("false negatives = %d, want 0", m.Counts.FN)
	}
}

func TestComputeEngineMetricsCompletedUnknownIsAbstention(t *testing.T) {
	rep := pairedReport(
		[]string{"cve_id", "vulnerable_code", "fixed_code"},
		[][]string{{"CVE-1", "v1", "f1"}},
		[]EngineRow{
			{Before: outcome(ClassificationUnknown, true), After: outcome(ClassificationSafe, true)},
		},
	)
	m := ComputeEngineMetrics(rep, rep.Engines[0])
	if m.Abstention.Count != 1 {
		t.Errorf("abstention count = %d, want 1", m.Abstention.Count)
	}
	if m.Abstention.Unknown != 1 {
		t.Errorf("unknown count = %d, want 1", m.Abstention.Unknown)
	}
	if m.Abstention.Failed != 0 {
		t.Errorf("failed count = %d, want 0", m.Abstention.Failed)
	}
}

func TestPatchedCorpusFPR(t *testing.T) {
	// Four rows: the engine flags exactly one patched snippet.
	outcomes := []EngineRow{
		pair(ClassificationSafe, ClassificationVulnerable),
		pair(ClassificationSafe, ClassificationSafe),
		pair(ClassificationSafe, ClassificationSafe),
		pair(ClassificationSafe, ClassificationSafe),
	}
	rep := pairedReport(
		[]string{"cve_id", "vulnerable_code", "fixed_code"},
		[][]string{{"CVE-1", "v1", "f1"}, {"CVE-2", "v2", "f2"}, {"CVE-3", "v3", "f3"}, {"CVE-4", "v4", "f4"}},
		outcomes,
	)
	m := ComputeEngineMetrics(rep, rep.Engines[0])

	if !m.Patched.Applicable {
		t.Fatal("the patched corpus must be applicable in paired mode")
	}
	if m.Patched.Samples != 4 {
		t.Errorf("patched samples = %d, want 4", m.Patched.Samples)
	}
	if m.Patched.Decided != 4 {
		t.Errorf("decided patched samples = %d, want 4", m.Patched.Decided)
	}
	if m.Patched.FalsePositives != 1 {
		t.Errorf("false positives = %d, want 1", m.Patched.FalsePositives)
	}
	if !floatAlmostEqual(m.Patched.FPR, 0.25) {
		t.Errorf("fpr = %v, want 0.25", m.Patched.FPR)
	}
	// The false positive of the confusion matrix is the same sample.
	if m.Counts.FP != 1 {
		t.Errorf("false positives of the confusion matrix = %d, want 1", m.Counts.FP)
	}
}

func TestPatchedCorpusFPRIgnoresAbstainedSamples(t *testing.T) {
	outcomes := []EngineRow{
		pair(ClassificationSafe, ClassificationVulnerable),
		{Before: outcome(ClassificationSafe, true), After: outcome(ClassificationUnknown, false)},
	}
	rep := pairedReport(
		[]string{"cve_id", "vulnerable_code", "fixed_code"},
		[][]string{{"CVE-1", "v1", "f1"}, {"CVE-2", "v2", "f2"}},
		outcomes,
	)
	m := ComputeEngineMetrics(rep, rep.Engines[0])
	if m.Patched.Samples != 2 {
		t.Errorf("patched samples = %d, want 2", m.Patched.Samples)
	}
	if m.Patched.Decided != 1 {
		t.Errorf("decided patched samples = %d, want 1", m.Patched.Decided)
	}
	if !floatAlmostEqual(m.Patched.FPR, 1) {
		t.Errorf("fpr = %v, want 1", m.Patched.FPR)
	}
}

func TestPerCWEGrouping(t *testing.T) {
	header := []string{"cve_id", ColumnVulnerabilityCWEIDs, "vulnerable_code", "fixed_code"}
	rows := [][]string{
		{"CVE-1", "CWE-79", "v1", "f1"},
		{"CVE-2", "CWE-20,CWE-918", "v2", "f2"},
		{"CVE-3", "", "v3", "f3"},
		{"CVE-4", "N/A", "v4", "f4"},
	}
	outcomes := []EngineRow{
		pair(ClassificationVulnerable, ClassificationSafe),
		pair(ClassificationVulnerable, ClassificationVulnerable),
		pair(ClassificationSafe, ClassificationSafe),
		pair(ClassificationSafe, ClassificationSafe),
	}
	rep := pairedReport(header, rows, outcomes)
	m := ComputeEngineMetrics(rep, rep.Engines[0])

	groups := map[string]CWEMetrics{}
	for _, g := range m.PerCWE {
		groups[g.CWE] = g
	}
	for _, want := range []struct {
		cwe     string
		support int
		decided int
		tp      int
		fp      int
	}{
		{"CWE-79", 2, 2, 1, 0},
		{"CWE-20", 2, 2, 1, 1},
		{"CWE-918", 2, 2, 1, 1},
		{CWEUnspecified, 4, 4, 0, 0},
	} {
		g, ok := groups[want.cwe]
		if !ok {
			t.Errorf("the group %q is missing", want.cwe)
			continue
		}
		if g.Support != want.support {
			t.Errorf("%s: support = %d, want %d", want.cwe, g.Support, want.support)
		}
		if g.Decided != want.decided {
			t.Errorf("%s: decided = %d, want %d", want.cwe, g.Decided, want.decided)
		}
		if g.Counts.TP != want.tp || g.Counts.FP != want.fp {
			t.Errorf("%s: counts = %+v, want tp %d and fp %d", want.cwe, g.Counts, want.tp, want.fp)
		}
	}
	if len(m.PerCWE) != 4 {
		t.Errorf("got %d groups, want 4", len(m.PerCWE))
	}
	// The groups are ordered by support, then by name.
	if m.PerCWE[0].CWE != CWEUnspecified {
		t.Errorf("the first group is %q, want %q", m.PerCWE[0].CWE, CWEUnspecified)
	}
}

func TestNullBaselinesOnABalancedCorpus(t *testing.T) {
	rep := pairedReport(
		[]string{"cve_id", "vulnerable_code", "fixed_code"},
		[][]string{{"CVE-1", "v1", "f1"}, {"CVE-2", "v2", "f2"}},
		[]EngineRow{
			pair(ClassificationVulnerable, ClassificationSafe),
			pair(ClassificationVulnerable, ClassificationSafe),
		},
	)
	m := ComputeEngineMetrics(rep, rep.Engines[0])

	// Four decided samples, two of them vulnerable: both null classifiers
	// score exactly 50%.
	if !floatAlmostEqual(m.NullBaselines.AlwaysVulnerable.Accuracy, 0.5) {
		t.Errorf("always_vulnerable accuracy = %v, want 0.5", m.NullBaselines.AlwaysVulnerable.Accuracy)
	}
	if !floatAlmostEqual(m.NullBaselines.AlwaysSafe.Accuracy, 0.5) {
		t.Errorf("always_safe accuracy = %v, want 0.5", m.NullBaselines.AlwaysSafe.Accuracy)
	}
	if !floatAlmostEqual(m.NullBaselines.AlwaysVulnerable.Recall, 1) {
		t.Errorf("always_vulnerable recall = %v, want 1", m.NullBaselines.AlwaysVulnerable.Recall)
	}
	if !floatAlmostEqual(m.NullBaselines.AlwaysSafe.Recall, 0) {
		t.Errorf("always_safe recall = %v, want 0", m.NullBaselines.AlwaysSafe.Recall)
	}
	// The engine answered every snippet correctly, so it beats the floor.
	if !floatAlmostEqual(m.Accuracy, 1) {
		t.Errorf("accuracy = %v, want 1", m.Accuracy)
	}
	if m.Accuracy <= m.NullBaselines.AlwaysVulnerable.Accuracy {
		t.Errorf("accuracy %v must be above the null baseline %v",
			m.Accuracy, m.NullBaselines.AlwaysVulnerable.Accuracy)
	}
}

func TestROCAUC(t *testing.T) {
	cases := []struct {
		name    string
		samples []ScoredSample
		want    float64
		ok      bool
	}{
		{
			name: "perfect separation",
			samples: []ScoredSample{
				{Label: true, Score: 0.9},
				{Label: true, Score: 0.8},
				{Label: false, Score: 0.2},
				{Label: false, Score: 0.1},
			},
			want: 1,
			ok:   true,
		},
		{
			name: "inverted",
			samples: []ScoredSample{
				{Label: true, Score: 0.1},
				{Label: true, Score: 0.2},
				{Label: false, Score: 0.8},
				{Label: false, Score: 0.9},
			},
			want: 0,
			ok:   true,
		},
		{
			name: "every score is the same",
			samples: []ScoredSample{
				{Label: true, Score: 0.5},
				{Label: true, Score: 0.5},
				{Label: false, Score: 0.5},
				{Label: false, Score: 0.5},
			},
			want: 0.5,
			ok:   true,
		},
		{
			name: "one tie",
			samples: []ScoredSample{
				{Label: true, Score: 0.5},
				{Label: false, Score: 0.5},
				{Label: false, Score: 0.1},
			},
			// The tied positive beats one negative and ties with the other.
			want: 0.75,
			ok:   true,
		},
		{
			name: "no positive",
			samples: []ScoredSample{
				{Label: false, Score: 0.5},
			},
			want: 0,
			ok:   false,
		},
		{
			name: "empty",
			want: 0,
			ok:   false,
		},
	}
	for _, tc := range cases {
		got, ok := ROCAUC(tc.samples)
		if ok != tc.ok {
			t.Errorf("%s: ok = %v, want %v", tc.name, ok, tc.ok)
			continue
		}
		if !ok {
			continue
		}
		if !floatAlmostEqual(got, tc.want) {
			t.Errorf("%s: auc = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestROCAUCAgainstBruteForce(t *testing.T) {
	samples := []ScoredSample{
		{Label: true, Score: 0.3},
		{Label: false, Score: 0.3},
		{Label: true, Score: 0.3},
		{Label: false, Score: 0.9},
		{Label: true, Score: 0.1},
		{Label: false, Score: 0.4},
	}
	positives, negatives := 0, 0
	for _, s := range samples {
		if s.Label {
			positives++
		} else {
			negatives++
		}
	}
	sum := 0.0
	for _, p := range samples {
		if !p.Label {
			continue
		}
		for _, n := range samples {
			if n.Label {
				continue
			}
			switch {
			case p.Score > n.Score:
				sum++
			case p.Score == n.Score:
				sum += 0.5
			}
		}
	}
	want := sum / float64(positives*negatives)
	got, ok := ROCAUC(samples)
	if !ok {
		t.Fatal("the area must be defined")
	}
	if !floatAlmostEqual(got, want) {
		t.Errorf("auc = %v, want %v", got, want)
	}
}

func TestROCAUCIsNotApplicableWithoutScores(t *testing.T) {
	rep := pairedReport(
		[]string{"cve_id", "vulnerable_code", "fixed_code"},
		[][]string{{"CVE-1", "v1", "f1"}},
		[]EngineRow{pair(ClassificationVulnerable, ClassificationSafe)},
	)
	m := ComputeEngineMetrics(rep, rep.Engines[0])
	if m.ROCAUC != nil {
		t.Errorf("roc-auc = %v, want nil", *m.ROCAUC)
	}
	if m.ROCAUCReason == "" {
		t.Error("the reason why the roc-auc is not applicable must be recorded")
	}
}

func TestROCAUCWithScores(t *testing.T) {
	rep := pairedReport(
		[]string{"cve_id", "vulnerable_code", "fixed_code"},
		[][]string{{"CVE-1", "v1", "f1"}, {"CVE-2", "v2", "f2"}},
		[]EngineRow{
			pair(ClassificationVulnerable, ClassificationSafe),
			pair(ClassificationVulnerable, ClassificationSafe),
		},
	)
	// The samples are before, after, before, after: the two vulnerable
	// snippets score high, the two patched ones low.
	scores := []float64{0.9, 0.1, 0.8, 0.2}
	m := ComputeEngineMetricsWithScores(rep, rep.Engines[0], scores)
	if m.ROCAUC == nil {
		t.Fatalf("roc-auc must be computed, reason: %s", m.ROCAUCReason)
	}
	if !floatAlmostEqual(*m.ROCAUC, 1) {
		t.Errorf("roc-auc = %v, want 1", *m.ROCAUC)
	}
}

func TestMetricsDocumentJSON(t *testing.T) {
	rep := pairedReport(
		[]string{"cve_id", ColumnVulnerabilityCWEIDs, "vulnerable_code", "fixed_code"},
		[][]string{{"CVE-1", "CWE-79", "v1", "f1"}},
		[]EngineRow{pair(ClassificationVulnerable, ClassificationSafe)},
	)
	doc := BuildMetricsDocument(rep, nil)

	if doc.SchemaVersion != MetricsSchemaVersion {
		t.Errorf("schema version = %q, want %q", doc.SchemaVersion, MetricsSchemaVersion)
	}
	if len(doc.Engines) != 1 {
		t.Fatalf("got %d engines, want 1", len(doc.Engines))
	}
	if doc.Corpus.Samples != 2 || doc.Corpus.Vulnerable != 1 || doc.Corpus.Safe != 1 {
		t.Errorf("corpus stats = %+v", doc.Corpus)
	}
	// The null baselines of the corpus are 50% on a balanced corpus.
	if !floatAlmostEqual(doc.NullBaselines.AlwaysVulnerable.Accuracy, 0.5) {
		t.Errorf("always_vulnerable accuracy = %v, want 0.5", doc.NullBaselines.AlwaysVulnerable.Accuracy)
	}

	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if raw["schema_version"] != MetricsSchemaVersion {
		t.Errorf("schema_version = %v", raw["schema_version"])
	}
	engines, ok := raw["engines"].([]any)
	if !ok || len(engines) != 1 {
		t.Fatalf("engines = %v", raw["engines"])
	}
	engine, ok := engines[0].(map[string]any)
	if !ok {
		t.Fatalf("engine = %v", engines[0])
	}
	for _, key := range []string{
		"engine", "model", "samples", "decided", "confusion_matrix", "precision",
		"recall", "f1", "f2", "f3", "roc_auc", "abstention", "per_cwe",
		"patched_corpus", "null_baselines",
	} {
		if _, ok := engine[key]; !ok {
			t.Errorf("the engine entry is missing %q", key)
		}
	}
	if engine["roc_auc"] != nil {
		t.Errorf("roc_auc = %v, want null", engine["roc_auc"])
	}
}

func TestMetricsStringRendersTheNumbers(t *testing.T) {
	rep := pairedReport(
		[]string{"cve_id", "vulnerable_code", "fixed_code"},
		[][]string{{"CVE-1", "v1", "f1"}, {"CVE-2", "v2", "f2"}},
		[]EngineRow{
			pair(ClassificationVulnerable, ClassificationSafe),
			pair(ClassificationSafe, ClassificationVulnerable),
		},
	)
	m := ComputeEngineMetrics(rep, rep.Engines[0])
	text := m.String()
	for _, want := range []string{"tp 1", "fp 1", "precision", "recall", "f1 / f2 / f3", "patched fpr", "roc-auc", "null baseline"} {
		if !strings.Contains(text, want) {
			t.Errorf("the summary does not mention %q:\n%s", want, text)
		}
	}
	// One false positive over two decided patched samples.
	if !strings.Contains(text, "50.0%") {
		t.Errorf("the summary does not report the false-positive rate:\n%s", text)
	}
}

func TestComputeMetricsCoversEveryEngine(t *testing.T) {
	rep := pairedReport(
		[]string{"cve_id", "vulnerable_code", "fixed_code"},
		[][]string{{"CVE-1", "v1", "f1"}},
		[]EngineRow{pair(ClassificationVulnerable, ClassificationSafe)},
	)
	rep.Engines = append(rep.Engines, &EngineReport{
		Prefix: PrefixLLM,
		Rows:   []EngineRow{pair(ClassificationUnknown, ClassificationSafe)},
	})
	metrics := ComputeMetrics(rep)
	if len(metrics) != 2 {
		t.Fatalf("got %d metrics, want 2", len(metrics))
	}
	if metrics[0].Engine != PrefixStatic || metrics[1].Engine != PrefixLLM {
		t.Errorf("the engines are %q and %q", metrics[0].Engine, metrics[1].Engine)
	}
	if metrics[1].Abstention.Count != 1 {
		t.Errorf("the abstention count = %d, want 1", metrics[1].Abstention.Count)
	}
}
