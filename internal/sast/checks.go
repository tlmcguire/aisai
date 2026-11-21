package sast

import (
	"math"
	"strings"

	"github.com/tlmcguire/aisai/internal/ast"
	"github.com/tlmcguire/aisai/internal/model"
)

// ToolName is the value reported in the Tool field of every finding produced
// by this engine.
const ToolName = "aisai"

// maxSnippetLen caps the length of the source snippet recorded on a finding.
const maxSnippetLen = 400

// checkFunc is the signature of a rule implementation. It receives the
// scanner of one file and returns the findings the rule produces for it.
type checkFunc func(sc *scanner) []model.Finding

// checks maps a rule identifier to its implementation.
var checks = map[string]checkFunc{
	RuleSubprocessShellTrue:     checkSubprocessShellTrue,
	RuleEvalExecNonliteral:      checkEvalExecNonliteral,
	RulePickleYamlUnsafeLoad:    checkPickleYamlUnsafeLoad,
	RuleTempfileMktemp:          checkTempfileMktemp,
	RuleWeakHashMD5SHA1:         checkWeakHashMD5SHA1,
	RuleHardcodedSecret:         checkHardcodedSecret,
	RuleDebugTrueEnabled:        checkDebugTrueEnabled,
	RuleAssertUsedForValidation: checkAssertUsedForValidation,
	RuleTLSVerifyDisabled:       checkTLSVerifyDisabled,
	RulePathTraversal:           checkPathTraversal,
}

// callInfo describes a call expression.
type callInfo struct {
	// node is the call node.
	node ast.Node
	// rawCallee is the callee as written in the source.
	rawCallee string
	// callee is the resolved qualified callee name.
	callee string
	// args are the positional arguments, in source order.
	args []ast.Node
	// kwargs are the keyword arguments, keyed by argument name.
	kwargs map[string]ast.Node
}

// callInfoOf extracts the structure of a call expression.
func callInfoOf(n ast.Node, s *scope) callInfo {
	info := callInfo{node: n, kwargs: map[string]ast.Node{}}
	fn, ok := n.Child("function")
	if !ok {
		return info
	}
	info.rawCallee = fn.Text
	info.callee = s.resolve(fn.Text)
	argList, ok := n.Child("arguments")
	if !ok {
		return info
	}
	for _, a := range argList.Children {
		if a.Kind == "keyword_argument" {
			name, okName := a.Child("name")
			value, okValue := a.Child("value")
			if okName && okValue {
				info.kwargs[name.Text] = value
			}
			continue
		}
		info.args = append(info.args, a)
	}
	return info
}

// checkSubprocessShellTrue reports subprocess calls that run through a shell.
func checkSubprocessShellTrue(sc *scanner) []model.Finding {
	r, _ := ruleByID(RuleSubprocessShellTrue)
	var out []model.Finding
	for _, e := range sc.events {
		if e.kind != evtCall {
			continue
		}
		info := callInfoOf(e.node, e.scope)
		if !isSubprocessCall(info.callee) {
			continue
		}
		shell, ok := info.kwargs["shell"]
		if !ok || !isTrueLiteral(shell) {
			continue
		}
		out = append(out, newFinding(r, sc, e.node,
			"subprocess call with shell=True allows shell command injection",
			model.Evidence{
				Source: "shell=True",
				Sink:   info.callee + "()",
				Note:   "the command is interpreted by the system shell",
			}))
	}
	return out
}

// isSubprocessCall reports whether a resolved callee is a subprocess call.
func isSubprocessCall(callee string) bool {
	return callee != "" && strings.HasPrefix(callee, "subprocess.")
}

// checkEvalExecNonliteral reports eval, exec and compile on a non-literal.
func checkEvalExecNonliteral(sc *scanner) []model.Finding {
	r, _ := ruleByID(RuleEvalExecNonliteral)
	var out []model.Finding
	for _, e := range sc.events {
		if e.kind != evtCall {
			continue
		}
		info := callInfoOf(e.node, e.scope)
		base := lastSegment(info.callee)
		switch base {
		case "eval", "exec", "compile":
		default:
			continue
		}
		// Only the builtins are reported: a method that merely happens to be
		// called eval is not the same thing.
		if info.callee != base && info.callee != "builtins."+base {
			continue
		}
		if len(info.args) == 0 {
			continue
		}
		if isLiteral(info.args[0]) {
			continue
		}
		out = append(out, newFinding(r, sc, e.node,
			base+"() is called with a non-literal argument and can execute attacker-controlled code",
			model.Evidence{
				Source: info.args[0].Text,
				Sink:   base + "()",
				Note:   "only literal arguments are safe to evaluate",
			}))
	}
	return out
}

