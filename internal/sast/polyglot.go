package sast

import (
	"regexp"
	"strings"
)

// This file holds the pattern rules that cover every language other than
// Python.
//
// The Python rules walk the syntax tree and follow names through scopes. The
// rules here are deliberately simpler: they look at one call expression or one
// line at a time. That makes them language independent, because every grammar
// has a node for a call and every language writes a call as a callee followed
// by an argument list, and it keeps them predictable. They report the
// dangerous construct itself (a shell command built from a non-literal
// value, MD5 used as a hash, certificate verification switched off), not the
// path an attacker takes to it.
//
// The matching core in this file works on plain strings so that it can be
// tested without a parser. polyglot_scan.go connects it to the syntax tree.

// The identifiers of the rules that only the pattern rules produce.
const (
	// RuleXSSUnsafeOutput detects markup written to a page without escaping.
	RuleXSSUnsafeOutput = "xss-unsafe-output"
	// RuleSQLInjectionConcat detects a SQL statement built by concatenation.
	RuleSQLInjectionConcat = "sql-injection-concat"
	// RuleUnsafeCFunction detects C library functions that cannot bound a write.
	RuleUnsafeCFunction = "unsafe-c-function"
	// RuleFormatString detects a printf-style call with a non-literal format.
	RuleFormatString = "format-string"
	// RuleWeakCipher detects broken ciphers and the ECB mode.
	RuleWeakCipher = "weak-cipher"
)

// maxPatternCallBytes is the largest call expression the pattern rules look
// at. A call that spans more than this is generated or minified code.
const maxPatternCallBytes = 64 << 10

// maxPatternLineBytes is the longest line the line rules look at.
const maxPatternLineBytes = 2000

// pgHit is one match of a pattern rule.
type pgHit struct {
	rule    string
	line    int
	endLine int
	code    string
	message string
	sink    string
	hint    string
}

// pgCall is a call expression reduced to what the pattern rules need.
type pgCall struct {
	// callee is the called expression with argument lists removed and path
	// separators normalized to dots, for example "Runtime.getRuntime.exec".
	callee string
	// args is the text between the parentheses of the last argument list.
	args string
	// text is the whole call expression.
	text string
	// line and endLine bound the call in the source.
	line    int
	endLine int
}

// pgFile is the file-level context a rule may consult.
type pgFile struct {
	lang    string
	content string
}

// pgCallRule matches a call expression.
type pgCallRule struct {
	rule  string
	langs string
	hint  string
	// match returns the message and sink of a finding, or false.
	match func(c pgCall, f pgFile) (message, sink string, ok bool)
}

// pgLineRule matches a line of source.
type pgLineRule struct {
	rule    string
	langs   string
	hint    string
	pattern *regexp.Regexp
	message string
	// accept, when set, decides from the line and the match whether the hit
	// is real. It receives the text before and after the match.
	accept func(before, after string) bool
}

const (
	pgAll  = "go javascript typescript tsx java csharp ruby rust c cpp php scala"
	pgWeb  = "javascript typescript tsx"
	pgGo   = "go"
	pgJava = "java scala"
	pgCS   = "csharp"
	pgRb   = "ruby"
	pgRs   = "rust"
	pgC    = "c cpp"
)

// pgLangMatches reports whether a space-separated language list holds lang.
func pgLangMatches(langs, lang string) bool {
	for _, l := range strings.Fields(langs) {
		if l == lang {
			return true
		}
	}
	return false
}

// pgSplitCall splits the text of a call expression into its callee and the
// text of its argument list.
func pgSplitCall(text string) (callee, args string) {
	t := strings.TrimSpace(text)
	t = strings.TrimPrefix(t, "await ")
	t = strings.TrimPrefix(t, "new ")
	open, end := pgLastGroup(t)
	if open < 0 {
		// A call written without parentheses, as Ruby allows: the first word
		// is the callee and the rest is the argument.
		if i := strings.IndexAny(t, " \t"); i > 0 {
			return pgNormalizeCallee(t[:i]), strings.TrimSpace(t[i+1:])
		}
		return pgNormalizeCallee(t), ""
	}
	return pgNormalizeCallee(t[:open]), t[open+1 : end]
}

// pgLastGroup returns the offsets of the parentheses of the last top-level
// parenthesized group of s, or -1 when there is none. Parentheses inside
// string literals are ignored.
func pgLastGroup(s string) (open, end int) {
	open, end = -1, -1
	var stack []int
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == '\\' {
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'', '`':
			quote = c
		case '(':
			stack = append(stack, i)
		case ')':
			if len(stack) == 0 {
				continue
			}
			o := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				open, end = o, i
			}
		}
	}
	return open, end
}

// pgUntilClose returns the text of s up to the first closing parenthesis that
// has no opening one in s. Parentheses inside string literals are ignored.
func pgUntilClose(s string) string {
	depth := 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == '\\' {
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'', '`':
			quote = c
		case '(':
			depth++
		case ')':
			if depth == 0 {
				return s[:i]
			}
			depth--
		}
	}
	return s
}

