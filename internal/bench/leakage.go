package bench

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The proxy orderings of the post-cutoff split.
const (
	// CutoffProxyCVEIDYear derives the date of a row from the year of its
	// CVE identifier.
	//
	// The corpus of this project carries no date column at all: its
	// columns are the CVE identifier, the vulnerability family, the
	// severities, the complexity metrics, the commit hash, the file name,
	// the change type, the language and the two snippets. The only
	// date-like signal is the year in the CVE identifier, which is the
	// year the identifier was assigned.
	//
	// This is a proxy and not a date. The identifier says in which year
	// the CVE was numbered, not on which day it was published, and the
	// granularity is a year. Every name of this file says so.
	CutoffProxyCVEIDYear = "cve-id-year"
	// CutoffProxyDateColumn uses a column that holds a real date.
	CutoffProxyDateColumn = "date-column"
)

// The names of the groups of a post-cutoff split.
const (
	// GroupPreCutoff is the group of the rows that are at or before the
	// cutoff.
	GroupPreCutoff = "pre-cutoff"
	// GroupPostCutoff is the group of the rows that are after the cutoff.
	GroupPostCutoff = "post-cutoff"
	// GroupUndated is the group of the rows no date could be derived
	// from.
	GroupUndated = "undated"
)

// CutoffOptions configures the post-cutoff split.
type CutoffOptions struct {
	// Cutoff is the date the corpus is split on.
	//
	// It must be supplied by the operator. It is not inferred from
	// anything: the corpus does not say when it was collected, and the
	// training cutoff of a model is a fact about the model, not about
	// the data.
	Cutoff time.Time
	// ModelCutoff is the training cutoff of the evaluated model.
	//
	// It must be supplied by the operator as well, and it is recorded in
	// the run metadata. It cannot be inferred: no API of a model provider
	// states it.
	ModelCutoff time.Time
	// DateColumn is the column of the corpus that holds a real date.
	//
	// When it is empty, or when the corpus does not have it, the split
	// falls back to the proxy ordering, and the result says so.
	DateColumn string
	// DateLayout is the layout of the dates of the date column. The
	// default is RFC 3339.
	DateLayout string
}

// CutoffSplit is the partition of the corpus rows into a pre-cutoff and a
// post-cutoff group.
//
// The split is deterministic: the same corpus and the same cutoff always
// give the same partition.
type CutoffSplit struct {
	// Cutoff is the date the corpus was split on.
	Cutoff time.Time
	// ModelCutoff is the training cutoff of the evaluated model, as
	// supplied by the operator.
	ModelCutoff time.Time
	// IsProxy reports that the ordering of the rows is a proxy and not a
	// real date.
	IsProxy bool
	// ProxyKind names the ordering: CutoffProxyCVEIDYear or
	// CutoffProxyDateColumn.
	ProxyKind string
	// Pre holds the row indices that are at or before the cutoff.
	Pre []int
	// Post holds the row indices that are after the cutoff.
	Post []int
	// Undated holds the row indices no date could be derived from.
	Undated []int
	// Rows is the number of rows the split covers.
	Rows int
	// Dates holds the date that was derived for every row, by row index.
	// A row with no date has the zero time.
	Dates []time.Time
}

// SplitByCutoff partitions the rows of the corpus into a pre-cutoff and a
// post-cutoff group.
//
// When the corpus holds a real date column, and the caller names it, the
// split is a true post-cutoff split: a row is post-cutoff when its date is
// after the cutoff.
//
// The corpus of this project has no date column, so the default path is the
// proxy ordering: the date of a row is derived from the year of its CVE
// identifier. The result is marked as a proxy, its kind is named, and it is
// never presented as a true post-cutoff split. The proxy is conservative: a
// row whose year is the year of the cutoff is put in the pre-cutoff group,
// because its real date may be before the cutoff.
func SplitByCutoff(table *Table, opts CutoffOptions) (*CutoffSplit, error) {
	if table == nil {
		return nil, fmt.Errorf("bench: no corpus to split")
	}
	if opts.Cutoff.IsZero() {
		return nil, fmt.Errorf("bench: the cutoff date is required")
	}
	if opts.DateColumn != "" && tableHasColumn(table, opts.DateColumn) {
		return splitByDateColumn(table, opts)
	}
	return SplitByCutoffProxy(table, opts)
}

