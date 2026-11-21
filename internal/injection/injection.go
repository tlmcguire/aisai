// Package injection detects instruction-like text inside audited source
// code.
//
// The audited package is attacker-controlled. Its contents may try to steer
// the model — to make it ignore the task, adopt a new persona, or leak its
// prompt. Anything that reads like an instruction is therefore treated as a
// finding rather than as content.
//
// The detectors live in their own package because two very different paths
// send package source to a model: the chat path, which retrieves chunks to
// answer a question, and the scan path, which asks the model to find
// vulnerabilities. Both need the same quarantine.
package injection

import (
	"encoding/base64"
	"regexp"
	"unicode/utf8"
)

// injectionPattern is one detector for instruction-like text.
type injectionPattern struct {
	// name identifies the detector in the quarantine report.
	name string
	// re matches the suspicious text.
	re *regexp.Regexp
}

// injectionPatterns are the detectors for instruction-like text inside
// source code.
//
// The audited package is attacker-controlled, so text that tries to steer the
// model is treated as a finding, not as content. The detectors are
// deliberately narrow (role markers have to start a line) so that ordinary
// code — a call to os.system, a "system:" YAML key — is not quarantined.
//
// The order of this slice is part of the contract: Detect returns the first
// detector that matches, and the injection corpus in internal/injection
// records the detector name that each case is expected to produce. A new
// detector is therefore appended at the end, never inserted in the middle, so
// that adding one never changes the verdict of an existing case.
//
// A quarantined chunk is not silently dropped: the scan path returns a
// QuarantineError that names the detector, and the chat path tells the user
// how many chunks were withheld. The cost of a false positive is therefore a
// visible loss of coverage, never a silent one.
var injectionPatterns = []injectionPattern{
	{
		name: "ignore-previous-instructions",
		re:   regexp.MustCompile(`(?i)ignore[\s_]+(?:all[\s_]+|any[\s_]+|the[\s_]+)?(?:previous|prior|above|earlier|preceding|foregoing)[\s_]+(?:instructions?|prompts?|commands?|rules?|directions?|messages?|context)`),
	},
	{
		name: "disregard-above",
		re:   regexp.MustCompile(`(?i)disregard\s+(?:all\s+|any\s+)?(?:previous|prior|above|the\s+above|earlier|preceding)\b`),
	},
	{
		name: "forget-previous",
		re:   regexp.MustCompile(`(?i)forget\s+(?:everything|all\s+(?:previous|prior|above)\b|the\s+above)`),
	},
	{
		name: "you-are-now",
		re:   regexp.MustCompile(`(?i)\byou\s+are\s+now\b`),
	},
	{
		name: "new-instructions",
		re:   regexp.MustCompile(`(?i)\bnew\s+(?:instructions?|prompt|rules?)\s*:`),
	},
	{
		name: "ai-assistant-role",
		re:   regexp.MustCompile(`(?im)^[ \t]*(?:\*\*)?AI[ \t]*assistant(?:\*\*)?[ \t]*:`),
	},
	{
		name: "system-role",
		re:   regexp.MustCompile(`(?im)^[ \t]*(?:\*\*)?system(?:\*\*)?[ \t]*:`),
	},
	{
		name: "assistant-role",
		re:   regexp.MustCompile(`(?im)^[ \t]*(?:\*\*)?assistant(?:\*\*)?[ \t]*:`),
	},
	{
		name: "user-role",
		re:   regexp.MustCompile(`(?im)^[ \t]*(?:\*\*)?(?:user|human)(?:\*\*)?[ \t]*:`),
	},
	{
		name: "developer-role",
		re:   regexp.MustCompile(`(?im)^[ \t]*(?:\*\*)?developer(?:\*\*)?[ \t]*:`),
	},
	{
		name: "instruction-header",
		re:   regexp.MustCompile(`(?i)#{2,}[ \t]*instruction`),
	},
	{
		name: "chatml-marker",
		re:   regexp.MustCompile(`<\|(?:im_start|im_end|system|user|assistant|endoftext|eot_id)\|>`),
	},
	{
		name: "override-instructions",
		re:   regexp.MustCompile(`(?i)override\s+(?:your\s+|all\s+|the\s+|any\s+)?(?:previous\s+|prior\s+|above\s+)?(?:instructions?|rules?|guidelines?|system\s+prompt)`),
	},
	{
		name: "reveal-prompt",
		re:   regexp.MustCompile(`(?i)(?:reveal|print|show|repeat|output|disclose)\s+(?:me\s+)?(?:your\s+|the\s+)?(?:full\s+|initial\s+|original\s+|system\s+)*(?:prompt|instructions?|directives)`),
	},
	{
		name: "conceal-from-user",
		re:   regexp.MustCompile(`(?i)do\s+not\s+(?:tell|inform|mention\s+to|show)\s+the\s+user`),
	},

	// The detectors below close the gaps that the injection corpus recorded
	// as known false negatives. Each one is narrow enough to leave the
	// ordinary-code traps of the corpus alone.

	{
		// A role marker at the start of a comment. A comment is the most
		// natural hiding place in Python, and the bare role markers above
		// only fire at the start of a line, so "# system: ..." used to slip
		// through.
		//
		// The role word has to be the first thing after the comment marker.
		// A comment that merely mentions a role — "# The system is fine." —
		// does not match.
		name: "role-marker-in-comment",
		re:   regexp.MustCompile(`(?im)^[ \t]*(?:#+|//+|/\*+|<!--)[ \t]*(?:\*\*)?(?:AI[ \t]*)?(?:system|assistant|user|human|developer)(?:\*\*)?[ \t]*:`),
	},
	{
		// An instruction that asks the auditor to drop a finding. The
		// object is restricted to the words a real instruction uses, so a
		// comment such as "do not report the error" in a logging module is
		// not quarantined.
		name: "suppress-finding-directive",
		re:   regexp.MustCompile(`(?i)(?:do\s+not|don'?t|never)\s+(?:report|flag|raise|mention|include)\s+(?:this|it|these|them|any|vulnerabilit\w+|security\s+issues?|issues?|findings?)`),
	},
	{
		// An impersonation of the auditor or of its policy. The text may
		// name the tool, the auditor, or a security policy.
		name: "impersonate-auditor",
		re:   regexp.MustCompile(`(?i)\b(?:aisai|the\s+auditor|security\s+policy|the\s+policy|this\s+policy)\b[^.\n]{0,60}\b(?:allows?|permits?|approves?|authoriz\w+|exempts?)\b`),
	},
	{
		// A fake tool-call or JSON-RPC envelope. The envelope alone is not
		// enough: a package may legitimately implement a JSON-RPC client.
		// The envelope has to name a capability that would let it act —
		// a shell, a file, or the network.
		name: "fake-tool-call-envelope",
		re:   regexp.MustCompile(`(?i)["']?(?:jsonrpc|tool_call|tool_calls|function_call|tool_use)["']?\s*:\s*[^\n]{0,300}?["']?(?:shell|exec|command|bash|read_file|write_file|delete_file|curl|wget|http[s]?://|rm\b|upload|download)["']?`),
	},
	{
		// An attempt to have the model hand out a local credential file.
		// The detector needs all three parts: a request verb, a word for
		// the contents, and the path. A package that merely prints the
		// path of a configuration file is not an attack, and a package that
		// asks for the contents of a credential file is.
		name: "local-file-exfiltration",
		re:   regexp.MustCompile(`(?i)(?:print|show|reveal|output|display|read|cat|dump|send|upload|email|exfiltrat\w+|include)\b[^\n]{0,40}\b(?:contents?|text|data|body|creds?|credentials?|values?)\b[^\n]{0,40}(?:~/\.aws/credentials|~/\.ssh/id_[a-z0-9]+|~/\.ssh/authorized_keys|~/\.netrc|~/\.config/gcloud|/etc/shadow|id_rsa)`),
	},
	{
		// The same instruction-override phrases in Chinese. An audit of a
		// Chinese package must not be the one case an attacker gets for
		// free.
		name: "ignore-previous-instructions-zh",
		re:   regexp.MustCompile(`(?:请\s*)?忽略\s*(?:掉)?\s*(?:所有|之前|以上|上述|前面|先前|以上所有)?\s*(?:的)?\s*(?:指令|指示|命令|规则|要求|提示词|提示)`),
	},
	{
		// The same instruction-override phrases in Japanese, Korean,
		// Spanish, French, German and Portuguese. Each phrase is the
		// equivalent of "ignore the previous instructions".
		name: "ignore-previous-instructions-i18n",
		re: regexp.MustCompile(`(?:` +
			`無視(?:して|し)?(?:ください|なさい)|(?:前の|これまでの|以前の)(?:指示|命令|ルール)を(?:無視|忘れ)|` +
			`(?:이전|위의)\s*(?:지시|명령|규칙)(?:을|를)?\s*(?:무시|잊어)|` +
			`ignora(?:r)?\s+(?:todas\s+)?las\s+(?:instrucciones|indicaciones|reglas)|` +
			`ignorez\s+(?:toutes\s+)?les\s+(?:instructions|r[eè]gles)|` +
			`ignoriere?\s+(?:alle\s+)?(?:die\s+)?(?:Anweisungen|Regeln)|` +
			`ignore\s+(?:todas\s+)?as\s+(?:instru(?:ç|c)(?:õ|o)es|regras)` +
			`)`),
	},
	{
		// A directive to mark the file as safe, in Chinese.
		name: "mark-safe-directive-zh",
		re:   regexp.MustCompile(`(?:将|把)\s*(?:此|该|本)?\s*(?:文件|模块|代码|包)\s*(?:标记|標記|视为|視為|判定)\s*(?:为|為)?\s*(?:安全|無漏洞|无漏洞|干净|乾淨)`),
	},
}

