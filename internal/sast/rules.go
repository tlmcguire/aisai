// Package sast implements the built-in static analysis rules of AISAI.
//
// The engine is a deterministic, dependency-free rule engine over the
// Tree-sitter syntax tree produced by package ast. It does not shell out to
// Bandit or Semgrep and does not use an LLM: the same input always produces
// the same findings, which is what makes the benchmark in bench/ meaningful.
//
// Every rule reports a canonical model.Finding carrying a CWE identifier, a
// normalized severity and a confidence score, so findings from this engine can
// be deduplicated against findings from other tools.
package sast

import "github.com/tlmcguire/aisai/internal/model"

// Rule identifiers of the built-in rules.
const (
	// RuleSubprocessShellTrue detects subprocess calls with shell=True.
	RuleSubprocessShellTrue = "subprocess-shell-true"
	// RuleEvalExecNonliteral detects eval/exec/compile on a non-literal.
	RuleEvalExecNonliteral = "eval-exec-nonliteral"
	// RulePickleYamlUnsafeLoad detects unsafe pickle and yaml deserialization.
	RulePickleYamlUnsafeLoad = "pickle-yaml-unsafe-load"
	// RuleTempfileMktemp detects tempfile.mktemp.
	RuleTempfileMktemp = "tempfile-mktemp"
	// RuleWeakHashMD5SHA1 detects MD5/SHA1 used for security purposes.
	RuleWeakHashMD5SHA1 = "weak-hash-md5-sha1"
	// RuleHardcodedSecret detects high-entropy secrets assigned to secret-ish names.
	RuleHardcodedSecret = "hardcoded-secret"
	// RuleDebugTrueEnabled detects debug mode being enabled.
	RuleDebugTrueEnabled = "debug-true-enabled"
	// RuleAssertUsedForValidation detects assert used for runtime validation.
	RuleAssertUsedForValidation = "assert-used-for-validation"
	// RuleTLSVerifyDisabled detects disabled TLS certificate verification.
	RuleTLSVerifyDisabled = "tls-verify-disabled"
	// RulePathTraversal detects file paths derived from request parameters.
	RulePathTraversal = "path-traversal"
)

// Rule describes one built-in static analysis rule.
type Rule struct {
	// ID is the stable rule identifier, for example "subprocess-shell-true".
	// It is used verbatim as the Finding.Rule of the findings it produces.
	ID string
	// Name is a short human-readable name of the rule.
	Name string
	// CWE is the CWE identifier the rule maps to, for example "CWE-78".
	CWE string
	// Description explains what the rule detects.
	Description string
	// Severity is the normalized severity of the findings the rule produces.
	Severity model.Severity
	// Confidence is the normalized confidence of the findings the rule
	// produces, in the range [0,1].
	Confidence float64
}

// ruleTable is the built-in rule catalogue, in a stable order.
var ruleTable = []Rule{
	{
		ID:          RuleSubprocessShellTrue,
		Name:        "Subprocess with shell=True",
		CWE:         "CWE-78",
		Description: "A subprocess call with shell=True lets an attacker inject additional shell commands.",
		Severity:    model.SeverityHigh,
		Confidence:  0.95,
	},
	{
		ID:          RuleEvalExecNonliteral,
		Name:        "eval/exec/compile on a non-literal",
		CWE:         "CWE-95",
		Description: "eval, exec or compile on a value that is not a literal can execute attacker-controlled code.",
		Severity:    model.SeverityHigh,
		Confidence:  0.90,
	},
	{
		ID:          RulePickleYamlUnsafeLoad,
		Name:        "Unsafe deserialization",
		CWE:         "CWE-502",
		Description: "pickle.load/loads and yaml.load without SafeLoader deserialize untrusted data and can execute code.",
		Severity:    model.SeverityHigh,
		Confidence:  0.90,
	},
	{
		ID:          RuleTempfileMktemp,
		Name:        "tempfile.mktemp",
		CWE:         "CWE-377",
		Description: "tempfile.mktemp is vulnerable to a race condition and should be replaced by tempfile.mkstemp.",
		Severity:    model.SeverityMedium,
		Confidence:  0.85,
	},
	{
		ID:          RuleWeakHashMD5SHA1,
		Name:        "Weak hash algorithm",
		CWE:         "CWE-327",
		Description: "MD5 and SHA1 are cryptographically broken and must not be used for security purposes.",
		Severity:    model.SeverityMedium,
		Confidence:  0.90,
	},
	{
		ID:          RuleHardcodedSecret,
		Name:        "Hardcoded secret",
		CWE:         "CWE-798",
		Description: "A high-entropy string literal assigned to a secret-like name is a hardcoded credential.",
		Severity:    model.SeverityHigh,
		Confidence:  0.75,
	},
	{
		ID:          RuleDebugTrueEnabled,
		Name:        "Debug mode enabled",
		CWE:         "CWE-489",
		Description: "Debug mode must not be enabled in production code; it exposes an interactive debugger.",
		Severity:    model.SeverityMedium,
		Confidence:  0.90,
	},
	{
		ID:          RuleAssertUsedForValidation,
		Name:        "Assert used for validation",
		CWE:         "CWE-617",
		Description: "assert statements are removed when Python runs with -O and must not be used for input validation.",
		Severity:    model.SeverityLow,
		Confidence:  0.70,
	},
	{
		ID:          RuleTLSVerifyDisabled,
		Name:        "TLS verification disabled",
		CWE:         "CWE-295",
		Description: "Passing verify=False disables TLS certificate verification and allows machine-in-the-middle attacks.",
		Severity:    model.SeverityHigh,
		Confidence:  0.90,
	},
	{
		ID:          RulePathTraversal,
		Name:        "Path traversal",
		CWE:         "CWE-22",
		Description: "A file path derived from a request parameter lets an attacker read or write arbitrary files.",
		Severity:    model.SeverityHigh,
		Confidence:  0.85,
	},
}

// Rules returns the built-in rule catalogue. The returned slice is a copy, so
// callers may modify it freely.
func Rules() []Rule {
	out := make([]Rule, len(ruleTable))
	copy(out, ruleTable)
	return out
}

// ruleByID looks up a rule in the built-in catalogue.
func ruleByID(id string) (Rule, bool) {
	for _, r := range ruleTable {
		if r.ID == id {
			return r, true
		}
	}
	return Rule{}, false
}