// SplitByCutoffProxy partitions the rows of the corpus with the proxy
// ordering.
//
// The date of a row is the first moment of the year of its CVE identifier.
// The function is named after what it does: it is a proxy split, and the
// result says so through CutoffSplit.IsProxy and CutoffSplit.ProxyKind.
//
// A row whose CVE identifier carries no year is undated, and is reported as
// such instead of being put in one of the two groups.
func SplitByCutoffProxy(table *Table, opts CutoffOptions) (*CutoffSplit, error) {
	if table == nil {
		return nil, fmt.Errorf("bench: no corpus to split")
	}
	if opts.Cutoff.IsZero() {
		return nil, fmt.Errorf("bench: the cutoff date is required")
	}
	split := &CutoffSplit{
		Cutoff:      opts.Cutoff,
		ModelCutoff: opts.ModelCutoff,
		IsProxy:     true,
		ProxyKind:   CutoffProxyCVEIDYear,
		Rows:        len(table.Rows),
		Dates:       make([]time.Time, len(table.Rows)),
	}
	for i := range table.Rows {
		date, ok := cveIDYear(table.Cell(i, "cve_id"))
		if !ok {
			split.Undated = append(split.Undated, i)
			continue
		}
		split.Dates[i] = date
		if date.After(opts.Cutoff) {
			split.Post = append(split.Post, i)
			continue
		}
		split.Pre = append(split.Pre, i)
	}
	return split, nil
}

// splitByDateColumn partitions the rows with a real date column.
func splitByDateColumn(table *Table, opts CutoffOptions) (*CutoffSplit, error) {
	layout := opts.DateLayout
	if layout == "" {
		layout = time.RFC3339
	}
	split := &CutoffSplit{
		Cutoff:      opts.Cutoff,
		ModelCutoff: opts.ModelCutoff,
		IsProxy:     false,
		ProxyKind:   CutoffProxyDateColumn,
		Rows:        len(table.Rows),
		Dates:       make([]time.Time, len(table.Rows)),
	}
	for i := range table.Rows {
		raw := strings.TrimSpace(table.Cell(i, opts.DateColumn))
		if raw == "" {
			split.Undated = append(split.Undated, i)
			continue
		}
		date, err := time.Parse(layout, raw)
		if err != nil {
			return nil, fmt.Errorf(
				"bench: the date %q of the row %d does not match the layout %q: %w",
				raw, i, layout, err)
		}
		split.Dates[i] = date
		if date.After(opts.Cutoff) {
			split.Post = append(split.Post, i)
			continue
		}
		split.Pre = append(split.Pre, i)
	}
	return split, nil
}

// cveIDYear derives the first moment of the year of a CVE identifier.
//
// The identifier of a CVE carries the year it was numbered in, which is a
// proxy for the date of the vulnerability and not the date itself.
func cveIDYear(cve string) (time.Time, bool) {
	parts := strings.Split(strings.TrimSpace(cve), "-")
	if len(parts) < 3 {
		return time.Time{}, false
	}
	if !strings.EqualFold(parts[0], "CVE") {
		return time.Time{}, false
	}
	year, err := strconv.Atoi(parts[1])
	if err != nil {
		return time.Time{}, false
	}
	if year < 1999 || year > 2100 {
		return time.Time{}, false
	}
	return time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC), true
}

// tableHasColumn reports whether a table has a column.
func tableHasColumn(table *Table, column string) bool {
	for _, c := range table.Header {
		if c == column {
			return true
		}
	}
	return false
}

// Group returns the row indices of a group.
func (s *CutoffSplit) Group(name string) []int {
	switch name {
	case GroupPreCutoff:
		return s.Pre
	case GroupPostCutoff:
		return s.Post
	case GroupUndated:
		return s.Undated
	}
	return nil
}

// ContaminatedRows returns the number of rows of the post-cutoff group that
// are still inside the training data of the evaluated model.
//
// A row of the post-cutoff group is only out of the training data of the
// model when the derived date of the row is after the training cutoff of the
// model. When the model was trained later than the cutoff of the corpus,
// part of the post-cutoff group is contaminated, and the number says how
// much of it.
//
// The model's training cutoff is supplied by the operator; it cannot be
// inferred.
func (s *CutoffSplit) ContaminatedRows() int {
	if s.ModelCutoff.IsZero() {
		return 0
	}
	n := 0
	for _, row := range s.Post {
		if row < len(s.Dates) && !s.Dates[row].After(s.ModelCutoff) {
			n++
		}
	}
	return n
}

