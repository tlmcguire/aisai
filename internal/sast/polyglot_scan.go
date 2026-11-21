package sast

import (
	"sort"

	"github.com/tlmcguire/aisai/internal/ast"
	"github.com/tlmcguire/aisai/internal/model"
)

// maxPatternFindingsPerFile caps what the pattern rules report for one file,
// so that a generated or minified file cannot flood the findings list.
const maxPatternFindingsPerFile = 200

// languageRuleTable describes the rules that only the pattern rules produce.
// The pattern rules also report the Python rules that have a counterpart in
// other languages (a weak hash, disabled TLS verification, a shell command),
// and those keep the identifier and the metadata they have in Rules.
var languageRuleTable = []Rule{
	{
		ID:          RuleXSSUnsafeOutput,
		Name:        "Unescaped output",
		CWE:         "CWE-79",
		Description: "Markup written to a page without escaping lets an attacker inject script.",
		Severity:    model.SeverityMedium,
		Confidence:  0.70,
	},
	{
		ID:          RuleSQLInjectionConcat,
		Name:        "SQL built by concatenation",
		CWE:         "CWE-89",
		Description: "A SQL statement that has values concatenated or formatted into its text can be changed by an attacker.",
		Severity:    model.SeverityHigh,
		Confidence:  0.75,
	},
	{
		ID:          RuleUnsafeCFunction,
		Name:        "Unbounded C library function",
		CWE:         "CWE-120",
		Description: "gets, strcpy and similar functions cannot limit how much they write and overflow the destination buffer.",
		Severity:    model.SeverityHigh,
		Confidence:  0.80,
	},
	{
		ID:          RuleFormatString,
		Name:        "Non-literal format string",
		CWE:         "CWE-134",
		Description: "A format string that comes from a variable lets an attacker read or write memory.",
		Severity:    model.SeverityHigh,
		Confidence:  0.70,
	},
	{
		ID:          RuleWeakCipher,
		Name:        "Weak cipher",
		CWE:         "CWE-327",
		Description: "DES, RC4, RC2 and the ECB mode do not protect data and must not be used.",
		Severity:    model.SeverityMedium,
		Confidence:  0.85,
	},
}

// LanguageRules returns the rules that only apply to languages other than
// Python. The returned slice is a copy.
func LanguageRules() []Rule {
	out := make([]Rule, len(languageRuleTable))
	copy(out, languageRuleTable)
	return out
}

// patternRuleByID looks a rule up in the Python catalogue and then in the
// language catalogue.
func patternRuleByID(id string) (Rule, bool) {
	if r, ok := ruleByID(id); ok {
		return r, true
	}
	for _, r := range languageRuleTable {
		if r.ID == id {
			return r, true
		}
	}
	return Rule{}, false
}

// scanPatterns runs the pattern rules over one parsed file of any language
// other than Python.
func scanPatterns(f *ast.File) []model.Finding {
	lang := f.LanguageDef()
	if lang == nil {
		return nil
	}
	pf := pgFile{lang: lang.ID, content: f.Content}

	var hits []pgHit
	var walk func(n ast.Node)
	walk = func(n ast.Node) {
		if lang.IsCall(n.Kind) && len(n.Text) <= maxPatternCallBytes {
			callee, args := pgSplitCall(n.Text)
			hits = append(hits, pgMatchCall(pgCall{
				callee:  callee,
				args:    args,
				text:    n.Text,
				line:    n.StartLine,
				endLine: n.EndLine,
			}, pf)...)
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(f.Root())
	hits = append(hits, pgScanLines(pf)...)

	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].line != hits[j].line {
			return hits[i].line < hits[j].line
		}
		return hits[i].rule < hits[j].rule
	})
	hits = pgDedupe(hits)
	if len(hits) > maxPatternFindingsPerFile {
		hits = hits[:maxPatternFindingsPerFile]
	}

	sc := &scanner{file: f}
	out := make([]model.Finding, 0, len(hits))
	for _, h := range hits {
		r, ok := patternRuleByID(h.rule)
		if !ok {
			continue
		}
		node := ast.Node{StartLine: h.line, EndLine: h.endLine, Text: h.code}
		finding := newFinding(r, sc, node, h.message, model.Evidence{Sink: h.sink, Note: h.hint})
		if h.hint != "" {
			finding.Remediation = h.hint
		}
		out = append(out, finding)
	}
	return out
}
