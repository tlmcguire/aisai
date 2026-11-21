package pypi

import (
	"fmt"
	"os"
	"runtime"
	"strings"
)

// MarkerEnv holds the environment values used when evaluating PEP 508
// environment markers.
type MarkerEnv struct {
	// PythonVersion is the Python version a requirement applies to, e.g. "3.13".
	PythonVersion string
	// PythonFullVersion is the full Python version, e.g. "3.13.1".
	PythonFullVersion string
	// OSName is "posix" or "nt".
	OSName string
	// SysPlatform is e.g. "linux", "darwin" or "win32".
	SysPlatform string
	// PlatformMachine is e.g. "x86_64" or "arm64".
	PlatformMachine string
	// PlatformSystem is e.g. "Linux" or "Darwin".
	PlatformSystem string
	// PlatformRelease is the OS release string.
	PlatformRelease string
	// PlatformVersion is the OS version string.
	PlatformVersion string
	// Implementation is e.g. "cpython".
	Implementation string
	// ImplementationVersion is the interpreter version.
	ImplementationVersion string
	// Extra is the requested extra name; the empty string means "no extra".
	Extra string
}

// defaultPythonVersion is the Python version used to evaluate markers when
// the caller does not specify one.
const defaultPythonVersion = "3.13"

// DefaultMarkerEnv returns the marker environment used by the resolver.
//
// The Python version can be overridden with the AISAI_PYTHON_VERSION
// environment variable so that a caller can resolve against the interpreter
// version of the audited project's target environment.
func DefaultMarkerEnv() MarkerEnv {
	pyVersion := strings.TrimSpace(os.Getenv("AISAI_PYTHON_VERSION"))
	if pyVersion == "" {
		pyVersion = defaultPythonVersion
	}
	env := MarkerEnv{
		PythonVersion:         pyVersion,
		PythonFullVersion:     pyVersion,
		OSName:                "posix",
		SysPlatform:           runtime.GOOS,
		PlatformMachine:       runtime.GOARCH,
		PlatformSystem:        runtime.GOOS,
		PlatformRelease:       "",
		PlatformVersion:       "",
		Implementation:        "cpython",
		ImplementationVersion: pyVersion,
		Extra:                 "",
	}
	switch runtime.GOOS {
	case "windows":
		env.OSName = "nt"
		env.SysPlatform = "win32"
		env.PlatformSystem = "Windows"
	case "darwin":
		env.PlatformSystem = "Darwin"
	case "linux":
		env.PlatformSystem = "Linux"
	default:
		env.PlatformSystem = runtime.GOOS
	}
	return env
}

// markerTokenKind enumerates the token kinds of a marker expression.
type markerTokenKind int

const (
	markerIdent markerTokenKind = iota
	markerString
	markerOp
	markerLParen
	markerRParen
	markerEnd
)

// markerToken is one token of a marker expression.
type markerToken struct {
	kind markerTokenKind
	text string
}

// tokenizeMarker splits a marker expression into tokens.
func tokenizeMarker(s string) ([]markerToken, error) {
	var tokens []markerToken
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '(':
			tokens = append(tokens, markerToken{kind: markerLParen, text: "("})
			i++
		case c == ')':
			tokens = append(tokens, markerToken{kind: markerRParen, text: ")"})
			i++
		case c == '\'' || c == '"':
			j := i + 1
			for j < len(s) && s[j] != c {
				j++
			}
			if j >= len(s) {
				return nil, fmt.Errorf("unterminated string in marker %q", s)
			}
			tokens = append(tokens, markerToken{kind: markerString, text: s[i+1 : j]})
			i = j + 1
		case c == '=' || c == '<' || c == '>' || c == '!' || c == '~':
			op := string(c)
			i++
			if i < len(s) && s[i] == '=' {
				op = op + "="
				i++
			}
			switch op {
			case "=":
				op = "=="
			case "===", "!=", "<=", ">=", "~=", "==", "<", ">":
			default:
				return nil, fmt.Errorf("unsupported operator %q in marker %q", op, s)
			}
			tokens = append(tokens, markerToken{kind: markerOp, text: op})
		case isIdentStart(c):
			j := i
			for j < len(s) && isIdentChar(s[j]) {
				j++
			}
			tokens = append(tokens, markerToken{kind: markerIdent, text: s[i:j]})
			i = j
		default:
			return nil, fmt.Errorf("unexpected character %q in marker %q", c, s)
		}
	}
	tokens = append(tokens, markerToken{kind: markerEnd})
	return tokens, nil
}

// isIdentStart reports whether c may start a marker identifier.
func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// isIdentChar reports whether c may appear inside a marker identifier.
func isIdentChar(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9') || c == '.'
}

// markerParser is a recursive-descent parser over marker tokens.
type markerParser struct {
	tokens []markerToken
	pos    int
	env    MarkerEnv
}

// peek returns the current token without consuming it.
func (p *markerParser) peek() markerToken {
	if p.pos >= len(p.tokens) {
		return markerToken{kind: markerEnd}
	}
	return p.tokens[p.pos]
}

// next consumes and returns the current token.
func (p *markerParser) next() markerToken {
	t := p.peek()
	if p.pos < len(p.tokens) {
		p.pos++
	}
	return t
}

