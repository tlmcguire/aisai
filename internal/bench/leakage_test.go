package bench

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// readTable reads a small corpus through the ordinary reader, so that the
// column index of the table is the one the reader builds.
func readTable(t *testing.T, content string) *Table {
	t.Helper()
	path := filepath.Join(t.TempDir(), "in.csv")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write the corpus: %v", err)
	}
	table, err := ReadTable(path)
	if err != nil {
		t.Fatalf("read the corpus: %v", err)
	}
	return table
}

func TestCVEIDYear(t *testing.T) {
	cases := []struct {
		cve  string
		year int
		ok   bool
	}{
		{"CVE-2012-4520", 2012, true},
		{"CVE-2024-1", 2024, true},
		{"cve-2019-0001", 2019, true},
		{"GHSA-1234-5678-9abc", 0, false},
		{"", 0, false},
		{"CVE-abc-1", 0, false},
	}
	for _, tc := range cases {
		got, ok := cveIDYear(tc.cve)
		if ok != tc.ok {
			t.Errorf("cveIDYear(%q) ok = %v, want %v", tc.cve, ok, tc.ok)
			continue
		}
		if !ok {
			continue
		}
		if got.Year() != tc.year {
			t.Errorf("cveIDYear(%q) = %d, want %d", tc.cve, got.Year(), tc.year)
		}
	}
}

func TestSplitByCutoffProxyOnTheCorpus(t *testing.T) {
	table, err := ReadTable(corpusPath)
	if err != nil {
		t.Fatalf("read the corpus: %v", err)
	}
	cutoff := time.Date(2023, time.January, 1, 0, 0, 0, 0, time.UTC)
	split, err := SplitByCutoffProxy(table, CutoffOptions{Cutoff: cutoff})
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if !split.IsProxy {
		t.Error("the split must be marked as a proxy: the corpus carries no date column")
	}
	if split.ProxyKind != CutoffProxyCVEIDYear {
		t.Errorf("the proxy kind = %q", split.ProxyKind)
	}
	if len(split.Pre)+len(split.Post)+len(split.Undated) != len(table.Rows) {
		t.Errorf("the split covers %d rows, want %d",
			len(split.Pre)+len(split.Post)+len(split.Undated), len(table.Rows))
	}
	if len(split.Post) == 0 {
		t.Error("the post-cutoff group is empty")
	}
	if len(split.Undated) != 0 {
		t.Errorf("%d rows carry no CVE year", len(split.Undated))
	}
	// Every row of the post-cutoff group must be after the cutoff.
	for _, row := range split.Post {
		if !split.Dates[row].After(cutoff) {
			t.Fatalf("the row %d is in the post-cutoff group but its date is %s",
				row, split.Dates[row])
		}
	}
	for _, row := range split.Pre {
		if split.Dates[row].After(cutoff) {
			t.Fatalf("the row %d is in the pre-cutoff group but its date is %s",
				row, split.Dates[row])
		}
	}
}

func TestSplitByCutoffProxyIsConservativeForTheCutoffYear(t *testing.T) {
	// A row whose year is the year of the cutoff may be before the cutoff,
	// so it is put in the pre-cutoff group.
	table := readTable(t, "cve_id,vulnerable_code,fixed_code\n"+
		"CVE-2023-1,a,b\n"+
		"CVE-2024-1,a,b\n")
	cutoff := time.Date(2023, time.June, 1, 0, 0, 0, 0, time.UTC)
	split, err := SplitByCutoffProxy(table, CutoffOptions{Cutoff: cutoff})
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if len(split.Pre) != 1 || len(split.Post) != 1 {
		t.Fatalf("the split is %d pre and %d post, want 1 and 1",
			len(split.Pre), len(split.Post))
	}
	if split.Pre[0] != 0 {
		t.Errorf("the row of the cutoff year must be in the pre-cutoff group, got %v", split.Pre)
	}
}

func TestSplitByCutoffNeedsACutoff(t *testing.T) {
	table := readTable(t, "cve_id\nCVE-2023-1\n")
	if _, err := SplitByCutoff(table, CutoffOptions{}); err == nil {
		t.Fatal("expected an error when no cutoff is given")
	}
}

func TestSplitByCutoffUsesADateColumnWhenThereIsOne(t *testing.T) {
	table := readTable(t, "cve_id,published,vulnerable_code,fixed_code\n"+
		"CVE-2023-1,2023-01-15T00:00:00Z,a,b\n"+
		"CVE-2023-2,2024-06-01T00:00:00Z,a,b\n")
	cutoff := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	split, err := SplitByCutoff(table, CutoffOptions{
		Cutoff:     cutoff,
		DateColumn: "published",
	})
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if split.IsProxy {
		t.Error("a real date column must not be reported as a proxy")
	}
	if split.ProxyKind != CutoffProxyDateColumn {
		t.Errorf("the split kind = %q", split.ProxyKind)
	}
	if len(split.Pre) != 1 || len(split.Post) != 1 {
		t.Fatalf("the split is %d pre and %d post, want 1 and 1",
			len(split.Pre), len(split.Post))
	}
}