// pgNormalizeCallee removes the argument lists from a callee and normalizes
// the path separators of the languages to a dot.
func pgNormalizeCallee(s string) string {
	var b strings.Builder
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				b.WriteByte(s[i])
			}
		}
	}
	out := strings.Join(strings.Fields(b.String()), "")
	out = strings.ReplaceAll(out, "::", ".")
	out = strings.ReplaceAll(out, "->", ".")
	out = strings.ReplaceAll(out, "?.", ".")
	return out
}

// pgSplitArgs splits an argument list at its top-level commas.
func pgSplitArgs(args string) []string {
	var out []string
	depth := 0
	var quote byte
	start := 0
	for i := 0; i < len(args); i++ {
		c := args[i]
		if quote != 0 {
			if c == '\\' {
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'', '`':
			quote = c
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(args[start:i]))
				start = i + 1
			}
		}
	}
	if rest := strings.TrimSpace(args[start:]); rest != "" || len(out) > 0 {
		out = append(out, rest)
	}
	return out
}

// pgPlainLiteral matches a string literal that carries no interpolation.
var pgPlainLiteral = regexp.MustCompile("^(?:\"(?:[^\"\\\\]|\\\\.)*\"|'(?:[^'\\\\]|\\\\.)*'|`[^`]*`)$")

// pgIsLiteral reports whether an argument is a constant string.
func pgIsLiteral(arg string) bool {
	a := strings.TrimSpace(arg)
	if !pgPlainLiteral.MatchString(a) {
		return false
	}
	return !strings.Contains(a, "${") && !strings.Contains(a, "#{")
}

// pgFirstArg returns the first argument of an argument list.
func pgFirstArg(args string) string {
	list := pgSplitArgs(args)
	if len(list) == 0 {
		return ""
	}
	return list[0]
}

// pgNonLiteralFirst reports whether the first argument is not a constant.
func pgNonLiteralFirst(args string) bool {
	first := pgFirstArg(args)
	return first != "" && !pgIsLiteral(first)
}

var (
	reGoShell       = regexp.MustCompile(`(?s)^\s*(?:[\w.]+\s*,\s*)?"(?:/bin/)?(?:sh|bash|zsh|dash|cmd(?:\.exe)?|powershell(?:\.exe)?|pwsh)"\s*,\s*"(?:-c|/c|-Command)"\s*,\s*(.+)$`)
	reJavaShell     = regexp.MustCompile(`(?s)^\s*"(?:/bin/)?(?:sh|bash|cmd(?:\.exe)?)"\s*,\s*"(?:-c|/c)"\s*,\s*(.+)$`)
	reRustShellArg  = regexp.MustCompile(`(?s)Command\s*::\s*new\s*\(\s*"(?:/bin/)?(?:sh|bash|cmd(?:\.exe)?|powershell)"\s*\)\s*\.arg\(\s*"(?:-c|/c)"\s*\)\s*\.arg\(`)
	reRustShellArgs = regexp.MustCompile(`(?s)Command\s*::\s*new\s*\(\s*"(?:/bin/)?(?:sh|bash|cmd(?:\.exe)?|powershell)"\s*\)\s*\.args\(\s*\[\s*"(?:-c|/c)"\s*,`)
	reShellTrue     = regexp.MustCompile(`shell\s*:\s*true`)
	reRubyExec      = regexp.MustCompile(`^(?:Kernel\.)?(?:system|exec|spawn|popen)$|^IO\.popen$|^Open3\.\w+$`)
	reNodeExec      = regexp.MustCompile(`^(?:child_process|childProcess|cp)\.(?:exec|execSync)$`)
	reNodeSpawn     = regexp.MustCompile(`^(?:child_process\.|childProcess\.|cp\.)?(?:spawn|spawnSync|execFile|execFileSync)$`)
	reNodeEval      = regexp.MustCompile(`^(?:eval|Function|vm\.runIn(?:New|This)?Context|vm\.Script|vm\.compileFunction)$`)
	reRubyEval      = regexp.MustCompile(`^(?:Kernel\.)?(?:eval|instance_eval|class_eval|module_eval)$`)
	reMD5SHA1       = regexp.MustCompile(`(?i)^\s*['"` + "`" + `](?:md5|sha-?1|md2)['"` + "`" + `]`)
	reJavaDigest    = regexp.MustCompile(`(?i)^\s*"(?:md5|sha-?1|md2)"`)
	reJavaCipher    = regexp.MustCompile(`(?i)^\s*"(?:(?:des|desede|rc4|rc2|arcfour|blowfish)(?:/[^"]*)?|aes|[^"]*/ecb/[^"]*)"`)
	reNodeCipher    = regexp.MustCompile(`(?i)^\s*['"` + "`" + `](?:des|rc4|rc2|bf|blowfish|[^'"` + "`" + `]*-ecb)`)
	reRubyCipher    = regexp.MustCompile(`(?i)['"](?:des|rc4|rc2|[^'"]*ecb)`)
	reCSDigest      = regexp.MustCompile(`^(?:System\.Security\.Cryptography\.)?(?:MD5|SHA1)(?:CryptoServiceProvider|Managed|Cng)?(?:\.(?:Create|HashData))?$`)
	reCSCipher      = regexp.MustCompile(`^(?:System\.Security\.Cryptography\.)?(?:DES|RC2)(?:CryptoServiceProvider)?(?:\.Create)?$`)
	reCSDeser       = regexp.MustCompile(`^(?:System\.[\w.]*)?(?:BinaryFormatter|NetDataContractSerializer|LosFormatter|SoapFormatter|ObjectStateFormatter)$`)
	reRustDigest    = regexp.MustCompile(`^(?:md5\.compute|(?:md5\.)?Md5\.(?:new|digest)|(?:sha1\.)?Sha1\.(?:new|digest|from))$`)
	reSQLCallee     = regexp.MustCompile(`(?i)(?:^|\.)(?:query|queryrow|querycontext|queryrowcontext|exec|execcontext|execute|executequery|executeupdate|preparestatement|preparecall|createquery|createnativequery|createsqlquery|raw|whereraw|find_by_sql|exec_query|select_all|where|sqlcommand|fromsqlraw|executesqlraw|mysql_query|sqlite3_exec|prepare|query_as|query_scalar)$`)
	reSQLKeyword    = regexp.MustCompile(`(?i)\b(?:select\s[^;]*?\sfrom|insert\s+into|update\s+\S+\s+set|delete\s+from|where\s)`)
	reSQLDynamic    = regexp.MustCompile("[\"'`]\\s*\\+|\\+\\s*[\"'`]|\\$\\{|#\\{|\\bSprintf\\b|String\\.format|\\bformat!|\\.format\\(|%[sdv]|\\$\"|string\\.Format|\\bconcat\\(")
	reFmtIdent      = regexp.MustCompile(`^[A-Za-z_][\w.\[\]]*$`)
)