// Detect reports whether text contains instruction-like text that tries to
// steer the model.
//
// It returns the name of the detector that matched and true when the text
// looks hostile. A false result means the text may be shown to the model.
//
// The base64 check runs after the textual detectors: an encoded payload is a
// deliberate obfuscation, so it is worth the decode, but a chunk that
// contains a plain instruction is reported by the detector that named it.
func Detect(text string) (string, bool) {
	if text == "" {
		return "", false
	}
	for _, p := range injectionPatterns {
		if p.re.MatchString(text) {
			return p.name, true
		}
	}
	if detectEncodedInjection(text) {
		return "base64-encoded-instruction", true
	}
	return "", false
}

// base64Candidate matches a run of characters that could be a base64 payload.
//
// The run has to be long enough that a decoded instruction fits in it, and it
// has to be delimited so that ordinary identifiers are not mistaken for one.
var base64Candidate = regexp.MustCompile(`[A-Za-z0-9+/]{24,}={0,2}`)

// maxEncodedCandidates bounds the number of payloads one chunk may hold.
//
// The cost of the check is a decode per candidate, and a chunk is a bounded
// piece of source, so the limit is generous.
const maxEncodedCandidates = 64

// detectEncodedInjection reports whether the text holds a base64 payload that
// decodes to an instruction.
//
// A payload is only reported when the decoded bytes are valid UTF-8 and match
// one of the textual detectors. Decoding first and matching afterwards is
// what keeps the false-positive rate down: a random base64 blob decodes to
// bytes that are almost never a valid instruction.
func detectEncodedInjection(text string) bool {
	candidates := base64Candidate.FindAllString(text, maxEncodedCandidates)
	for _, candidate := range candidates {
		decoded, ok := decodeBase64(candidate)
		if !ok {
			continue
		}
		for _, p := range injectionPatterns {
			if p.re.MatchString(decoded) {
				return true
			}
		}
	}
	return false
}

// decodeBase64 decodes a base64 payload, trying the standard, the raw and
// the URL-safe alphabets.
func decodeBase64(payload string) (string, bool) {
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	for _, enc := range encodings {
		raw, err := enc.DecodeString(payload)
		if err != nil {
			continue
		}
		// The decoded bytes have to be text for the detectors to have
		// anything to match.
		if !utf8.Valid(raw) {
			continue
		}
		return string(raw), true
	}
	return "", false
}