// checkPickleYamlUnsafeLoad reports unsafe deserialization calls.
func checkPickleYamlUnsafeLoad(sc *scanner) []model.Finding {
	r, _ := ruleByID(RulePickleYamlUnsafeLoad)
	var out []model.Finding
	for _, e := range sc.events {
		if e.kind != evtCall {
			continue
		}
		info := callInfoOf(e.node, e.scope)
		switch info.callee {
		case "pickle.load", "pickle.loads", "cPickle.load", "cPickle.loads",
			"_pickle.load", "_pickle.loads":
			out = append(out, newFinding(r, sc, e.node,
				"unsafe deserialization with "+info.callee+" can execute arbitrary code",
				model.Evidence{
					Sink: info.callee + "()",
					Note: "pickle deserializes arbitrary objects and can run code",
				}))
		case "yaml.load", "yaml.load_all":
			if usesSafeLoader(info) {
				continue
			}
			out = append(out, newFinding(r, sc, e.node,
				"unsafe YAML load without SafeLoader can construct arbitrary objects",
				model.Evidence{
					Sink: info.callee + "()",
					Note: "use yaml.safe_load or Loader=yaml.SafeLoader",
				}))
		case "yaml.unsafe_load":
			out = append(out, newFinding(r, sc, e.node,
				"unsafe YAML load without SafeLoader can construct arbitrary objects",
				model.Evidence{
					Sink: info.callee + "()",
					Note: "use yaml.safe_load or Loader=yaml.SafeLoader",
				}))
		}
	}
	return out
}

// usesSafeLoader reports whether a yaml load call selects a safe loader,
// either through the Loader keyword argument or through the second positional
// argument.
func usesSafeLoader(info callInfo) bool {
	if loader, ok := info.kwargs["Loader"]; ok {
		return strings.Contains(lastSegment(loader.Text), "SafeLoader")
	}
	if len(info.args) >= 2 {
		return strings.Contains(lastSegment(info.args[1].Text), "SafeLoader")
	}
	return false
}

// checkTempfileMktemp reports the use of tempfile.mktemp.
func checkTempfileMktemp(sc *scanner) []model.Finding {
	r, _ := ruleByID(RuleTempfileMktemp)
	var out []model.Finding
	for _, e := range sc.events {
		if e.kind != evtCall {
			continue
		}
		info := callInfoOf(e.node, e.scope)
		if info.callee != "tempfile.mktemp" {
			continue
		}
		out = append(out, newFinding(r, sc, e.node,
			"tempfile.mktemp is vulnerable to a race condition",
			model.Evidence{
				Sink: "tempfile.mktemp()",
				Note: "use tempfile.mkstemp or tempfile.NamedTemporaryFile",
			}))
	}
	return out
}

// checkWeakHashMD5SHA1 reports MD5 and SHA1 used for security purposes.
func checkWeakHashMD5SHA1(sc *scanner) []model.Finding {
	r, _ := ruleByID(RuleWeakHashMD5SHA1)
	var out []model.Finding
	for _, e := range sc.events {
		if e.kind != evtCall {
			continue
		}
		info := callInfoOf(e.node, e.scope)
		switch info.callee {
		case "hashlib.md5", "hashlib.sha1":
		default:
			continue
		}
		// usedforsecurity=False is the documented way to say the digest is
		// not used for a security purpose.
		if v, ok := info.kwargs["usedforsecurity"]; ok && isFalseLiteral(v) {
			continue
		}
		out = append(out, newFinding(r, sc, e.node,
			"weak hash algorithm "+info.callee+" is used where a strong algorithm is required",
			model.Evidence{
				Sink: info.callee + "()",
				Note: "use hashlib.sha256 or better",
			}))
	}
	return out
}

// secretNameParts are the substrings that mark a name as secret-like.
var secretNameParts = []string{
	"password", "passwd", "secret", "token", "api_key", "apikey", "api-key",
	"access_key", "accesskey", "private_key", "privatekey", "secret_key", "secretkey",
}

// benignSecretSuffixes mark names that look secret-like but hold metadata
// rather than a credential, such as token_type or token_count.
var benignSecretSuffixes = []string{
	"_type", "_types", "_count", "_name", "_names", "_len", "_length", "_id",
	"_ids", "_ttl", "_expiry", "_expires", "_format", "_prefix", "_suffix",
	"_regex", "_pattern", "_hash", "_encoder", "_decoder",
}

// minSecretLength is the minimum length of a string literal to be considered
// a hardcoded secret.
const minSecretLength = 8

// minSecretEntropy is the minimum Shannon entropy, in bits per character, for
// a string literal to be considered a hardcoded secret.
const minSecretEntropy = 3.0