// pgCallRules is the catalogue of the call rules.
var pgCallRules = []pgCallRule{
	// ---- command execution ------------------------------------------------
	{
		rule: RuleSubprocessShellTrue, langs: pgGo,
		hint: "Pass the program and its arguments separately to exec.Command and do not go through a shell.",
		match: func(c pgCall, f pgFile) (string, string, bool) {
			if c.callee != "exec.Command" && c.callee != "exec.CommandContext" {
				return "", "", false
			}
			m := reGoShell.FindStringSubmatch(c.args)
			if m == nil || pgIsLiteral(strings.TrimSpace(m[1])) {
				return "", "", false
			}
			return "a shell command is built from a non-literal value", c.callee + "()", true
		},
	},
	{
		rule: RuleSubprocessShellTrue, langs: pgWeb,
		hint: "Use execFile or spawn with an argument array and without a shell, and validate the input.",
		match: func(c pgCall, f pgFile) (string, string, bool) {
			switch {
			case reNodeExec.MatchString(c.callee):
			case (c.callee == "exec" || c.callee == "execSync") && strings.Contains(f.content, "child_process"):
			case reNodeSpawn.MatchString(c.callee) && reShellTrue.MatchString(c.args):
			default:
				return "", "", false
			}
			if !pgNonLiteralFirst(c.args) {
				return "", "", false
			}
			return "a shell command is built from a non-literal value", c.callee + "()", true
		},
	},
	{
		rule: RuleSubprocessShellTrue, langs: pgJava,
		hint: "Pass the program and its arguments as separate array elements and never concatenate input into a command.",
		match: func(c pgCall, f pgFile) (string, string, bool) {
			switch c.callee {
			case "Runtime.getRuntime.exec", "getRuntime.exec":
				if !pgNonLiteralFirst(c.args) {
					return "", "", false
				}
				return "Runtime.exec runs a command built from a non-literal value", "Runtime.exec()", true
			case "ProcessBuilder", "java.lang.ProcessBuilder":
				m := reJavaShell.FindStringSubmatch(c.args)
				if m == nil || pgIsLiteral(strings.TrimSpace(m[1])) {
					return "", "", false
				}
				return "ProcessBuilder runs a shell command built from a non-literal value", "ProcessBuilder()", true
			}
			return "", "", false
		},
	},
	{
		rule: RuleSubprocessShellTrue, langs: pgCS,
		hint: "Start the process with a fixed file name and pass the input as an argument, never as part of the command.",
		match: func(c pgCall, f pgFile) (string, string, bool) {
			if c.callee != "Process.Start" && c.callee != "System.Diagnostics.Process.Start" {
				return "", "", false
			}
			if !pgNonLiteralFirst(c.args) {
				return "", "", false
			}
			return "Process.Start runs a command built from a non-literal value", "Process.Start()", true
		},
	},
	{
		rule: RuleSubprocessShellTrue, langs: pgRb,
		hint: "Pass the command and its arguments as separate values so that no shell is involved.",
		match: func(c pgCall, f pgFile) (string, string, bool) {
			if !reRubyExec.MatchString(c.callee) || !pgNonLiteralFirst(c.args) {
				return "", "", false
			}
			return "a shell command is built from a non-literal value", c.callee + "()", true
		},
	},
	{
		rule: RuleSubprocessShellTrue, langs: pgRs,
		hint: "Run the program directly with Command::new(program).arg(value) instead of passing a string to a shell.",
		match: func(c pgCall, f pgFile) (string, string, bool) {
			if !strings.Contains(c.text, "Command") {
				return "", "", false
			}
			loc := reRustShellArg.FindStringIndex(c.text)
			if loc == nil {
				loc = reRustShellArgs.FindStringIndex(c.text)
			}
			if loc == nil {
				return "", "", false
			}
			rest := strings.TrimRight(strings.TrimSpace(pgUntilClose(c.text[loc[1]:])), "] \t\r\n")
			if first := pgFirstArg(rest); first == "" || pgIsLiteral(first) {
				return "", "", false
			}
			return "a shell command is built from a non-literal value", "Command::new()", true
		},
	},
	{
		rule: RuleSubprocessShellTrue, langs: pgC,
		hint: "Call execv with an argument vector instead of system or popen, and validate the input.",
		match: func(c pgCall, f pgFile) (string, string, bool) {
			if (c.callee != "system" && c.callee != "popen") || !pgNonLiteralFirst(c.args) {
				return "", "", false
			}
			return c.callee + " runs a command built from a non-literal value", c.callee + "()", true
		},
	},

	// ---- code evaluation --------------------------------------------------
	{
		rule: RuleEvalExecNonliteral, langs: pgWeb,
		hint: "Do not evaluate strings as code. Parse data with JSON.parse and call functions directly.",
		match: func(c pgCall, f pgFile) (string, string, bool) {
			if !reNodeEval.MatchString(c.callee) || !pgNonLiteralFirst(c.args) {
				return "", "", false
			}
			return c.callee + " evaluates a non-literal value as code", c.callee + "()", true
		},
	},
	{
		rule: RuleEvalExecNonliteral, langs: pgRb,
		hint: "Avoid eval. Use a lookup table or public_send with an allowlist of method names.",
		match: func(c pgCall, f pgFile) (string, string, bool) {
			if !reRubyEval.MatchString(c.callee) || !pgNonLiteralFirst(c.args) {
				return "", "", false
			}
			return c.callee + " evaluates a non-literal value as code", c.callee + "()", true
		},
	},

	// ---- deserialization --------------------------------------------------
	{
		rule: RulePickleYamlUnsafeLoad, langs: pgJava,
		hint: "Do not deserialize untrusted data with Java serialization. Use JSON, or set an ObjectInputFilter allowlist.",
		match: func(c pgCall, f pgFile) (string, string, bool) {
			if c.callee != "ObjectInputStream" && c.callee != "java.io.ObjectInputStream" && c.callee != "XMLDecoder" {
				return "", "", false
			}
			return "Java object deserialization can execute attacker-controlled code", c.callee + "()", true
		},
	},
	{
		rule: RulePickleYamlUnsafeLoad, langs: pgCS,
		hint: "Do not use BinaryFormatter or similar formatters on untrusted data. Use System.Text.Json.",
		match: func(c pgCall, f pgFile) (string, string, bool) {
			if !reCSDeser.MatchString(c.callee) {
				return "", "", false
			}
			return c.callee + " deserializes with a type-unsafe formatter", c.callee + "()", true
		},
	},
	{
		rule: RulePickleYamlUnsafeLoad, langs: pgRb,
		hint: "Use YAML.safe_load with permitted classes, or JSON. Never use Marshal on untrusted data.",
		match: func(c pgCall, f pgFile) (string, string, bool) {
			switch c.callee {
			case "Marshal.load", "Marshal.restore", "YAML.load", "Psych.load", "YAML.unsafe_load", "Psych.unsafe_load":
				return c.callee + " deserializes untrusted data and can execute code", c.callee + "()", true
			}
			return "", "", false
		},
	},
	{
		rule: RulePickleYamlUnsafeLoad, langs: pgWeb,
		hint: "Do not use node-serialize. Parse untrusted data as JSON.",
		match: func(c pgCall, f pgFile) (string, string, bool) {
			if c.callee != "serialize.unserialize" && c.callee != "nodeSerialize.unserialize" {
				return "", "", false
			}
			return "unserialize executes functions embedded in the data", c.callee + "()", true
		},
	},

	// ---- weak hashes ------------------------------------------------------
	{
		rule: RuleWeakHashMD5SHA1, langs: pgGo,
		hint: "Use crypto/sha256 or crypto/sha512.",
		match: func(c pgCall, f pgFile) (string, string, bool) {
			switch c.callee {
			case "md5.New", "md5.Sum", "sha1.New", "sha1.Sum":
				return "weak hash algorithm " + c.callee + " is used where a strong algorithm is required", c.callee + "()", true
			}
			return "", "", false
		},
	},
	{
		rule: RuleWeakHashMD5SHA1, langs: pgWeb,
		hint: "Use SHA-256 or better, for example crypto.createHash('sha256').",
		match: func(c pgCall, f pgFile) (string, string, bool) {
			if (strings.HasSuffix(c.callee, "createHash") || strings.HasSuffix(c.callee, ".digest")) && reMD5SHA1.MatchString(c.args) {
				return "weak hash algorithm is used where a strong algorithm is required", c.callee + "()", true
			}
			return "", "", false
		},
	},
	{
		rule: RuleWeakHashMD5SHA1, langs: pgJava,
		hint: "Use MessageDigest.getInstance(\"SHA-256\") or better.",
		match: func(c pgCall, f pgFile) (string, string, bool) {
			if c.callee == "MessageDigest.getInstance" && reJavaDigest.MatchString(c.args) {
				return "weak hash algorithm is used where a strong algorithm is required", "MessageDigest.getInstance()", true
			}
			return "", "", false
		},
	},
	{
		rule: RuleWeakHashMD5SHA1, langs: pgCS,
		hint: "Use SHA256.Create or SHA512.Create.",
		match: func(c pgCall, f pgFile) (string, string, bool) {
			if reCSDigest.MatchString(c.callee) && c.callee != "MD5" && c.callee != "SHA1" {
				return "weak hash algorithm " + c.callee + " is used where a strong algorithm is required", c.callee + "()", true
			}
			return "", "", false
		},
	},
	{
		rule: RuleWeakHashMD5SHA1, langs: pgRb,
		hint: "Use Digest::SHA256.",
		match: func(c pgCall, f pgFile) (string, string, bool) {
			name := strings.TrimPrefix(c.callee, "OpenSSL.")
			if name == "Digest.MD5" || name == "Digest.SHA1" || strings.HasPrefix(name, "Digest.MD5.") || strings.HasPrefix(name, "Digest.SHA1.") {
				return "weak hash algorithm " + c.callee + " is used where a strong algorithm is required", c.callee + "()", true
			}
			return "", "", false
		},
	},
	{
		rule: RuleWeakHashMD5SHA1, langs: pgRs,
		hint: "Use the sha2 crate (Sha256 or Sha512).",
		match: func(c pgCall, f pgFile) (string, string, bool) {
			if reRustDigest.MatchString(c.callee) {
				return "weak hash algorithm " + c.callee + " is used where a strong algorithm is required", c.callee + "()", true
			}
			return "", "", false
		},
	},
	{
		rule: RuleWeakHashMD5SHA1, langs: pgC,
		hint: "Use SHA-256 or better, for example EVP_sha256().",
		match: func(c pgCall, f pgFile) (string, string, bool) {
			switch c.callee {
			case "MD5", "SHA1", "MD5_Init", "SHA1_Init", "EVP_md5", "EVP_sha1":
				return "weak hash algorithm " + c.callee + " is used where a strong algorithm is required", c.callee + "()", true
			}
			return "", "", false
		},
	},

	// ---- weak ciphers -----------------------------------------------------
	{
		rule: RuleWeakCipher, langs: pgGo,
		hint: "Use AES with GCM (crypto/aes and cipher.NewGCM).",
		match: func(c pgCall, f pgFile) (string, string, bool) {
			switch c.callee {
			case "des.NewCipher", "des.NewTripleDESCipher", "rc4.NewCipher":
				return c.callee + " creates a broken cipher", c.callee + "()", true
			}
			return "", "", false
		},
	},
	{
		rule: RuleWeakCipher, langs: pgWeb,
		hint: "Use createCipheriv with aes-256-gcm and a random IV.",
		match: func(c pgCall, f pgFile) (string, string, bool) {
			switch {
			case strings.HasSuffix(c.callee, ".createCipher") || c.callee == "createCipher":
				return "createCipher derives the key without a salt and uses a fixed IV", c.callee + "()", true
			case (strings.HasSuffix(c.callee, ".createCipheriv") || c.callee == "createCipheriv") && reNodeCipher.MatchString(c.args):
				return "a broken cipher or the ECB mode is selected", c.callee + "()", true
			}
			return "", "", false
		},
	},
	{
		rule: RuleWeakCipher, langs: pgJava,
		hint: "Use AES/GCM/NoPadding. A bare \"AES\" means ECB mode.",
		match: func(c pgCall, f pgFile) (string, string, bool) {
			if c.callee == "Cipher.getInstance" && reJavaCipher.MatchString(c.args) {
				return "a broken cipher or the ECB mode is selected", "Cipher.getInstance()", true
			}
			return "", "", false
		},
	},
	{
		rule: RuleWeakCipher, langs: pgCS,
		hint: "Use AesGcm or Aes with a random IV.",
		match: func(c pgCall, f pgFile) (string, string, bool) {
			if reCSCipher.MatchString(c.callee) && c.callee != "DES" && c.callee != "RC2" {
				return c.callee + " is a broken cipher", c.callee + "()", true
			}
			return "", "", false
		},
	},
	{
		rule: RuleWeakCipher, langs: pgRb,
		hint: "Use OpenSSL::Cipher.new('aes-256-gcm').",
		match: func(c pgCall, f pgFile) (string, string, bool) {
			if c.callee == "OpenSSL.Cipher.new" && reRubyCipher.MatchString(c.args) {
				return "a broken cipher or the ECB mode is selected", "OpenSSL::Cipher.new()", true
			}
			return "", "", false
		},
	},
	{
		rule: RuleWeakCipher, langs: pgC,
		hint: "Use an AES-GCM construction such as EVP_aes_256_gcm.",
		match: func(c pgCall, f pgFile) (string, string, bool) {
			n := c.callee
			if strings.HasPrefix(n, "EVP_des") || strings.HasPrefix(n, "EVP_rc4") || strings.HasPrefix(n, "EVP_rc2") ||
				strings.HasPrefix(n, "DES_") || n == "RC4" || strings.HasSuffix(n, "_ecb") && strings.HasPrefix(n, "EVP_") {
				return n + " selects a broken cipher or the ECB mode", n + "()", true
			}
			return "", "", false
		},
	},

	// ---- SQL built by concatenation ----------------------------------------
	{
		rule: RuleSQLInjectionConcat, langs: pgAll,
		hint: "Use a parameterized query and pass the values separately from the statement.",
		match: func(c pgCall, f pgFile) (string, string, bool) {
			if !reSQLCallee.MatchString(c.callee) {
				return "", "", false
			}
			// The argument of "where" is a condition, not a whole statement, so
			// it carries no SQL keyword.
			fragment := strings.HasSuffix(c.callee, "where") || strings.HasSuffix(c.callee, "whereraw") ||
				strings.HasSuffix(c.callee, "find_by_sql")
			if !fragment && !reSQLKeyword.MatchString(c.args) {
				return "", "", false
			}
			if !reSQLDynamic.MatchString(c.args) {
				return "", "", false
			}
			return "a SQL statement is built by concatenating or formatting values into the text", c.callee + "()", true
		},
	},

	// ---- C library --------------------------------------------------------
	{
		rule: RuleUnsafeCFunction, langs: pgC,
		hint: "Use a bounded function: fgets, snprintf, strlcpy or strncat with an explicit size.",
		match: func(c pgCall, f pgFile) (string, string, bool) {
			switch c.callee {
			case "gets", "strcpy", "strcat", "sprintf", "vsprintf", "stpcpy":
				return c.callee + " cannot limit how much it writes and can overflow the destination", c.callee + "()", true
			case "scanf", "fscanf", "sscanf":
				if strings.Contains(c.args, "%s") {
					return c.callee + " with %s has no width limit and can overflow the destination", c.callee + "()", true
				}
			}
			return "", "", false
		},
	},
	{
		rule: RuleTempfileMktemp, langs: pgC,
		hint: "Use mkstemp, which creates the file atomically.",
		match: func(c pgCall, f pgFile) (string, string, bool) {
			switch c.callee {
			case "mktemp", "tmpnam", "tempnam":
				return c.callee + " is vulnerable to a race between the name and the file", c.callee + "()", true
			}
			return "", "", false
		},
	},
	{
		rule: RuleFormatString, langs: pgC,
		hint: "Pass a constant format string, for example printf(\"%s\", value).",
		match: func(c pgCall, f pgFile) (string, string, bool) {
			idx := -1
			switch c.callee {
			case "printf", "syslog":
				idx = 0
				if c.callee == "syslog" {
					idx = 1
				}
			case "fprintf", "dprintf":
				idx = 1
			}
			if idx < 0 {
				return "", "", false
			}
			list := pgSplitArgs(c.args)
			// A format with nothing after it is the dangerous shape: the
			// format is the last argument and it is a variable.
			if len(list) != idx+1 || !reFmtIdent.MatchString(list[idx]) {
				return "", "", false
			}
			return c.callee + " uses a non-literal format string", c.callee + "()", true
		},
	},
}

