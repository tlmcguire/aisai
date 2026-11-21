package pypi

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// PEP 440 versions
// ---------------------------------------------------------------------------

// preKind orders the pre-release kinds: alpha < beta < release candidate.
const (
	preNone = iota
	preAlpha
	preBeta
	preRC
)

// parsedVersion is a PEP 440 version broken into comparable parts.
type parsedVersion struct {
	epoch   int
	release []int
	preKind int
	preNum  int
	hasPost bool
	post    int
	hasDev  bool
	dev     int
	local   []string
	raw     string
}

// versionRe splits a version into epoch, release and the remainder that
// holds the pre/post/dev suffixes.
var versionRe = regexp.MustCompile(`^(?:(\d+)!)?(\d+(?:\.\d+)*)(.*)$`)

// preRe matches a pre-release suffix at the start of a version remainder.
var preRe = regexp.MustCompile(`^[._-]?(alpha|beta|preview|pre|rc|c|a|b)[._-]?(\d*)`)

// postRe matches an explicit post-release suffix at the start of a remainder.
var postRe = regexp.MustCompile(`^[._-]?(post|rev|r)[._-]?(\d*)`)

// devRe matches a development-release suffix at the start of a remainder.
var devRe = regexp.MustCompile(`^[._-]?dev[._-]?(\d*)`)

// implicitPostRe matches the implicit post-release form "1.0-1".
var implicitPostRe = regexp.MustCompile(`^-(\d+)$`)

// parseVersion parses a PEP 440 version string into comparable parts.
func parseVersion(s string) (parsedVersion, bool) {
	var v parsedVersion
	raw := strings.TrimSpace(s)
	if raw == "" {
		return v, false
	}
	v.raw = raw
	rest := strings.ToLower(raw)

	// A leading "v" is allowed ("v1.2.3").
	if rest[0] == 'v' && len(rest) > 1 && rest[1] >= '0' && rest[1] <= '9' {
		rest = rest[1:]
	}

	// Local version segment comes last and is separated by "+".
	if i := strings.Index(rest, "+"); i >= 0 {
		v.local = splitLocalVersion(rest[i+1:])
		rest = rest[:i]
	}

	m := versionRe.FindStringSubmatch(rest)
	if m == nil {
		return v, false
	}
	if m[1] != "" {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return v, false
		}
		v.epoch = n
	}
	for _, part := range strings.Split(m[2], ".") {
		n, err := strconv.Atoi(part)
		if err != nil {
			return v, false
		}
		v.release = append(v.release, n)
	}
	rest = m[3]

	// Suffixes appear in the canonical order pre, post, dev.
	if pm := preRe.FindStringSubmatch(rest); pm != nil {
		v.preKind = preKindFor(pm[1])
		v.preNum, _ = strconv.Atoi(pm[2])
		rest = rest[len(pm[0]):]
	}
	if pm := postRe.FindStringSubmatch(rest); pm != nil {
		v.hasPost = true
		v.post, _ = strconv.Atoi(pm[2])
		rest = rest[len(pm[0]):]
	} else if pm := implicitPostRe.FindStringSubmatch(rest); pm != nil {
		v.hasPost = true
		v.post, _ = strconv.Atoi(pm[1])
		rest = ""
	}
	if dm := devRe.FindStringSubmatch(rest); dm != nil {
		v.hasDev = true
		v.dev, _ = strconv.Atoi(dm[1])
		rest = rest[len(dm[0]):]
	}
	if rest != "" {
		return v, false
	}
	return v, true
}

// preKindFor maps a pre-release keyword onto its ordering class.
func preKindFor(keyword string) int {
	switch keyword {
	case "a", "alpha":
		return preAlpha
	case "b", "beta":
		return preBeta
	case "c", "rc", "pre", "preview":
		return preRC
	default:
		return preNone
	}
}

// splitLocalVersion splits a local version segment into comparable parts.
func splitLocalVersion(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == '.' || r == '-' || r == '_'
	})
	return parts
}