// checkHardcodedSecret reports high-entropy string literals assigned to
// secret-like names.
func checkHardcodedSecret(sc *scanner) []model.Finding {
	r, _ := ruleByID(RuleHardcodedSecret)
	var out []model.Finding
	for _, e := range sc.events {
		if e.kind != evtAssign {
			continue
		}
		left, ok := e.node.Child("left")
		if !ok {
			continue
		}
		right, ok := e.node.Child("right")
		if !ok || right.Kind != "string" {
			continue
		}
		// Interpolated strings are not plain literals.
		if right.HasDescendant("interpolation") {
			continue
		}
		name := assignmentName(left)
		if name == "" || !isSecretName(name) {
			continue
		}
		value := stringLiteralValue(right)
		if len(value) < minSecretLength {
			continue
		}
		if shannonEntropy(value) < minSecretEntropy {
			continue
		}
		out = append(out, newFinding(r, sc, e.node,
			"hardcoded secret assigned to "+name,
			model.Evidence{
				Source: name,
				Sink:   left.Text,
				Note:   "move the secret to an environment variable or a secret manager",
			}))
	}
	return out
}

// assignmentName returns the name bound by the left-hand side of an
// assignment.
func assignmentName(left ast.Node) string {
	switch left.Kind {
	case "identifier":
		return left.Text
	case "attribute":
		if attr, ok := left.Child("attribute"); ok {
			return attr.Text
		}
	}
	return ""
}

// isSecretName reports whether a name looks like it holds a credential.
func isSecretName(name string) bool {
	lower := strings.ToLower(name)
	for _, suffix := range benignSecretSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return false
		}
	}
	for _, part := range secretNameParts {
		if strings.Contains(lower, part) {
			return true
		}
	}
	return false
}

// stringLiteralValue returns the content of a string literal node.
func stringLiteralValue(s ast.Node) string {
	var parts []string
	for _, c := range s.Children {
		if c.Kind == "string_content" {
			parts = append(parts, c.Text)
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, "")
	}
	return strings.Trim(s.Text, `"'`)
}

// shannonEntropy returns the Shannon entropy of a string in bits per
// character.
func shannonEntropy(s string) float64 {
	if s == "" {
		return 0
	}
	counts := map[rune]int{}
	total := 0
	for _, r := range s {
		counts[r]++
		total++
	}
	h := 0.0
	for _, c := range counts {
		p := float64(c) / float64(total)
		h -= p * math.Log2(p)
	}
	return h
}

// checkDebugTrueEnabled reports debug mode being switched on.
func checkDebugTrueEnabled(sc *scanner) []model.Finding {
	r, _ := ruleByID(RuleDebugTrueEnabled)
	var out []model.Finding
	for _, e := range sc.events {
		switch e.kind {
		case evtCall:
			info := callInfoOf(e.node, e.scope)
			switch lastSegment(info.callee) {
			case "run", "serve":
			default:
				continue
			}
			debug, ok := info.kwargs["debug"]
			if !ok || !isTrueLiteral(debug) {
				continue
			}
			out = append(out, newFinding(r, sc, e.node,
				"debug mode is enabled by "+info.callee+"()",
				model.Evidence{
					Source: "debug=True",
					Sink:   info.callee + "()",
					Note:   "the interactive debugger must not be reachable in production",
				}))
		case evtAssign:
			left, ok := e.node.Child("left")
			if !ok {
				continue
			}
			right, ok := e.node.Child("right")
			if !ok || !isTrueLiteral(right) {
				continue
			}
			name := strings.ToLower(assignmentName(left))
			if name != "debug" && !strings.HasSuffix(name, "_debug") {
				continue
			}
			out = append(out, newFinding(r, sc, e.node,
				"debug flag "+assignmentName(left)+" is enabled",
				model.Evidence{
					Source: assignmentName(left),
					Sink:   left.Text,
					Note:   "read the debug flag from the environment instead",
				}))
		}
	}
	return out
}

// checkAssertUsedForValidation reports assert statements that validate
// request-derived input.
func checkAssertUsedForValidation(sc *scanner) []model.Finding {
	r, _ := ruleByID(RuleAssertUsedForValidation)
	var out []model.Finding
	for _, e := range sc.events {
		if e.kind != evtAssert || len(e.node.Children) == 0 {
			continue
		}
		cond := e.node.Children[0]
		if !containsRequestSource(cond) {
			continue
		}
		hasComparison := cond.Has("comparison_operator")
		hasCall := cond.Has("call") || cond.Has("subscript")
		if !hasComparison && !hasCall {
			continue
		}
		out = append(out, newFinding(r, sc, e.node,
			"assert is used to validate request input and is removed under python -O",
			model.Evidence{
				Source: "request input",
				Sink:   "assert",
				Note:   "raise an explicit exception instead of asserting",
			}))
	}
	return out
}

