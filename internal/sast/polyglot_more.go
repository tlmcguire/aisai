package sast

import "regexp"

// This file extends the pattern rules to PHP and to shell scripts. Scala and
// C++ reuse the Java and C rules (see pgJava and pgC in polyglot.go).

const (
	pgPHP  = "php"
	pgBash = "bash"
)

var (
	rePHPExec      = regexp.MustCompile(`^(?:system|exec|shell_exec|passthru|popen|proc_open|pcntl_exec)$`)
	rePHPEval      = regexp.MustCompile(`^(?:eval|assert|create_function|preg_replace_callback_array)$`)
	rePHPDeser     = regexp.MustCompile(`^(?:unserialize|yaml_parse)$`)
	rePHPDigest    = regexp.MustCompile(`^(?:md5|sha1|crypt|md5_file|sha1_file)$`)
	rePHPCipher    = regexp.MustCompile(`(?i)^\s*['"](?:des|rc4|rc2|bf|[^'"]*ecb)`)
	rePHPInclude   = regexp.MustCompile(`^(?:include|include_once|require|require_once)$`)
	rePHPSQL       = regexp.MustCompile(`(?i)(?:^|\.)(?:query|exec|prepare|mysqli_query|mysql_query|pg_query|sqlite_query|real_query|multi_query)$`)
	rePHPInterp    = regexp.MustCompile(`"[^"]*\$[A-Za-z_]|\.\s*\$`)
	rePHPSafeUnser = regexp.MustCompile(`allowed_classes\s*['"]?\s*=>\s*false`)
	rePHPRequest   = regexp.MustCompile(`\$_(?:GET|POST|REQUEST|COOKIE|FILES|SERVER)\b`)
	reBashPipeSh   = regexp.MustCompile(`\b(?:curl|wget)\b[^|;&\n]*\|\s*(?:sudo\s+)?(?:ba|z|da)?sh\b`)
	reBashEval     = regexp.MustCompile(`(?:^|[;&|]\s*|\s)eval\s+["']?\$`)
	reBashNoVerif  = regexp.MustCompile(`\bcurl\b[^\n]*\s(?:-k|--insecure)\b|\bwget\b[^\n]*--no-check-certificate`)
)

