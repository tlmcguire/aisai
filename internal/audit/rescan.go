package audit

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/tlmcguire/aisai/internal/ast"
	"github.com/tlmcguire/aisai/internal/findings"
	"github.com/tlmcguire/aisai/internal/model"
)

// RescanResult is the difference between the findings of a file before and
// after a patch was applied.
//
// It is the evidence that a proposed fix actually fixed something, which is
// the whole point of the download → mutate → rescan loop (F§6).
type RescanResult struct {
	// Resolved lists the ids of findings that were present before the
	// patch and are gone after it.
	Resolved []string `json:"resolved"`
	// Introduced lists the ids of findings that the patch introduced.
	Introduced []string `json:"introduced"`
	// Unchanged counts the findings that are present both before and after.
	Unchanged int `json:"unchanged"`
}

// RescanFile re-parses one file from disk, refreshes its indexed chunks,
// re-runs the static rules over it and reports what changed.
//
// This is what makes a patch verifiable: without it, applying a fix would
// only assert that the fix worked.
func (m *Manager) RescanFile(ctx context.Context, id, relPath string) (*RescanResult, error) {
	a, err := m.audit(id)
	if err != nil {
		return nil, err
	}
	if m.deps.SAST == nil {
		return nil, errors.New("no static analysis engine configured")
	}

	target := model.RelPath(relPath)
	if target == "" {
		return nil, errors.New("empty path")
	}

	abs, err := ResolvePath(a.Root, target)
	if err != nil {
		return nil, err
	}

	before, err := m.findingsForFile(ctx, id, target)
	if err != nil {
		return nil, err
	}

	parsed, err := ast.Parse(abs)
	if err != nil || parsed == nil {
		return nil, fmt.Errorf("re-parse %s: %w", target, err)
	}
	parsed.Path = target
	for i := range parsed.Chunks {
		parsed.Chunks[i].AuditID = id
		parsed.Chunks[i].File = target
		parsed.Chunks[i].ID = model.FindingID(target, parsed.Chunks[i].StartLine, "", parsed.Chunks[i].SymbolPath)
	}

	// Replace the cached parse so the code view and the AST view show the
	// patched file rather than the original.
	a.mu.Lock()
	replaced := false
	for i := range a.files {
		if a.files[i].Path == target {
			a.files[i] = parsed
			replaced = true
			break
		}
	}
	if !replaced {
		a.files = append(a.files, parsed)
	}
	if a.filesByPath == nil {
		a.filesByPath = map[string]*ast.File{}
	}
	a.filesByPath[target] = parsed
	pkgByDir := a.pkgByDir
	a.mu.Unlock()

	// Refresh the search index for this file, or a search would keep
	// returning the pre-patch content.
	if m.deps.Index != nil {
		if err := m.deps.Index.ReplaceFileChunks(id, target, parsed.Chunks); err != nil {
			// A broken index degrades search to keyword-only; it must not
			// fail the rescan.
			m.emit(model.Event{
				Type:    model.EventAuditProgress,
				AuditID: id,
				Phase:   "rescan",
				Message: "Index refresh failed: " + err.Error(),
			})
		}
	}

	after := m.normalizeFindings(m.deps.SAST.Scan([]*ast.File{parsed}), pkgByDir)

	// Upsert the new findings and mark the vanished ones as fixed.
	if m.deps.Store != nil {
		if _, err := m.deps.Store.Upsert(ctx, id, after); err != nil {
			return nil, err
		}
	}

	result := diffFindings(before, after)

	if m.deps.Store != nil {
		for _, findingID := range result.Resolved {
			// A finding that is gone is fixed, not dismissed. SetState
			// refuses unknown states, so this cannot fail on a value we
			// do not control.
			_ = m.deps.Store.SetState(ctx, id, findingID, model.StateFixed, nil)
		}
	}

	for i := range after {
		f := after[i]
		m.emit(model.Event{Type: model.EventFinding, AuditID: id, Finding: &f})
	}
	m.emit(model.Event{
		Type:    model.EventScanProgress,
		AuditID: id,
		Phase:   "rescan",
		Percent: 1,
		Message: fmt.Sprintf("%s: %d resolved, %d introduced, %d unchanged",
			target, len(result.Resolved), len(result.Introduced), result.Unchanged),
	})

	return result, nil
}

// findingsForFile returns the stored findings of one file.
func (m *Manager) findingsForFile(ctx context.Context, id, file string) ([]model.Finding, error) {
	if m.deps.Store == nil {
		return nil, nil
	}
	return m.deps.Store.List(ctx, id, findings.Filter{Files: []string{model.RelPath(file)}})
}

// normalizeFindings applies the same normalization a full scan does.
func (m *Manager) normalizeFindings(raw []model.Finding, pkgByDir map[string]model.PURL) []model.Finding {
	out := make([]model.Finding, 0, len(raw))
	for _, f := range raw {
		if !f.Severity.Valid() {
			f.Severity = model.SeverityInfo
		}
		if f.Confidence <= 0 || f.Confidence > 1 {
			f.Confidence = 0.5
		}
		if f.State == "" {
			f.State = model.StateOpen
		}
		f.File = model.RelPath(f.File)
		if f.ID == "" {
			f.ID = model.FindingID(f.File, f.StartLine, f.CWE, f.Rule)
		}
		if f.Package == "" {
			f.Package = packageForPath(pkgByDir, f.File)
		}
		out = append(out, f)
	}
	return out
}

// diffFindings compares two sets of findings for the same file.
//
// The comparison is by rule and CWE rather than by id, because a finding's
// id includes its line number and a patch moves lines around. Matching by
// rule is what lets "the same eval() call, three lines lower" count as
// unchanged rather than as one resolved plus one introduced.
func diffFindings(before, after []model.Finding) *RescanResult {
	beforeByKey := groupByKey(before)
	afterByKey := groupByKey(after)

	resolved := make([]string, 0)
	introduced := make([]string, 0)
	unchanged := 0

	for key, bs := range beforeByKey {
		as := afterByKey[key]
		matched := len(bs)
		if len(as) < matched {
			matched = len(as)
		}
		unchanged += matched
		// The before entries with no counterpart are gone.
		for _, f := range bs[matched:] {
			resolved = append(resolved, f.ID)
		}
	}

	for key, as := range afterByKey {
		bs := beforeByKey[key]
		matched := len(bs)
		if len(as) < matched {
			matched = len(as)
		}
		// The after entries with no counterpart are new.
		for _, f := range as[matched:] {
			introduced = append(introduced, f.ID)
		}
	}

	sort.Strings(resolved)
	sort.Strings(introduced)

	return &RescanResult{Resolved: resolved, Introduced: introduced, Unchanged: unchanged}
}

// findingKey is the identity of a finding for the purposes of a rescan
// comparison: what is wrong, not where it is.
func findingKey(f model.Finding) string {
	cwe := f.CWE
	if cwe == "" {
		cwe = f.Rule
	}
	return strings.ToLower(cwe)
}

// groupByKey groups findings by their comparison key.
func groupByKey(fs []model.Finding) map[string][]model.Finding {
	out := map[string][]model.Finding{}
	for _, f := range fs {
		k := findingKey(f)
		out[k] = append(out[k], f)
	}
	return out
}
