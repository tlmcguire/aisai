package bench

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTokenizeSplitsIdentifiers(t *testing.T) {
	cases := []struct {
		name string
		code string
		want []string
	}{
		{"underscores", "my_var_name = 1", []string{"my", "var", "name", "=", "num"}},
		{"camel case", "parseHTTPResponse(x)", []string{"parse", "http", "response", "(", "x", ")"}},
		{"dotted", "os.system(cmd)", []string{"os", ".", "system", "(", "cmd", ")"}},
		{"keywords are tokens", "def f(): pass", []string{"def", "f", "(", ")", ":", "pass"}},
	}
	for _, tc := range cases {
		got := Tokenize(tc.code)
		if len(got) != len(tc.want) {
			t.Errorf("%s: tokens = %v, want %v", tc.name, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: tokens = %v, want %v", tc.name, got, tc.want)
				break
			}
		}
	}
}

func TestTokenizeStringLiteral(t *testing.T) {
	got := Tokenize(`x = "hello world"`)
	want := []string{"x", "=", "str", "hello", "world"}
	if len(got) != len(want) {
		t.Fatalf("tokens = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tokens = %v, want %v", got, want)
		}
	}
}

func TestSplitRowsIsDeterministicAndDisjoint(t *testing.T) {
	table, err := ReadTable(corpusPath)
	if err != nil {
		t.Fatalf("read the corpus: %v", err)
	}
	first, err := SplitRows(table, SplitOptions{})
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	second, err := SplitRows(table, SplitOptions{})
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if len(first.Train) != len(second.Train) || len(first.Test) != len(second.Test) {
		t.Fatal("two splits of the same corpus differ in size")
	}
	for i := range first.Test {
		if first.Test[i] != second.Test[i] {
			t.Fatal("two splits of the same corpus differ")
		}
	}
	if len(first.Train)+len(first.Test) != len(table.Rows) {
		t.Errorf("the split covers %d rows, want %d",
			len(first.Train)+len(first.Test), len(table.Rows))
	}

	inTrain := map[int]bool{}
	for _, row := range first.Train {
		if inTrain[row] {
			t.Fatalf("the row %d is in the training split twice", row)
		}
		inTrain[row] = true
	}
	for _, row := range first.Test {
		if inTrain[row] {
			t.Fatalf("the row %d is in both splits", row)
		}
	}
}

func TestSplitRowsKeepsTheRowsOfOneCommitTogether(t *testing.T) {
	table, err := ReadTable(corpusPath)
	if err != nil {
		t.Fatalf("read the corpus: %v", err)
	}
	split, err := SplitRows(table, SplitOptions{})
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	side := map[string]bool{}
	for _, row := range split.Train {
		side[table.Cell(row, DefaultGroupColumn)] = true
	}
	for _, row := range split.Test {
		key := table.Cell(row, DefaultGroupColumn)
		if key == "" {
			continue
		}
		if side[key] {
			t.Fatalf("the commit %q is in both splits", key)
		}
	}
}

func TestSplitRowsHonoursTheSeed(t *testing.T) {
	table, err := ReadTable(corpusPath)
	if err != nil {
		t.Fatalf("read the corpus: %v", err)
	}
	a, err := SplitRows(table, SplitOptions{Seed: 1})
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	b, err := SplitRows(table, SplitOptions{Seed: 2})
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	same := len(a.Test) == len(b.Test)
	if same {
		for i := range a.Test {
			if a.Test[i] != b.Test[i] {
				same = false
				break
			}
		}
	}
	if same {
		t.Error("two different seeds must give two different splits")
	}
}

func TestTrainTFIDFRefusesTheTrainingSnippets(t *testing.T) {
	docs := []Document{
		{Code: "eval(user_input)", Label: ClassificationVulnerable},
		{Code: "value = 1", Label: ClassificationSafe},
	}
	model := TrainTFIDF(docs, TFIDFOptions{})

	out := model.ClassifyOutcome("eval(user_input)")
	if out.Classification != ClassificationUnknown {
		t.Errorf("a training snippet must not be evaluated, got %q", out.Classification)
	}
	if out.Completed {
		t.Error("a training snippet must not be a completed run")
	}
	if out.Error == "" {
		t.Error("the refusal must record the reason")
	}

	out = model.ClassifyOutcome("other(user_input)")
	if out.Classification == ClassificationUnknown {
		t.Error("a held-out snippet must be evaluated")
	}
}

func TestTFIDFKeywordModeIsTheDegenerateControl(t *testing.T) {
	model := TrainTFIDF(nil, TFIDFOptions{Mode: TFIDFModeKeyword})
	cases := map[string]string{
		"eval(user_input)":                ClassificationVulnerable,
		"exec(payload)":                   ClassificationVulnerable,
		"data = pickle.loads(blob)":       ClassificationVulnerable,
		"os.system(cmd)":                  ClassificationVulnerable,
		"subprocess.run(cmd, shell=True)": ClassificationVulnerable,
		"value = 1":                       ClassificationSafe,
		"print('hello')":                  ClassificationSafe,
	}
	for code, want := range cases {
		got, _ := model.Predict(code)
		if got != want {
			t.Errorf("the keyword mode on %q = %q, want %q", code, got, want)
		}
	}
}