// pgLineRules is the catalogue of the line rules.
var pgLineRules = []pgLineRule{
	// ---- TLS verification -------------------------------------------------
	{rule: RuleTLSVerifyDisabled, langs: pgGo, pattern: regexp.MustCompile(`\bInsecureSkipVerify\s*[:=]+\s*true\b`),
		message: "InsecureSkipVerify disables certificate verification",
		hint:    "Leave InsecureSkipVerify false and trust the right roots through RootCAs."},
	{rule: RuleTLSVerifyDisabled, langs: pgWeb, pattern: regexp.MustCompile(`\brejectUnauthorized\s*:\s*false\b`),
		message: "rejectUnauthorized: false disables certificate verification",
		hint:    "Leave rejectUnauthorized enabled and add the CA with the ca option."},
	{rule: RuleTLSVerifyDisabled, langs: pgWeb, pattern: regexp.MustCompile(`NODE_TLS_REJECT_UNAUTHORIZED[^=\n]{0,6}=\s*['"]?0\b`),
		message: "NODE_TLS_REJECT_UNAUTHORIZED=0 disables certificate verification for the whole process",
		hint:    "Remove the override and trust the right CA instead."},
	{rule: RuleTLSVerifyDisabled, langs: pgJava, pattern: regexp.MustCompile(`ALLOW_ALL_HOSTNAME_VERIFIER|NoopHostnameVerifier|TrustAllStrategy|TrustSelfSignedStrategy|(?i:hostnameverifier).*->\s*true`),
		message: "the host name or certificate check is switched off",
		hint:    "Use the default host name verifier and a trust store that holds the right certificates."},
	{rule: RuleTLSVerifyDisabled, langs: pgCS, pattern: regexp.MustCompile(`ServerCertificateValidationCallback\s*\+?=.*=>\s*true|DangerousAcceptAnyServerCertificateValidator`),
		message: "the certificate validation callback accepts every certificate",
		hint:    "Remove the callback so that the platform validates certificates."},
	{rule: RuleTLSVerifyDisabled, langs: pgRb, pattern: regexp.MustCompile(`\bVERIFY_NONE\b`),
		message: "VERIFY_NONE disables certificate verification",
		hint:    "Use OpenSSL::SSL::VERIFY_PEER."},
	{rule: RuleTLSVerifyDisabled, langs: pgRs, pattern: regexp.MustCompile(`danger_accept_invalid_(?:certs|hostnames)\(\s*true\s*\)`),
		message: "the client accepts invalid certificates",
		hint:    "Remove the danger_accept_invalid call and add the root certificate you need."},
	{rule: RuleTLSVerifyDisabled, langs: pgC, pattern: regexp.MustCompile(`CURLOPT_SSL_VERIFY(?:PEER|HOST)\s*,\s*0|\bSSL_VERIFY_NONE\b`),
		message: "certificate verification is switched off",
		hint:    "Enable CURLOPT_SSL_VERIFYPEER (1) and CURLOPT_SSL_VERIFYHOST (2), or use SSL_VERIFY_PEER."},

	// ---- cross-site scripting --------------------------------------------
	{rule: RuleXSSUnsafeOutput, langs: pgWeb, pattern: regexp.MustCompile(`\.(?:innerHTML|outerHTML)\s*\+?=(?:[^=]|$)`),
		message: "markup is assigned to innerHTML without escaping",
		hint:    "Assign to textContent, or sanitize the markup with a library such as DOMPurify.",
		accept:  pgAcceptNonLiteralRest},
	{rule: RuleXSSUnsafeOutput, langs: pgWeb, pattern: regexp.MustCompile(`\bdocument\.write(?:ln)?\s*\(|\.insertAdjacentHTML\s*\(`),
		message: "markup is written to the page without escaping",
		hint:    "Build the nodes with createElement and textContent instead of writing markup.",
		accept:  pgAcceptNonLiteralRest},
	{rule: RuleXSSUnsafeOutput, langs: pgWeb, pattern: regexp.MustCompile(`\bdangerouslySetInnerHTML\b`),
		message: "dangerouslySetInnerHTML renders markup without escaping",
		hint:    "Render the value as text, or sanitize it before it reaches dangerouslySetInnerHTML."},
	{rule: RuleXSSUnsafeOutput, langs: pgGo, pattern: regexp.MustCompile(`\btemplate\.(?:HTML|JS|JSStr|CSS|URL)\(`),
		message: "a value is marked as safe and will not be escaped by the template",
		hint:    "Let html/template escape the value, and only convert constants.",
		accept:  pgAcceptNonLiteralRest},
	{rule: RuleXSSUnsafeOutput, langs: pgCS, pattern: regexp.MustCompile(`\bHtml\.Raw\(`),
		message: "Html.Raw writes the value without encoding",
		hint:    "Write the value with the default encoding, or sanitize the markup first.",
		accept:  pgAcceptNonLiteralRest},
	{rule: RuleXSSUnsafeOutput, langs: pgRb, pattern: regexp.MustCompile(`\.html_safe\b|\braw\s*\(`),
		message: "a value is marked as safe and will not be escaped",
		hint:    "Remove html_safe and raw, or sanitize the value with the sanitize helper.",
		accept:  pgAcceptRubySafe},
	{rule: RuleXSSUnsafeOutput, langs: pgJava, pattern: regexp.MustCompile(`\.(?:print|println|write)\(.*getParameter\(`),
		message: "a request parameter is written to the response without encoding",
		hint:    "Encode the value for HTML, for example with the OWASP Java Encoder."},
}