// CompareVersions compares two PEP 440 version strings.
//
// It returns -1 when a sorts before b, +1 when a sorts after b and 0 when
// they are equivalent. Unparseable versions sort before parseable ones.
func CompareVersions(a, b string) int {
	va, aOK := parseVersion(a)
	vb, bOK := parseVersion(b)
	switch {
	case !aOK && !bOK:
		return strings.Compare(a, b)
	case !aOK:
		return -1
	case !bOK:
		return 1
	}
	if c := compareInt(va.epoch, vb.epoch); c != 0 {
		return c
	}
	if c := compareRelease(va.release, vb.release); c != 0 {
		return c
	}
	// A pre-release sorts before the corresponding final release.
	switch {
	case va.preKind == preNone && vb.preKind == preNone:
	case va.preKind == preNone:
		return 1
	case vb.preKind == preNone:
		return -1
	default:
		if c := compareInt(va.preKind, vb.preKind); c != 0 {
			return c
		}
		if c := compareInt(va.preNum, vb.preNum); c != 0 {
			return c
		}
	}
	// An absent post-release sorts before any post-release.
	vaPost, vbPost := -1, -1
	if va.hasPost {
		vaPost = va.post
	}
	if vb.hasPost {
		vbPost = vb.post
	}
	if c := compareInt(vaPost, vbPost); c != 0 {
		return c
	}
	// A final release sorts after any development release of the same version.
	switch {
	case va.hasDev == vb.hasDev:
		if c := compareInt(va.dev, vb.dev); c != 0 {
			return c
		}
	case va.hasDev:
		return -1
	default:
		return 1
	}
	// A local version sorts after the same version without one.
	switch {
	case len(va.local) == 0 && len(vb.local) == 0:
		return 0
	case len(va.local) == 0:
		return -1
	case len(vb.local) == 0:
		return 1
	}
	return compareLocal(va.local, vb.local)
}

// compareInt compares two integers.
func compareInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// compareRelease compares release segments, padding the shorter one with
// zeros as PEP 440 requires.
func compareRelease(a, b []int) int {
	n := len(a)
	if len(b) > n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		x, y := 0, 0
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if c := compareInt(x, y); c != 0 {
			return c
		}
	}
	return 0
}

// compareLocal compares local version segments, numerically when both parts
// are numeric and lexicographically otherwise.
func compareLocal(a, b []string) int {
	n := len(a)
	if len(b) > n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if i >= len(a) {
			return -1
		}
		if i >= len(b) {
			return 1
		}
		x, xErr := strconv.Atoi(a[i])
		y, yErr := strconv.Atoi(b[i])
		if xErr == nil && yErr == nil {
			if c := compareInt(x, y); c != 0 {
				return c
			}
			continue
		}
		if c := strings.Compare(a[i], b[i]); c != 0 {
			return c
		}
	}
	return 0
}

// IsPrerelease reports whether a version is a pre-release or development
// release.
func IsPrerelease(version string) bool {
	v, ok := parseVersion(version)
	if !ok {
		return false
	}
	return v.preKind != preNone || v.hasDev
}

// LatestVersion returns the highest version in versions that satisfies the
// given PEP 440 specifier set.
//
// The empty specifier matches every version. Pre-releases are only returned
// when no stable version satisfies the specifier. It returns an empty string
// when nothing matches.
func LatestVersion(versions []string, specifier string) string {
	var stable, pre []string
	for _, v := range versions {
		if !Matches(v, specifier) {
			continue
		}
		if IsPrerelease(v) {
			pre = append(pre, v)
		} else {
			stable = append(stable, v)
		}
	}
	pick := func(list []string) string {
		best := ""
		for _, v := range list {
			if best == "" || CompareVersions(v, best) > 0 {
				best = v
			}
		}
		return best
	}
	if s := pick(stable); s != "" {
		return s
	}
	return pick(pre)
}

// Matches reports whether a version satisfies a PEP 440 specifier set.
//
// The specifier is a comma-separated list of clauses such as ">=1.0,<2.0".
// An empty specifier matches every version. Unknown clauses are ignored
// rather than treated as a match, so a malformed specifier can never widen
// a resolution.
func Matches(version, specifier string) bool {
	clauses := splitClauses(specifier)
	if len(clauses) == 0 {
		return true
	}
	for _, clause := range clauses {
		op, want := splitClause(clause)
		if op == "" {
			continue
		}
		if !matchClause(version, op, want) {
			return false
		}
	}
	return true
}