func TestTFIDFBaselineIsRegisteredAsABaseline(t *testing.T) {
	SetTFIDFModel(nil)
	if _, err := NewBaselineEngine("tfidf"); err == nil {
		t.Fatal("the tfidf baseline must refuse to run without a trained model")
	}

	model := TrainTFIDF([]Document{
		{Code: "import os\nos.system(cmd)", Label: ClassificationVulnerable},
		{Code: "value = 1", Label: ClassificationSafe},
	}, TFIDFOptions{})
	SetTFIDFModel(model)

	eng, err := NewBaselineEngine("tfidf")
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}
	// The model label carries the configuration of the baseline, so that a
	// result row always says which model produced it.
	if !strings.HasPrefix(eng.Model(), "baseline:tfidf;") {
		t.Errorf("model = %q", eng.Model())
	}
	if eng.Prefix() != PrefixLLM {
		t.Errorf("prefix = %q", eng.Prefix())
	}
	// A snippet of the training split is refused, so that an in-sample
	// result can never be read as a held-out one.
	out := eng.Classify(context.Background(), "import os\nos.system(cmd)")
	if out.Classification != ClassificationUnknown {
		t.Errorf("a training snippet must not be evaluated, got %q", out.Classification)
	}
	out = eng.Classify(context.Background(), "print('held out')")
	if out.Classification == "" || out.Classification == ClassificationUnknown {
		t.Errorf("a held-out snippet must be evaluated, got %q", out.Classification)
	}
	SetTFIDFModel(nil)
}

// TestTFIDFBaselineOnTheCorpus measures the baseline on the held-out split
// of the corpus.
//
// The numbers are printed so that a run of the test is a measurement: the
// test is the harness of the baseline, and it is the only place where the
// held-out numbers of the corpus are produced.
func TestTFIDFBaselineOnTheCorpus(t *testing.T) {
	if _, err := os.Stat(corpusPath); err != nil {
		t.Skipf("the corpus is not available: %v", err)
	}
	table, err := ReadTable(corpusPath)
	if err != nil {
		t.Fatalf("read the corpus: %v", err)
	}
	schema, err := DetectSchema(table.Header, "", "", "", "")
	if err != nil {
		t.Fatalf("detect the schema: %v", err)
	}
	split, err := SplitRows(table, SplitOptions{})
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	docs := documentsOf(table, schema, split.Train)
	model := TrainTFIDF(docs, TFIDFOptions{})
	model.split = split
	ev := EvaluateTFIDF(table, schema, model, split)

	t.Logf("corpus: %s", corpusPath)
	t.Logf("rows: %d (train %d, test %d)", len(table.Rows), len(split.Train), len(split.Test))
	t.Logf("seed: %d, test fraction: %.2f, group column: %s", split.Seed, split.TestFraction, split.GroupColumn)
	t.Logf("snippets shared between the splits: %d", split.SharedSnippets)
	t.Logf("vocabulary: %d features", model.VocabularySize())
	t.Logf("held-out: TP %d, FP %d, FN %d, TN %d", ev.TP, ev.FP, ev.FN, ev.TN)
	t.Logf("held-out accuracy: %.4f, precision %.4f, recall %.4f, F1 %.4f",
		ev.Accuracy, ev.Precision, ev.Recall, ev.F1)
	t.Logf("in-sample accuracy (training split): %.4f", ev.TrainAccuracy)

	if ev.TestSnippets != len(split.Test)*2 {
		t.Errorf("the evaluation covered %d snippets, want %d", ev.TestSnippets, len(split.Test)*2)
	}
	if ev.TP+ev.FN != len(split.Test) {
		t.Errorf("the vulnerable snippets do not add up: %d + %d, want %d",
			ev.TP, ev.FN, len(split.Test))
	}
	if ev.FP+ev.TN != len(split.Test) {
		t.Errorf("the fixed snippets do not add up: %d + %d, want %d",
			ev.FP, ev.TN, len(split.Test))
	}
	if ev.Accuracy <= 0.5 {
		t.Errorf("the held-out accuracy is %.4f, which is not better than a coin flip", ev.Accuracy)
	}
	if ev.TrainAccuracy < ev.Accuracy {
		t.Errorf("the in-sample accuracy %.4f is below the held-out accuracy %.4f",
			ev.TrainAccuracy, ev.Accuracy)
	}
}