func init() {
	pgCallRules = append(pgCallRules,
		pgCallRule{
			rule: RuleSubprocessShellTrue, langs: pgPHP,
			hint: "Avoid shell execution. Use escapeshellarg on every argument, or call a library function instead.",
			match: func(c pgCall, f pgFile) (string, string, bool) {
				if !rePHPExec.MatchString(c.callee) || !pgNonLiteralFirst(c.args) {
					return "", "", false
				}
				return c.callee + " runs a command built from a non-literal value", c.callee + "()", true
			},
		},
		pgCallRule{
			rule: RuleEvalExecNonliteral, langs: pgPHP,
			hint: "Do not evaluate strings as code. Dispatch through an allowlist instead.",
			match: func(c pgCall, f pgFile) (string, string, bool) {
				if !rePHPEval.MatchString(c.callee) || !pgNonLiteralFirst(c.args) {
					return "", "", false
				}
				return c.callee + " evaluates a non-literal value as code", c.callee + "()", true
			},
		},
		pgCallRule{
			rule: RuleEvalExecNonliteral, langs: pgPHP,
			hint: "Validate the path against an allowlist; never include a path taken from a request.",
			match: func(c pgCall, f pgFile) (string, string, bool) {
				if !rePHPInclude.MatchString(c.callee) || !rePHPRequest.MatchString(c.args) {
					return "", "", false
				}
				return c.callee + " loads a file chosen by the request", c.callee, true
			},
		},
		pgCallRule{
			rule: RulePickleYamlUnsafeLoad, langs: pgPHP,
			hint: "Do not unserialize untrusted data. Use json_decode, or pass allowed_classes => false.",
			match: func(c pgCall, f pgFile) (string, string, bool) {
				if !rePHPDeser.MatchString(c.callee) {
					return "", "", false
				}
				if c.callee == "unserialize" && rePHPSafeUnser.MatchString(c.args) {
					return "", "", false
				}
				return c.callee + " deserializes data and can instantiate attacker-chosen objects", c.callee + "()", true
			},
		},
		pgCallRule{
			rule: RuleWeakHashMD5SHA1, langs: pgPHP,
			hint: "Use hash('sha256', ...) for digests and password_hash for passwords.",
			match: func(c pgCall, f pgFile) (string, string, bool) {
				if !rePHPDigest.MatchString(c.callee) {
					return "", "", false
				}
				return "weak hash algorithm " + c.callee + " is used where a strong algorithm is required", c.callee + "()", true
			},
		},
		pgCallRule{
			rule: RuleWeakCipher, langs: pgPHP,
			hint: "Use openssl_encrypt with aes-256-gcm.",
			match: func(c pgCall, f pgFile) (string, string, bool) {
				if c.callee != "openssl_encrypt" && c.callee != "openssl_decrypt" {
					return "", "", false
				}
				if !rePHPCipher.MatchString(pgFirstArgOfSecond(c.args)) {
					return "", "", false
				}
				return "a broken cipher or the ECB mode is used", c.callee + "()", true
			},
		},
		pgCallRule{
			rule: RuleSQLInjectionConcat, langs: pgPHP,
			hint: "Use prepared statements with bound parameters.",
			match: func(c pgCall, f pgFile) (string, string, bool) {
				if !rePHPSQL.MatchString(c.callee) || !reSQLKeyword.MatchString(c.args) {
					return "", "", false
				}
				if !reSQLDynamic.MatchString(c.args) && !rePHPInterp.MatchString(c.args) {
					return "", "", false
				}
				return "a SQL statement is built by concatenating or interpolating values into the text", c.callee + "()", true
			},
		},
	)

	pgLineRules = append(pgLineRules,
		pgLineRule{rule: RuleXSSUnsafeOutput, langs: pgPHP,
			pattern: regexp.MustCompile(`\b(?:echo|print)\b[^;]*\$_(?:GET|POST|REQUEST|COOKIE)\b`),
			message: "a request value is written to the page without escaping",
			hint:    "Escape the value with htmlspecialchars($v, ENT_QUOTES, 'UTF-8')."},
		pgLineRule{rule: RuleTLSVerifyDisabled, langs: pgPHP,
			pattern: regexp.MustCompile(`CURLOPT_SSL_VERIFY(?:PEER|HOST)\s*,\s*(?:false|0)|'verify'\s*=>\s*false`),
			message: "certificate verification is switched off",
			hint:    "Leave verification on and add the CA bundle you need."},
		pgLineRule{rule: RuleDebugTrueEnabled, langs: pgPHP,
			pattern: regexp.MustCompile(`\bdisplay_errors['"]?\s*[,=]\s*['"]?(?:1|on|true)\b|\bAPP_DEBUG['"]?\s*[,=]\s*true`),
			message: "error details are shown to the client",
			hint:    "Turn display_errors off in production and log errors instead."},

		pgLineRule{rule: RuleSubprocessShellTrue, langs: pgBash, pattern: reBashPipeSh,
			message: "a download is piped straight into a shell",
			hint:    "Download the script, verify its checksum, then run it."},
		pgLineRule{rule: RuleEvalExecNonliteral, langs: pgBash, pattern: reBashEval,
			message: "eval runs a value built from a variable as code",
			hint:    "Avoid eval. Use arrays and \"${var}\" expansion without re-parsing."},
		pgLineRule{rule: RuleTLSVerifyDisabled, langs: pgBash, pattern: reBashNoVerif,
			message: "certificate verification is switched off",
			hint:    "Remove -k, --insecure and --no-check-certificate and trust the right CA."},
		pgLineRule{rule: RuleTempfileMktemp, langs: pgBash,
			pattern: regexp.MustCompile(`\bmktemp\s+-u\b`),
			message: "mktemp -u only prints a name, so the file can be raced or symlinked",
			hint:    "Create the file with plain mktemp and clean it up with trap."},
	)
}

// pgFirstArgOfSecond returns the second argument of an argument list.
func pgFirstArgOfSecond(args string) string {
	list := pgSplitArgs(args)
	if len(list) < 2 {
		return ""
	}
	return list[1]
}