// EvaluateMarker evaluates a PEP 508 environment marker expression.
//
// An empty marker evaluates to true. A malformed marker returns an error so
// that callers can decide explicitly how to treat it.
func EvaluateMarker(marker string, env MarkerEnv) (bool, error) {
	trimmed := strings.TrimSpace(marker)
	if trimmed == "" {
		return true, nil
	}
	tokens, err := tokenizeMarker(trimmed)
	if err != nil {
		return false, err
	}
	p := &markerParser{tokens: tokens, env: env}
	result, err := p.parseOr()
	if err != nil {
		return false, err
	}
	if p.peek().kind != markerEnd {
		return false, fmt.Errorf("unexpected token %q in marker %q", p.peek().text, marker)
	}
	return result, nil
}

// parseOr parses an "or" expression.
func (p *markerParser) parseOr() (bool, error) {
	left, err := p.parseAnd()
	if err != nil {
		return false, err
	}
	for {
		t := p.peek()
		if t.kind != markerIdent || !strings.EqualFold(t.text, "or") {
			return left, nil
		}
		p.next()
		right, err := p.parseAnd()
		if err != nil {
			return false, err
		}
		left = left || right
	}
}

// parseAnd parses an "and" expression.
func (p *markerParser) parseAnd() (bool, error) {
	left, err := p.parseUnary()
	if err != nil {
		return false, err
	}
	for {
		t := p.peek()
		if t.kind != markerIdent || !strings.EqualFold(t.text, "and") {
			return left, nil
		}
		p.next()
		right, err := p.parseUnary()
		if err != nil {
			return false, err
		}
		left = left && right
	}
}

// parseUnary parses a "not" expression or a primary expression.
func (p *markerParser) parseUnary() (bool, error) {
	t := p.peek()
	if t.kind == markerIdent && strings.EqualFold(t.text, "not") {
		p.next()
		v, err := p.parseUnary()
		if err != nil {
			return false, err
		}
		return !v, nil
	}
	return p.parsePrimary()
}

// parsePrimary parses a parenthesized expression or a comparison.
func (p *markerParser) parsePrimary() (bool, error) {
	t := p.peek()
	if t.kind == markerLParen {
		p.next()
		v, err := p.parseOr()
		if err != nil {
			return false, err
		}
		if p.peek().kind != markerRParen {
			return false, fmt.Errorf("missing closing parenthesis in marker")
		}
		p.next()
		return v, nil
	}
	return p.parseComparison()
}

// parseComparison parses "operand [op operand]".
func (p *markerParser) parseComparison() (bool, error) {
	left, leftIsVersion, err := p.parseOperand()
	if err != nil {
		return false, err
	}
	t := p.peek()
	if t.kind != markerOp {
		// A bare identifier is truthy when it resolves to a non-empty value.
		return left != "", nil
	}
	op := t.text
	p.next()
	right, rightIsVersion, err := p.parseOperand()
	if err != nil {
		return false, err
	}
	if leftIsVersion || rightIsVersion {
		return compareMarkerVersions(left, right, op)
	}
	switch op {
	case "==":
		return left == right, nil
	case "!=":
		return left != right, nil
	case "<":
		return left < right, nil
	case "<=":
		return left <= right, nil
	case ">":
		return left > right, nil
	case ">=":
		return left >= right, nil
	case "~=":
		c := CompareVersions(left, right)
		return c == 0, nil
	default:
		return false, fmt.Errorf("unsupported operator %q in marker", op)
	}
}

// parseOperand parses an identifier or string operand.
func (p *markerParser) parseOperand() (string, bool, error) {
	t := p.next()
	switch t.kind {
	case markerString:
		return t.text, false, nil
	case markerIdent:
		value, isVersion := p.lookup(t.text)
		return value, isVersion, nil
	default:
		return "", false, fmt.Errorf("unexpected token %q in marker", t.text)
	}
}

// lookup resolves a marker variable against the environment.
func (p *markerParser) lookup(name string) (string, bool) {
	switch strings.ToLower(name) {
	case "python_version":
		return p.env.PythonVersion, true
	case "python_full_version":
		return p.env.PythonFullVersion, true
	case "implementation_version":
		return p.env.ImplementationVersion, true
	case "implementation_name":
		return p.env.Implementation, false
	case "os_name":
		return p.env.OSName, false
	case "sys_platform":
		return p.env.SysPlatform, false
	case "platform_machine":
		return p.env.PlatformMachine, false
	case "platform_system":
		return p.env.PlatformSystem, false
	case "platform_release":
		return p.env.PlatformRelease, false
	case "platform_version":
		return p.env.PlatformVersion, false
	case "extra":
		return p.env.Extra, false
	default:
		return "", false
	}
}

// compareMarkerVersions compares two version-valued marker operands.
func compareMarkerVersions(left, right, op string) (bool, error) {
	c := CompareVersions(left, right)
	switch op {
	case "==":
		return c == 0, nil
	case "!=":
		return c != 0, nil
	case "<":
		return c < 0, nil
	case "<=":
		return c <= 0, nil
	case ">":
		return c > 0, nil
	case ">=":
		return c >= 0, nil
	case "~=":
		return Matches(left, "~="+right), nil
	default:
		return false, fmt.Errorf("unsupported operator %q in marker", op)
	}
}