func TestSplitByCutoffFallsBackToTheProxy(t *testing.T) {
	table := readTable(t, "cve_id,vulnerable_code,fixed_code\nCVE-2024-1,a,b\n")
	cutoff := time.Date(2023, time.January, 1, 0, 0, 0, 0, time.UTC)
	// The column does not exist: the split must fall back to the proxy and
	// say so.
	split, err := SplitByCutoff(table, CutoffOptions{Cutoff: cutoff, DateColumn: "published"})
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if !split.IsProxy {
		t.Error("the split must be marked as a proxy")
	}
	if len(split.Post) != 1 {
		t.Errorf("the post-cutoff group holds %d rows, want 1", len(split.Post))
	}
}

func TestCutoffSplitContaminatedRows(t *testing.T) {
	table := readTable(t, "cve_id\nCVE-2022-1\nCVE-2024-1\nCVE-2025-1\n")
	cutoff := time.Date(2023, time.January, 1, 0, 0, 0, 0, time.UTC)
	// The model was trained until the middle of 2024: the row of 2024 is
	// post-cutoff for the corpus but still inside the training data of the
	// model.
	modelCutoff := time.Date(2024, time.July, 1, 0, 0, 0, 0, time.UTC)
	split, err := SplitByCutoffProxy(table, CutoffOptions{Cutoff: cutoff, ModelCutoff: modelCutoff})
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if len(split.Post) != 2 {
		t.Fatalf("the post-cutoff group holds %d rows, want 2", len(split.Post))
	}
	if got := split.ContaminatedRows(); got != 1 {
		t.Errorf("contaminated rows = %d, want 1", got)
	}
}

func TestCutoffSplitMetrics(t *testing.T) {
	table := readTable(t, "cve_id\nCVE-2022-1\nCVE-2024-1\n")
	cutoff := time.Date(2023, time.January, 1, 0, 0, 0, 0, time.UTC)
	split, err := SplitByCutoffProxy(table, CutoffOptions{Cutoff: cutoff})
	if err != nil {
		t.Fatalf("split: %v", err)
	}

	rep := &Report{
		Schema: InputSchema{Mode: ModePaired},
		Rows:   [][]string{{"CVE-2022-1"}, {"CVE-2024-1"}},
		Engines: []*EngineReport{{
			Prefix: PrefixStatic,
			Rows: []EngineRow{
				{
					Before: Outcome{Classification: ClassificationVulnerable, Correct: true, Completed: true},
					After:  Outcome{Classification: ClassificationSafe, Correct: true, Completed: true},
				},
				{
					Before: Outcome{Classification: ClassificationSafe, Correct: false, Completed: true},
					After:  Outcome{Classification: ClassificationVulnerable, Correct: false, Completed: true},
				},
			},
		}},
	}
	groups, err := split.Metrics(rep, 0)
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}
	if len(groups) != 3 {
		t.Fatalf("the metrics hold %d groups, want 3", len(groups))
	}
	if groups[0].Group != GroupPreCutoff || groups[1].Group != GroupPostCutoff {
		t.Errorf("the groups are %q and %q", groups[0].Group, groups[1].Group)
	}
	if groups[0].Accuracy != 1 {
		t.Errorf("the pre-cutoff accuracy is %f, want 1", groups[0].Accuracy)
	}
	if groups[1].Accuracy != 0 {
		t.Errorf("the post-cutoff accuracy is %f, want 0", groups[1].Accuracy)
	}
	if groups[1].Rows != 1 || groups[1].Snippets != 2 {
		t.Errorf("the post-cutoff group holds %d rows and %d snippets, want 1 and 2",
			groups[1].Rows, groups[1].Snippets)
	}
}

func TestCutoffSplitMetricsRejectsAnUnknownEngine(t *testing.T) {
	split := &CutoffSplit{}
	if _, err := split.Metrics(&Report{}, 3); err == nil {
		t.Fatal("expected an error for an engine that does not exist")
	}
}

func TestCutoffSplitMetadataNamesTheProxy(t *testing.T) {
	table := readTable(t, "cve_id\nCVE-2024-1\n")
	cutoff := time.Date(2023, time.January, 1, 0, 0, 0, 0, time.UTC)
	split, err := SplitByCutoffProxy(table, CutoffOptions{
		Cutoff:      cutoff,
		ModelCutoff: time.Date(2023, time.June, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	m := split.Metadata("abc123", "some-model")
	if !m.IsProxy {
		t.Error("the metadata must report that the ordering is a proxy")
	}
	if m.CorpusSHA256 != "abc123" || m.Model != "some-model" {
		t.Errorf("the metadata is %+v", m)
	}
	if !m.Cutoff.Equal(cutoff) {
		t.Errorf("the cutoff of the metadata is %s, want %s", m.Cutoff, cutoff)
	}
	if !m.ModelCutoff.Equal(time.Date(2023, time.June, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("the model cutoff of the metadata is %s", m.ModelCutoff)
	}
	if !strings.Contains(m.ProxyNote, "proxy") {
		t.Errorf("the note must name the limitation: %q", m.ProxyNote)
	}
	if m.PostCutoffRows != 1 {
		t.Errorf("the post-cutoff rows of the metadata = %d, want 1", m.PostCutoffRows)
	}
}
