package sast

import (
	"sort"

	"github.com/tlmcguire/aisai/internal/ast"
	"github.com/tlmcguire/aisai/internal/model"
)

// Engine evaluates the built-in rules over parsed files.
//
// An Engine holds no mutable state: it is safe for concurrent use by multiple
// goroutines, provided the files handed to Scan are not modified while the
// scan runs.
type Engine struct {
	// rules are the rules the engine evaluates, in catalogue order.
	rules []Rule
}

// NewEngine returns an Engine that evaluates the built-in rule set.
func NewEngine() *Engine {
	return &Engine{rules: Rules()}
}

// Rules returns the rules the engine evaluates.
func (e *Engine) Rules() []Rule {
	out := make([]Rule, len(e.rules))
	copy(out, e.rules)
	return out
}

// Scan evaluates all rules over the given files and returns the findings.
//
// Python files are checked by the syntax-tree rules, files in every other
// supported language by the pattern rules. The result is deterministic:
// findings are sorted by file, then by start line, then by rule identifier.
// Files that are nil are skipped.
func (e *Engine) Scan(files []*ast.File) []model.Finding {
	var findings []model.Finding
	for _, f := range files {
		if f == nil {
			continue
		}
		// The Python rules read the Python grammar. A file in another
		// language goes through the pattern rules instead; running the
		// Python rules over it would match node kinds that happen to share
		// a name with Python's, such as Ruby's "call".
		if lang := f.LanguageDef(); lang != nil && lang.ID != ast.LanguagePython {
			findings = append(findings, scanPatterns(f)...)
			continue
		}
		sc := newScanner(f)
		sc.collect()
		var fileFindings []model.Finding
		for _, r := range e.rules {
			check, ok := checks[r.ID]
			if !ok {
				continue
			}
			fileFindings = append(fileFindings, check(sc)...)
		}
		annotateReachability(sc, fileFindings)
		findings = append(findings, fileFindings...)
	}
	sortFindings(findings)
	return findings
}

// sortFindings orders findings by file, then start line, then rule id.
func sortFindings(findings []model.Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.StartLine != b.StartLine {
			return a.StartLine < b.StartLine
		}
		return a.Rule < b.Rule
	})
}