// pgAcceptNonLiteralRest accepts a hit when what follows the match is not a
// constant string.
func pgAcceptNonLiteralRest(_, after string) bool {
	rest := strings.TrimSpace(after)
	rest = strings.TrimRight(rest, "); \t")
	if rest == "" {
		return true
	}
	return !pgIsLiteral(pgFirstArg(rest))
}

// pgAcceptRubySafe accepts a hit unless the marked value is a constant.
func pgAcceptRubySafe(before, after string) bool {
	b := strings.TrimSpace(before)
	if strings.HasSuffix(b, `"`) || strings.HasSuffix(b, `'`) {
		return strings.Contains(b, "#{")
	}
	rest := strings.TrimSpace(after)
	if strings.HasPrefix(rest, `"`) || strings.HasPrefix(rest, `'`) {
		return pgAcceptNonLiteralRest("", rest)
	}
	return true
}

// reSecretAssign matches a name assigned a string literal: "name = 'value'",
// "name := "value"", "name: 'value'" and the quoted-key JSON form.
var reSecretAssign = regexp.MustCompile("[\"']?([A-Za-z_][\\w.\\-]*)[\"']?\\s*(?::=|=|:)\\s*(?:\"((?:[^\"\\\\]|\\\\.)*)\"|'((?:[^'\\\\]|\\\\.)*)'|`([^`$]*)`)")