// splitClauses splits a specifier set into its comma-separated clauses.
func splitClauses(specifier string) []string {
	specifier = strings.TrimSpace(specifier)
	specifier = strings.Trim(specifier, "()")
	if specifier == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(specifier, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// splitClause splits one clause into its operator and version.
func splitClause(clause string) (string, string) {
	for _, op := range []string{"===", "==", "!=", "<=", ">=", "~=", "<", ">"} {
		if strings.HasPrefix(clause, op) {
			return op, strings.TrimSpace(strings.TrimPrefix(clause, op))
		}
	}
	return "", ""
}

// matchClause evaluates a single specifier clause against a version.
func matchClause(version, op, want string) bool {
	switch op {
	case "===":
		return strings.TrimSpace(version) == want
	case "==":
		if strings.HasSuffix(want, ".*") {
			prefix := strings.TrimSuffix(want, ".*")
			return strings.HasPrefix(strings.TrimSpace(version), prefix)
		}
		return CompareVersions(version, want) == 0
	case "!=":
		if strings.HasSuffix(want, ".*") {
			prefix := strings.TrimSuffix(want, ".*")
			return !strings.HasPrefix(strings.TrimSpace(version), prefix)
		}
		return CompareVersions(version, want) != 0
	case "<", "<=", ">", ">=":
		c := CompareVersions(version, want)
		switch op {
		case "<":
			return c < 0
		case "<=":
			return c <= 0
		case ">":
			return c > 0
		default:
			return c >= 0
		}
	case "~=":
		return matchCompatibleRelease(version, want)
	default:
		return false
	}
}

// matchCompatibleRelease implements the "~=" (compatible release) operator.
func matchCompatibleRelease(version, want string) bool {
	wv, ok := parseVersion(want)
	if !ok {
		return false
	}
	if CompareVersions(version, want) < 0 {
		return false
	}
	if len(wv.release) < 2 {
		return CompareVersions(version, want) == 0
	}
	upper := make([]int, len(wv.release)-1)
	copy(upper, wv.release[:len(upper)])
	upper[len(upper)-1]++
	return compareRelease(parseVersionRelease(version), upper) < 0
}

// parseVersionRelease returns the release segments of a version, or nil when
// the version cannot be parsed.
func parseVersionRelease(version string) []int {
	v, ok := parseVersion(version)
	if !ok {
		return nil
	}
	return v.release
}

// ---------------------------------------------------------------------------
// PEP 508 requirements
// ---------------------------------------------------------------------------

// Requirement is a parsed PEP 508 dependency specification.
type Requirement struct {
	// Name is the project name, exactly as written in the specification.
	Name string
	// Extras lists the requested extras. Extras are never resolved.
	Extras []string
	// Specifier is the version specifier set, e.g. ">=1.0,<2.0".
	Specifier string
	// Marker is the environment marker expression, without the leading ";".
	Marker string
}

// String renders the requirement back into PEP 508 form.
func (r Requirement) String() string {
	s := r.Name
	if len(r.Extras) > 0 {
		s += "[" + strings.Join(r.Extras, ",") + "]"
	}
	if r.Specifier != "" {
		s += " (" + r.Specifier + ")"
	}
	if r.Marker != "" {
		s += "; " + r.Marker
	}
	return s
}

// ParseRequirement parses a PEP 508 requirement specification.
//
// It understands "name", "name (>=1.0)", "name[extra]>=1.0" and environment
// markers separated by ";".
func ParseRequirement(s string) (Requirement, error) {
	var req Requirement
	spec := strings.TrimSpace(s)
	if spec == "" {
		return req, fmt.Errorf("pypi: empty requirement")
	}
	if i := strings.Index(spec, ";"); i >= 0 {
		req.Marker = strings.TrimSpace(spec[i+1:])
		spec = strings.TrimSpace(spec[:i])
	}
	i := 0
	for i < len(spec) && isNameChar(spec[i]) {
		i++
	}
	req.Name = strings.TrimSpace(spec[:i])
	spec = spec[i:]
	if req.Name == "" {
		return req, fmt.Errorf("pypi: requirement %q has no name", s)
	}
	if strings.HasPrefix(spec, "[") {
		j := strings.Index(spec, "]")
		if j < 0 {
			return req, fmt.Errorf("pypi: requirement %q has an unterminated extra", s)
		}
		for _, extra := range strings.Split(spec[1:j], ",") {
			extra = strings.TrimSpace(extra)
			if extra != "" {
				req.Extras = append(req.Extras, extra)
			}
		}
		spec = spec[j+1:]
	}
	spec = strings.TrimSpace(spec)
	switch {
	case strings.HasPrefix(spec, "("):
		j := strings.LastIndex(spec, ")")
		if j < 0 {
			return req, fmt.Errorf("pypi: requirement %q has an unterminated specifier", s)
		}
		req.Specifier = strings.TrimSpace(spec[1:j])
		spec = strings.TrimSpace(spec[j+1:])
	case spec != "":
		req.Specifier = strings.TrimSpace(spec)
		spec = ""
	}
	if spec != "" {
		return req, fmt.Errorf("pypi: requirement %q has trailing garbage %q", s, spec)
	}
	return req, nil
}

// isNameChar reports whether c may appear in a Python project name.
func isNameChar(c byte) bool {
	return c == '-' || c == '_' || c == '.' ||
		(c >= '0' && c <= '9') ||
		(c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z')
}