// TestTFIDFIsStableAcrossSeeds checks that the number the baseline produces
// is a measurement and not an artefact of one particular split.
//
// The classifier is deterministic, so the only thing that changes with the
// seed is the split. The held-out accuracy must stay in a narrow band, and
// no snippet may ever be on both sides of a split.
func TestTFIDFIsStableAcrossSeeds(t *testing.T) {
	if _, err := os.Stat(corpusPath); err != nil {
		t.Skipf("the corpus is not available: %v", err)
	}
	table, err := ReadTable(corpusPath)
	if err != nil {
		t.Fatalf("read the corpus: %v", err)
	}
	schema, err := DetectSchema(table.Header, "", "", "", "")
	if err != nil {
		t.Fatalf("detect the schema: %v", err)
	}
	for _, seed := range []int64{1, 7, DefaultSplitSeed, 424242} {
		split, err := SplitRows(table, SplitOptions{Seed: seed})
		if err != nil {
			t.Fatalf("split: %v", err)
		}
		if split.SharedSnippets != 0 {
			t.Errorf("the seed %d leaves %d snippets on both sides of the split",
				seed, split.SharedSnippets)
		}
		docs := documentsOf(table, schema, split.Train)
		model := TrainTFIDF(docs, TFIDFOptions{})
		model.split = split
		ev := EvaluateTFIDF(table, schema, model, split)
		t.Logf("seed=%d test rows=%d accuracy=%.4f precision=%.4f recall=%.4f f1=%.4f in-sample=%.4f",
			seed, len(split.Test), ev.Accuracy, ev.Precision, ev.Recall, ev.F1, ev.TrainAccuracy)
		if ev.Accuracy < 0.48 || ev.Accuracy > 0.60 {
			t.Errorf("the seed %d gives a held-out accuracy of %.4f, which is outside the band "+
				"the other seeds give", seed, ev.Accuracy)
		}
	}
}

// TestTFIDFKeywordModeOnTheCorpus measures the degenerate control on the
// same held-out split.
func TestTFIDFKeywordModeOnTheCorpus(t *testing.T) {
	if _, err := os.Stat(corpusPath); err != nil {
		t.Skipf("the corpus is not available: %v", err)
	}
	table, err := ReadTable(corpusPath)
	if err != nil {
		t.Fatalf("read the corpus: %v", err)
	}
	schema, err := DetectSchema(table.Header, "", "", "", "")
	if err != nil {
		t.Fatalf("detect the schema: %v", err)
	}
	split, err := SplitRows(table, SplitOptions{})
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	model := TrainTFIDF(nil, TFIDFOptions{Mode: TFIDFModeKeyword})
	model.split = split
	ev := EvaluateTFIDF(table, schema, model, split)

	t.Logf("keyword mode, held-out: TP %d, FP %d, FN %d, TN %d", ev.TP, ev.FP, ev.FN, ev.TN)
	t.Logf("keyword mode, held-out accuracy: %.4f, precision %.4f, recall %.4f, F1 %.4f",
		ev.Accuracy, ev.Precision, ev.Recall, ev.F1)
}

// TestTrainTFIDFBaselineInstallsTheModel checks the wiring the "tfidf"
// baseline of the engine relies on.
func TestTrainTFIDFBaselineInstallsTheModel(t *testing.T) {
	defer SetTFIDFModel(nil)
	model, split, err := TrainTFIDFBaseline(corpusPath, TFIDFOptions{})
	if err != nil {
		t.Fatalf("train: %v", err)
	}
	if model == nil || split == nil {
		t.Fatal("the training must return the model and the split")
	}
	if CurrentTFIDFModel() != model {
		t.Error("the trained model must be installed")
	}
	eng, err := NewBaselineEngine("tfidf")
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}
	out := eng.Classify(context.Background(), "x = 1")
	if out.Classification == "" {
		t.Error("the baseline must answer")
	}
}

// TestTFIDFBaselineRunsThroughTheEngine checks that the baseline plugs into
// the ordinary run path.
func TestTFIDFBaselineRunsThroughTheEngine(t *testing.T) {
	defer SetTFIDFModel(nil)
	dir := t.TempDir()
	in := filepath.Join(dir, "in.csv")
	if err := os.WriteFile(in, []byte(sampleInput), 0o600); err != nil {
		t.Fatalf("write the input: %v", err)
	}
	if _, _, err := TrainTFIDFBaseline(in, TFIDFOptions{}); err != nil {
		t.Fatalf("train: %v", err)
	}
	rep, err := Run(context.Background(), Options{Input: in, Baseline: "tfidf"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(rep.Engines) != 1 {
		t.Fatalf("the run holds %d engines, want 1", len(rep.Engines))
	}
	if !strings.HasPrefix(rep.Engines[0].Prefix, PrefixLLM) {
		t.Errorf("the baseline is written under the %q prefix, got %q", PrefixLLM, rep.Engines[0].Prefix)
	}
	// The label of a row carries the configuration of the baseline, so that
	// the seed and the split travel with the result.
	if !strings.HasPrefix(rep.Engines[0].Rows[0].Before.Model, "baseline:tfidf;") {
		t.Errorf("model = %q", rep.Engines[0].Rows[0].Before.Model)
	}
	if !strings.Contains(rep.Engines[0].Rows[0].Before.Model, "seed=") {
		t.Errorf("the model label does not carry the seed: %q", rep.Engines[0].Rows[0].Before.Model)
	}
}