// checkTLSVerifyDisabled reports disabled TLS certificate verification.
func checkTLSVerifyDisabled(sc *scanner) []model.Finding {
	r, _ := ruleByID(RuleTLSVerifyDisabled)
	var out []model.Finding
	for _, e := range sc.events {
		if e.kind != evtCall {
			continue
		}
		info := callInfoOf(e.node, e.scope)
		verify, ok := info.kwargs["verify"]
		if !ok || !isFalseLiteral(verify) {
			continue
		}
		head := firstSegment(info.callee)
		if !httpClientModules[head] {
			continue
		}
		out = append(out, newFinding(r, sc, e.node,
			"TLS certificate verification is disabled by verify=False",
			model.Evidence{
				Source: "verify=False",
				Sink:   info.callee + "()",
				Note:   "a machine-in-the-middle attacker can read and modify the traffic",
			}))
	}
	return out
}

// checkPathTraversal reports file paths derived from request parameters.
func checkPathTraversal(sc *scanner) []model.Finding {
	r, _ := ruleByID(RulePathTraversal)
	var out []model.Finding
	for _, e := range sc.events {
		if e.kind != evtCall {
			continue
		}
		info := callInfoOf(e.node, e.scope)
		switch lastSegment(info.rawCallee) {
		case "open", "send_file":
		default:
			continue
		}
		if len(info.args) == 0 {
			continue
		}
		pathArg := info.args[0]
		if src, ok := requestSourceNode(pathArg); ok {
			out = append(out, newFinding(r, sc, e.node,
				"file path is taken directly from a request parameter",
				model.Evidence{
					Source: src.Text,
					Sink:   info.rawCallee + "()",
					Note:   "resolve the path against a fixed base directory and reject traversal",
				}))
			continue
		}
		names := taintedNames(pathArg, e.scope)
		if len(names) == 0 {
			continue
		}
		out = append(out, newFinding(r, sc, e.node,
			"file path is derived from a request parameter",
			model.Evidence{
				Source: strings.Join(names, ", "),
				Sink:   info.rawCallee + "()",
				Note:   "resolve the path against a fixed base directory and reject traversal",
			}))
	}
	return out
}

// newFinding builds a canonical finding for a rule.
func newFinding(r Rule, sc *scanner, node ast.Node, message string, ev model.Evidence) model.Finding {
	severity := r.Severity
	if !severity.Valid() {
		severity = model.SeverityMedium
	}
	confidence := r.Confidence
	if confidence < 0 {
		confidence = 0
	}
	if confidence > 1 {
		confidence = 1
	}
	return model.Finding{
		ID:          model.FindingID(sc.file.Path, node.StartLine, r.CWE, r.ID),
		Rule:        r.ID,
		Tool:        ToolName,
		CWE:         r.CWE,
		Severity:    severity,
		Confidence:  confidence,
		File:        sc.file.Path,
		StartLine:   node.StartLine,
		EndLine:     node.EndLine,
		Code:        snippet(node.Text),
		Message:     message,
		Evidence:    ev,
		Remediation: remediationFor(r.ID),
		State:       model.StateOpen,
	}
}

// snippet shortens a source snippet to a length suitable for a finding.
func snippet(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxSnippetLen {
		return s
	}
	return s[:maxSnippetLen] + "..."
}

// isTrueLiteral reports whether a node is the literal True.
func isTrueLiteral(n ast.Node) bool { return n.Kind == "true" }

// isFalseLiteral reports whether a node is the literal False.
func isFalseLiteral(n ast.Node) bool { return n.Kind == "false" }

// isLiteral reports whether a node is a constant literal.
func isLiteral(n ast.Node) bool {
	switch n.Kind {
	case "string", "integer", "float", "true", "false", "none":
		return true
	}
	return false
}

// remediationFor returns the remediation hint of a rule.
func remediationFor(id string) string {
	switch id {
	case RuleSubprocessShellTrue:
		return "Pass the command as a list of arguments and leave shell=False."
	case RuleEvalExecNonliteral:
		return "Use ast.literal_eval for data, or avoid evaluating code entirely."
	case RulePickleYamlUnsafeLoad:
		return "Use json, or yaml.safe_load / Loader=yaml.SafeLoader."
	case RuleTempfileMktemp:
		return "Use tempfile.mkstemp or tempfile.NamedTemporaryFile."
	case RuleWeakHashMD5SHA1:
		return "Use hashlib.sha256 or hashlib.sha512."
	case RuleHardcodedSecret:
		return "Read the secret from an environment variable or a secret manager."
	case RuleDebugTrueEnabled:
		return "Take the debug flag from the environment and default it to False."
	case RuleAssertUsedForValidation:
		return "Raise an explicit exception when the input is invalid."
	case RuleTLSVerifyDisabled:
		return "Leave certificate verification enabled, or pin the expected certificate."
	case RulePathTraversal:
		return "Resolve the path against a fixed base directory and reject traversal sequences."
	}
	return ""
}