// pgCommentLine reports whether a line is only a comment.
func pgCommentLine(trimmed string) bool {
	return strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") ||
		strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") ||
		strings.HasPrefix(trimmed, "--")
}

// pgSecretHit looks for a hardcoded secret on one line.
func pgSecretHit(line string) (name string, ok bool) {
	for _, m := range reSecretAssign.FindAllStringSubmatch(line, -1) {
		n := m[1]
		if i := strings.LastIndex(n, "."); i >= 0 {
			n = n[i+1:]
		}
		if !isSecretName(n) {
			continue
		}
		value := m[2] + m[3] + m[4]
		if len(value) < minSecretLength || shannonEntropy(value) < minSecretEntropy {
			continue
		}
		if strings.ContainsAny(value, "{}$") || strings.Contains(value, "://") {
			continue
		}
		return n, true
	}
	return "", false
}

// pgScanLines applies the line rules and the secret rule to a file.
func pgScanLines(f pgFile) []pgHit {
	var out []pgHit
	lines := strings.Split(f.content, "\n")
	for i, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		if len(line) > maxPatternLineBytes {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || pgCommentLine(trimmed) {
			continue
		}
		for _, r := range pgLineRules {
			if !pgLangMatches(r.langs, f.lang) {
				continue
			}
			loc := r.pattern.FindStringIndex(line)
			if loc == nil {
				continue
			}
			if r.accept != nil && !r.accept(line[:loc[0]], line[loc[1]:]) {
				continue
			}
			out = append(out, pgHit{
				rule: r.rule, line: i + 1, endLine: i + 1, code: trimmed,
				message: r.message, sink: strings.TrimSpace(line[loc[0]:loc[1]]), hint: r.hint,
			})
		}
		if name, ok := pgSecretHit(line); ok {
			out = append(out, pgHit{
				rule: RuleHardcodedSecret, line: i + 1, endLine: i + 1, code: trimmed,
				message: "hardcoded secret assigned to " + name, sink: name,
				hint: "Read the secret from an environment variable or a secret manager.",
			})
		}
	}
	return out
}

// pgMatchCall applies the call rules to one call expression.
func pgMatchCall(c pgCall, f pgFile) []pgHit {
	var out []pgHit
	for _, r := range pgCallRules {
		if !pgLangMatches(r.langs, f.lang) {
			continue
		}
		msg, sink, ok := r.match(c, f)
		if !ok {
			continue
		}
		out = append(out, pgHit{
			rule: r.rule, line: c.line, endLine: c.endLine, code: c.text,
			message: msg, sink: sink, hint: r.hint,
		})
	}
	return out
}

// pgDedupe keeps the first hit of every rule on every line, in order.
func pgDedupe(hits []pgHit) []pgHit {
	type key struct {
		rule string
		line int
	}
	seen := map[key]bool{}
	out := hits[:0:0]
	for _, h := range hits {
		k := key{h.rule, h.line}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, h)
	}
	return out
}
