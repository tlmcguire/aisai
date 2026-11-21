package bench

import "testing"

// TestTFIDFHyperparameterScan sweeps the smoothing of the classifier and
// both of its modes over the real corpus and logs the held-out performance
// of each combination.
//
// It is a diagnostic, not an assertion: the numbers move with the corpus and
// the split, so pinning them here would make the test fail every time the
// corpus changes. Read the log to choose the settings, and pin the chosen
// ones in the test that asserts the behaviour.
//
// The default settings are the ones the classifier uses when no options are
// given; the scan exists to show that they are not an accident.
func TestTFIDFHyperparameterScan(t *testing.T) {
	table, err := ReadTable(corpusPath)
	if err != nil {
		t.Skipf("the corpus is not available: %v", err)
	}
	schema, err := DetectSchema(table.Header, "", "", "", "")
	if err != nil {
		t.Fatalf("detect the schema: %v", err)
	}
	split, err := SplitRows(table, SplitOptions{})
	if err != nil {
		t.Fatalf("split the corpus: %v", err)
	}
	docs := documentsOf(table, schema, split.Train)

	for _, mode := range []string{TFIDFModeClassifier, TFIDFModeKeyword} {
		for _, smoothing := range []float64{0.01, 0.1, 0.5, 1.0} {
			model := TrainTFIDF(docs, TFIDFOptions{Mode: mode, Smoothing: smoothing})
			model.split = split
			evaluation := EvaluateTFIDF(table, schema, model, split)
			t.Logf("mode=%-10s smoothing=%.2f heldout acc=%.4f prec=%.4f rec=%.4f | train acc=%.4f",
				mode, smoothing,
				evaluation.Accuracy, evaluation.Precision, evaluation.Recall,
				evaluation.TrainAccuracy)
		}
	}
}