// CutoffGroupMetrics holds the metrics of one group of a split.
type CutoffGroupMetrics struct {
	// Group is the name of the group.
	Group string
	// Rows is the number of rows of the group.
	Rows int
	// Snippets is the number of snippets of the group.
	Snippets int
	// Completed is the number of snippets the engine answered.
	Completed int
	// Correct is the number of the answered snippets that were classified
	// correctly.
	Correct int
	// Accuracy is the share of the answered snippets that were correct.
	Accuracy float64
}

// Metrics computes the per-group metrics of a run.
//
// The engine index selects the engine of the report: the first engine is the
// LLM one, the second the static one, in the order the report holds them.
//
// The metrics of a group are computed over the rows of that group only, so a
// run can be reported on the post-cutoff subset alone.
func (s *CutoffSplit) Metrics(rep *Report, engine int) ([]CutoffGroupMetrics, error) {
	if rep == nil {
		return nil, fmt.Errorf("bench: no report to measure")
	}
	if engine < 0 || engine >= len(rep.Engines) {
		return nil, fmt.Errorf(
			"bench: the report holds %d engines, the engine %d does not exist", len(rep.Engines), engine)
	}
	er := rep.Engines[engine]

	out := make([]CutoffGroupMetrics, 0, 3)
	for _, name := range []string{GroupPreCutoff, GroupPostCutoff, GroupUndated} {
		rows := s.Group(name)
		g := CutoffGroupMetrics{Group: name, Rows: len(rows)}
		for _, row := range rows {
			if row < 0 || row >= len(er.Rows) {
				continue
			}
			r := er.Rows[row]
			outcomes := []Outcome{r.Before, r.After}
			if rep.Schema.Mode == ModeSingle {
				outcomes = []Outcome{r.Single}
			}
			for _, o := range outcomes {
				g.Snippets++
				if !o.Completed {
					continue
				}
				g.Completed++
				if o.Correct {
					g.Correct++
				}
			}
		}
		if g.Completed > 0 {
			g.Accuracy = float64(g.Correct) / float64(g.Completed)
		}
		out = append(out, g)
	}
	return out, nil
}

// LeakageMetadata records the provenance of a run in the context of the
// training cutoff of the evaluated model.
//
// It is written into the report of a run so that the numbers can be read
// with the cutoff they were measured against.
type LeakageMetadata struct {
	// CorpusSHA256 is the digest of the corpus the run was made on.
	CorpusSHA256 string
	// Model identifies the evaluated model.
	Model string
	// Cutoff is the date the corpus was split on.
	Cutoff time.Time
	// ModelCutoff is the training cutoff of the evaluated model, as
	// supplied by the operator.
	ModelCutoff time.Time
	// SplitKind names the ordering the split used.
	SplitKind string
	// IsProxy reports that the ordering is a proxy and not a real date.
	IsProxy bool
	// ProxyNote states the limitation of the ordering in one sentence.
	ProxyNote string
	// PreCutoffRows is the number of rows of the pre-cutoff group.
	PreCutoffRows int
	// PostCutoffRows is the number of rows of the post-cutoff group.
	PostCutoffRows int
	// UndatedRows is the number of rows no date could be derived from.
	UndatedRows int
	// ContaminatedRows is the number of rows of the post-cutoff group
	// that are still inside the training data of the evaluated model.
	ContaminatedRows int
}

// Metadata returns the metadata of the split.
func (s *CutoffSplit) Metadata(corpusSHA256, model string) LeakageMetadata {
	m := LeakageMetadata{
		CorpusSHA256:     corpusSHA256,
		Model:            model,
		Cutoff:           s.Cutoff,
		ModelCutoff:      s.ModelCutoff,
		SplitKind:        s.ProxyKind,
		IsProxy:          s.IsProxy,
		PreCutoffRows:    len(s.Pre),
		PostCutoffRows:   len(s.Post),
		UndatedRows:      len(s.Undated),
		ContaminatedRows: s.ContaminatedRows(),
	}
	if s.IsProxy {
		m.ProxyNote = fmt.Sprintf(
			"the corpus carries no date column: the ordering is the year of the CVE identifier (%s), "+
				"which is a proxy for the date and not the date itself", s.ProxyKind)
	} else {
		m.ProxyNote = "the ordering is a real date column of the corpus"
	}
	return m
}
