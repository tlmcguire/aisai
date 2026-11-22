package sast

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/tlmcguire/aisai/internal/ast"
	"github.com/tlmcguire/aisai/internal/model"
)

// This file is the taint analysis for the languages other than Python
// (Python has its own, in scan.go and checks.go).
//
// Taint analysis asks one question: can a value an attacker controls (a
// request parameter, a command-line argument, the bytes of a file being
// parsed) reach a dangerous call (a shell, a SQL statement, a file path, a
// raw memory operation) without being checked on the way? A pattern rule
// finds the dangerous call; taint analysis finds the dangerous call that is
// actually fed from outside, which is the difference between "this code runs
// a shell" and "this code runs a shell with the user's text".
// Background: https://owasp.org/www-community/vulnerabilities/ and the survey
// by Schwartz, Avgerinos, Brumley, "All You Ever Wanted to Know About Dynamic
// Taint Analysis and Forward Symbolic Execution".
//
// The analysis is deliberately modest, so that its answers can be explained:
//
//   - It follows values inside one function, in source order, through
//     declarations, assignments, string building and method chains. A value
//     is tainted when it is derived from a source, and clean again when it
//     is overwritten by something clean outside any branch.
//   - A call to a function defined in the same file is followed one level:
//     a parameter that reaches a sink inside makes the call site a finding,
//     and a function that returns a tainted value taints its caller's result.
//   - A sanitizer (an escape function, a basename, a numeric parse) stops
//     the taint. A numeric parse only stops it for sinks that take text: a
//     number read from a file is exactly what an allocation or a raw slice
//     must not trust.
//   - A check on the value (starts_with, a whitelist lookup) does not stop
//     the taint, because the analysis does not follow branches, but it lowers
//     the finding's confidence and says so.
//
// Every finding carries the path in its evidence: where the value came from,
// each step it took, and the sink.

// taintSource matches an expression that produces attacker-controlled data.
type taintSource struct {
	re    *regexp.Regexp
	label string
	// local marks data that whoever runs the program supplies (arguments,
	// environment, standard input). There is no privilege boundary between
	// that person and the program, so findings that start there are rated lower.
	local bool
}

// outSource is a call that writes attacker data into an argument.
type outSource struct {
	callee *regexp.Regexp
	arg    int
	label  string
	local  bool
}

// taintSink describes a dangerous call.
type taintSink struct {
	rule string
	// callee matches the normalized callee ("Command.new.arg", "fs.readFile").
	callee *regexp.Regexp
	// args are the argument positions that matter; nil means any.
	args []int
	// label names the sink in the finding.
	label string
	// numeric sinks accept a numeric taint as well (sizes and indexes).
	numeric bool
	// when adds a condition on the whole call, for example that a command
	// chain starts a shell.
	when func(c callSite) bool
}

// callSite is a call expression reduced to what a sink condition needs.
type callSite struct {
	callee string
	text   string
}

// assignSink matches an assignment to a dangerous property (innerHTML).
type assignSink struct {
	rule  string
	left  *regexp.Regexp
	label string
}

// bindingInfo is what a declaration or assignment binds.
type bindingInfo struct {
	// targets are the names (or dotted paths) being assigned.
	targets []string
	// values are the right-hand expressions; one value for several targets
	// means the targets share it (a call returning a tuple).
	values []ast.Node
	// augmented is true for "+=" and the like: the target keeps its taint
	// and gains the value's.
	augmented bool
}

// objectSource is a name that holds attacker-controlled data as a whole (a
// request object), except for the members that are not data.
type objectSource struct {
	label string
	safe  *regexp.Regexp
	local bool
}

// taintLang is everything the engine needs to know about one language.
type taintLang struct {
	// scopes start a new function scope; closures share their parent's.
	scopes      map[string]bool
	closures    map[string]bool
	conditional map[string]bool
	// bind extracts the binding a node makes, if it makes one.
	bind func(n ast.Node) (bindingInfo, bool)
	// params returns the parameters of a function node that hold attacker
	// data, with their names.
	params func(fn ast.Node) map[string]objectSource
	// paramNames returns every parameter name in order, for summaries.
	paramNames func(fn ast.Node) []string
	// funcName returns the declared name of a function node.
	funcName func(fn ast.Node) string
	// body returns the node holding the function's statements.
	body func(fn ast.Node) (ast.Node, bool)
	// tail returns the expression a function body evaluates to without a
	// return statement (Rust's last expression, an arrow function's body).
	tail func(body ast.Node) (ast.Node, bool)
	// exported reports whether a function is part of the file's public API.
	exported func(fn ast.Node) bool
	// alias names a function assigned to a variable (a JavaScript arrow
	// function), so that calls to it can be followed.
	alias func(n ast.Node) (name string, fn ast.Node, ok bool)
	// outSources are calls that put attacker data into one of their
	// arguments (read_line(&mut buf)).
	outSources []outSource

	sources    []taintSource
	sanitizers *regexp.Regexp
	numeric    *regexp.Regexp
	pure       *regexp.Regexp
	guards     *regexp.Regexp
	sinks      []taintSink
	assigns    []assignSink
	// exprKinds are the node kinds a source pattern is matched against.
	exprKinds map[string]bool
	// callKinds are the node kinds that are calls.
	callKinds map[string]bool
	// returnKinds are the node kinds of a return statement.
	returnKinds map[string]bool
}

// trace is how a value became tainted.
type trace struct {
	source  string
	line    int
	steps   []string
	numeric bool
	// guarded is set when a check on the value exists in the function.
	guarded bool
	// local is set when the data comes from whoever runs the program.
	local bool
}

func (t *trace) with(step string) *trace {
	c := *t
	c.steps = append(append([]string(nil), t.steps...), step)
	if len(c.steps) > 6 {
		c.steps = c.steps[len(c.steps)-6:]
	}
	return &c
}

// taintEntry is a tainted name and where in the nesting it became tainted.
type taintEntry struct {
	t     *trace
	depth int
}

// funcSummary is what the analysis learned about a function of the file.
type funcSummary struct {
	node ast.Node
	// sinkParam maps a parameter position to the first sink it reaches.
	sinkParam map[int]sinkReach
	// returnsSource: the function returns attacker data by itself.
	returnsSource bool
	// returnsParam: parameter positions whose value flows to the result.
	returnsParam map[int]bool
}

// sinkReach is a sink reached from a parameter.
type sinkReach struct {
	rule    string
	label   string
	line    int
	numeric bool
}

// taintFinding is a finding before it becomes a model.Finding.
type taintFinding struct {
	rule    string
	line    int
	endLine int
	code    string
	message string
	sink    string
	t       *trace
}

// analyzer holds the state of one file's analysis.
type analyzer struct {
	f         *ast.File
	lang      *taintLang
	summaries map[string]*funcSummary
	ambiguous map[string]bool
	out       []taintFinding
	// recording, when non-nil, collects the sinks a function's parameters
	// reach instead of reporting findings (the summary pass).
	recording map[int]sinkReach
	// paramTaint maps a name to its parameter position in a summary pass.
	paramIndex map[string]int
	// returned collects what a summary pass learns about return values.
	returnedSource bool
	returnedParams map[int]bool
}

// scanTaint runs the taint analysis over a file in a supported language.
func scanTaint(f *ast.File) []model.Finding {
	def := f.LanguageDef()
	if def == nil {
		return nil
	}
	lang := taintLangFor(def.ID)
	if lang == nil {
		return nil
	}
	a := &analyzer{f: f, lang: lang, summaries: map[string]*funcSummary{}, ambiguous: map[string]bool{}}
	root := f.Root()
	a.collectFunctions(root)
	for round := 0; round < 2; round++ {
		a.summarize()
	}
	a.out = nil
	a.walkScope(root, a.newScopeState(nil), 0)
	a.reportAPIParams()

	sort.SliceStable(a.out, func(i, j int) bool {
		if a.out[i].line != a.out[j].line {
			return a.out[i].line < a.out[j].line
		}
		return a.out[i].rule < a.out[j].rule
	})
	sc := &scanner{file: f}
	seen := map[string]bool{}
	var out []model.Finding
	for _, h := range a.out {
		r, ok := patternRuleByID(h.rule)
		if !ok {
			continue
		}
		key := fmt.Sprintf("%s:%d", h.rule, h.line)
		if seen[key] {
			continue
		}
		seen[key] = true
		node := ast.Node{StartLine: h.line, EndLine: h.endLine, Text: h.code}
		flow := append([]string{fmt.Sprintf("line %d: %s", h.t.line, h.t.source)}, h.t.steps...)
		flow = append(flow, fmt.Sprintf("line %d: %s", h.line, h.sink))
		note := "The value is followed from its source to this call within the file."
		fnd := newFinding(r, sc, node, h.message, model.Evidence{Source: h.t.source, Sink: h.sink, Flow: flow, Note: note})
		if h.t.guarded {
			fnd.Confidence = r.Confidence * 0.6
			fnd.Evidence.Note = "A check on this value exists in the function. The analysis does not follow branches, so verify the check is sufficient."
		}
		if h.t.local {
			// The person running the program chooses these values, so there
			// is usually no attacker: rate it lower, and say why.
			fnd.Confidence *= 0.7
			switch h.rule {
			case RuleTaintPath, RuleTaintSSRF, RuleTaintRedirect, RuleTaintXSS, RuleTaintAlloc:
				fnd.Severity = model.SeverityLow
			}
			fnd.Evidence.Note = "This value is supplied by whoever runs the program (an argument, the environment or standard input), so it matters only where that person is not trusted: a setuid binary, a CI job, a service started with someone else's input."
		}
		out = append(out, fnd)
		if len(out) >= maxPatternFindingsPerFile {
			break
		}
	}
	return out
}

// reportAPIParams reports a public function whose parameter reaches a raw
// memory operation unchecked. A safe public function is a promise that no
// argument can cause undefined behaviour; a size or index passed straight to
// from_raw_parts or get_unchecked breaks it.
func (a *analyzer) reportAPIParams() {
	if a.lang.exported == nil {
		return
	}
	names := make([]string, 0, len(a.summaries))
	for name := range a.summaries {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		s := a.summaries[name]
		if a.ambiguous[name] || !a.lang.exported(s.node) {
			continue
		}
		pnames := a.lang.paramNames(s.node)
		for i, reach := range s.sinkParam {
			if reach.rule != RuleTaintMemory || i >= len(pnames) {
				continue
			}
			t := &trace{
				source:  fmt.Sprintf("parameter `%s` of the public function `%s`", pnames[i], name),
				line:    s.node.StartLine,
				guarded: true,
			}
			a.out = append(a.out, taintFinding{
				rule: reach.rule, line: reach.line, endLine: reach.line,
				code:    "",
				message: fmt.Sprintf("%s reaches %s. If a caller passes a bad value this is undefined behaviour, which a safe public function must prevent.", t.source, reach.label),
				sink:    reach.label,
				t:       t,
			})
		}
	}
}

// scopeState is the taint state of one scope.
type scopeState struct {
	taint   map[string]taintEntry
	objects map[string]objectSource
	guarded map[string]bool
}

func (a *analyzer) newScopeState(parent *scopeState) *scopeState {
	s := &scopeState{taint: map[string]taintEntry{}, objects: map[string]objectSource{}, guarded: map[string]bool{}}
	if parent != nil {
		for k, v := range parent.taint {
			s.taint[k] = v
		}
		for k, v := range parent.objects {
			s.objects[k] = v
		}
	}
	return s
}

// collectFunctions records every named function of the file for the summaries.
func (a *analyzer) collectFunctions(n ast.Node) {
	register := func(name string, fn ast.Node) {
		if name == "" {
			return
		}
		if _, dup := a.summaries[name]; dup {
			a.ambiguous[name] = true
			return
		}
		a.summaries[name] = &funcSummary{node: fn, sinkParam: map[int]sinkReach{}, returnsParam: map[int]bool{}}
	}
	if a.lang.scopes[n.Kind] {
		register(a.lang.funcName(n), n)
	}
	if a.lang.alias != nil {
		if name, fn, ok := a.lang.alias(n); ok {
			register(name, fn)
		}
	}
	for _, c := range n.Children {
		a.collectFunctions(c)
	}
}

// summarize runs the analysis once per function with each parameter tainted in
// turn, to learn which parameters reach a sink and which flow to the result.
func (a *analyzer) summarize() {
	for name, s := range a.summaries {
		if a.ambiguous[name] {
			continue
		}
		names := a.lang.paramNames(s.node)
		// Run once with nothing tainted to see whether it returns a source.
		a.runSummary(s, -1, "")
		for i, p := range names {
			if p == "" {
				continue
			}
			a.runSummary(s, i, p)
		}
	}
}

func (a *analyzer) runSummary(s *funcSummary, index int, param string) {
	body, ok := a.lang.body(s.node)
	if !ok {
		return
	}
	prevOut, prevRec, prevIdx := a.out, a.recording, a.paramIndex
	prevSrc, prevRet := a.returnedSource, a.returnedParams
	defer func() {
		a.out, a.recording, a.paramIndex = prevOut, prevRec, prevIdx
		a.returnedSource, a.returnedParams = prevSrc, prevRet
	}()
	a.out = nil
	a.recording = map[int]sinkReach{}
	a.paramIndex = map[string]int{}
	a.returnedSource = false
	a.returnedParams = map[int]bool{}
	st := a.newScopeState(nil)
	for name, obj := range a.lang.params(s.node) {
		st.objects[name] = obj
	}
	if index >= 0 {
		st.taint[param] = taintEntry{t: &trace{source: "parameter `" + param + "`", line: s.node.StartLine, numeric: false}}
		a.paramIndex[param] = index
	}
	a.walkBody(body, st, 0)
	if a.lang.tail != nil {
		if expr, ok := a.lang.tail(body); ok {
			a.noteReturnExpr(expr, st)
		}
	}
	if index < 0 {
		s.returnsSource = a.returnedSource
		return
	}
	if r, ok := a.recording[index]; ok {
		if _, have := s.sinkParam[index]; !have {
			s.sinkParam[index] = r
		}
	}
	if a.returnedParams[index] {
		s.returnsParam[index] = true
	}
}

// walkScope walks a node that may contain function scopes.
func (a *analyzer) walkScope(n ast.Node, st *scopeState, depth int) {
	a.walkBody(n, st, depth)
}

// analyzeFunction analyzes one function with a fresh scope that inherits the
// module's tainted globals.
func (a *analyzer) analyzeFunction(fn ast.Node, parent *scopeState) {
	body, ok := a.lang.body(fn)
	if !ok {
		return
	}
	st := a.newScopeState(parent)
	for name, obj := range a.lang.params(fn) {
		st.objects[name] = obj
	}
	a.collectGuards(body, st)
	a.walkBody(body, st, 0)
}

// walkBody walks statements in source order, updating the taint state and
// reporting sinks.
func (a *analyzer) walkBody(n ast.Node, st *scopeState, depth int) {
	if a.lang.scopes[n.Kind] {
		// A nested function is its own scope, analyzed on its own. A closure
		// (a.lang.closures) is not listed in scopes: it shares its parent's state.
		a.analyzeFunction(n, st)
		return
	}
	cond := depth
	if a.lang.conditional[n.Kind] {
		cond++
	}
	if info, ok := a.lang.bind(n); ok {
		a.applyBinding(n, info, st, cond)
	}
	if a.lang.callKinds[n.Kind] {
		a.checkOutSource(n, st)
		a.checkCall(n, st)
	}
	a.checkAssignSink(n, st)
	if a.lang.returnKinds[n.Kind] && a.recording != nil {
		a.noteReturn(n, st)
	}
	for _, c := range n.Children {
		a.walkBody(c, st, cond)
	}
}

// noteReturn records what a function returns, for its summary.
func (a *analyzer) noteReturn(n ast.Node, st *scopeState) {
	for _, c := range n.Children {
		a.noteReturnExpr(c, st)
	}
}

func (a *analyzer) noteReturnExpr(n ast.Node, st *scopeState) {
	if t := a.exprTaint(n, st); t != nil {
		if strings.HasPrefix(t.source, "parameter `") {
			name := strings.TrimSuffix(strings.TrimPrefix(t.source, "parameter `"), "`")
			if i, ok := a.paramIndex[name]; ok {
				a.returnedParams[i] = true
				return
			}
		}
		a.returnedSource = true
	}
}

// applyBinding updates the taint state for a declaration or assignment.
func (a *analyzer) applyBinding(n ast.Node, info bindingInfo, st *scopeState, depth int) {
	for i, target := range info.targets {
		if target == "" {
			continue
		}
		var value ast.Node
		switch {
		case len(info.values) == 0:
			continue
		case len(info.values) == 1:
			value = info.values[0]
		case i < len(info.values):
			value = info.values[i]
		default:
			continue
		}
		t := a.exprTaint(value, st)
		if t != nil {
			step := fmt.Sprintf("line %d: %s = %s", n.StartLine, target, short(value.Text, 60))
			st.taint[target] = taintEntry{t: t.with(step), depth: depth}
			if t.guarded || st.guarded[target] {
				c := st.taint[target]
				tc := *c.t
				tc.guarded = true
				c.t = &tc
				st.taint[target] = c
			}
			continue
		}
		if info.augmented {
			continue
		}
		// An unconditional overwrite with something clean ends the taint.
		if e, ok := st.taint[target]; ok && depth <= e.depth {
			delete(st.taint, target)
		}
	}
}

// checkCall reports a tainted argument reaching a sink, and records sinks
// reached by parameters during a summary pass.
func (a *analyzer) checkCall(n ast.Node, st *scopeState) {
	callee, _ := pgSplitCall(n.Text)
	argsNode, ok := n.Child("arguments")
	if !ok {
		return
	}
	args := argsNode.Children
	site := callSite{callee: callee, text: n.Text}

	for _, sink := range a.lang.sinks {
		if !sink.callee.MatchString(callee) {
			continue
		}
		if sink.when != nil && !sink.when(site) {
			continue
		}
		for i, arg := range args {
			if sink.args != nil && !containsInt(sink.args, i) {
				continue
			}
			t := a.exprTaint(arg, st)
			if t == nil || (t.numeric && !sink.numeric) {
				continue
			}
			t = a.markGuarded(t, arg, st)
			a.report(n, sink.rule, sink.label, t, callee)
			a.recordSinkReach(t, sink.rule, sink.label, n.StartLine, sink.numeric)
			break
		}
	}

	// A call to a function of this file: follow it one level.
	name := lastSegment(callee)
	if sum, ok := a.summaries[name]; ok && !a.ambiguous[name] && a.recording == nil {
		for i, arg := range args {
			reach, ok := sum.sinkParam[i]
			if !ok {
				continue
			}
			t := a.exprTaint(arg, st)
			if t == nil || (t.numeric && !reach.numeric) {
				continue
			}
			label := fmt.Sprintf("%s (inside `%s`, line %d)", reach.label, name, reach.line)
			a.report(n, reach.rule, label, t, callee)
			break
		}
	} else if ok && a.recording != nil && !a.ambiguous[name] {
		// During a summary pass a call to another function propagates what
		// is known so far.
		for i, arg := range args {
			reach, has := sum.sinkParam[i]
			if !has {
				continue
			}
			if t := a.exprTaint(arg, st); t != nil && !(t.numeric && !reach.numeric) {
				a.recordSinkReach(t, reach.rule, reach.label, reach.line, reach.numeric)
			}
		}
	}
}

// recordSinkReach notes, during a summary pass, that a parameter reached a sink.
func (a *analyzer) recordSinkReach(t *trace, rule, label string, line int, numeric bool) {
	if a.recording == nil || !strings.HasPrefix(t.source, "parameter `") {
		return
	}
	name := strings.TrimSuffix(strings.TrimPrefix(t.source, "parameter `"), "`")
	if i, ok := a.paramIndex[name]; ok {
		if _, have := a.recording[i]; !have {
			a.recording[i] = sinkReach{rule: rule, label: label, line: line, numeric: numeric}
		}
	}
}

// report adds a finding unless a summary pass is running.
func (a *analyzer) report(n ast.Node, rule, label string, t *trace, callee string) {
	if a.recording != nil {
		return
	}
	msg := fmt.Sprintf("%s flows into %s without being checked.", t.source, label)
	a.out = append(a.out, taintFinding{
		rule: rule, line: n.StartLine, endLine: n.EndLine, code: n.Text,
		message: msg, sink: label, t: t,
	})
}

// checkAssignSink reports a tainted value assigned to a dangerous property.
func (a *analyzer) checkAssignSink(n ast.Node, st *scopeState) {
	if len(a.lang.assigns) == 0 {
		return
	}
	info, ok := a.lang.bind(n)
	if !ok || len(info.values) == 0 {
		return
	}
	for _, target := range info.targets {
		for _, s := range a.lang.assigns {
			if !s.left.MatchString(target) {
				continue
			}
			if t := a.exprTaint(info.values[0], st); t != nil && !t.numeric {
				a.report(n, s.rule, s.label, t, target)
				a.recordSinkReach(t, s.rule, s.label, n.StartLine, false)
			}
		}
	}
}

// exprTaint returns how an expression is tainted, or nil when it is clean.
func (a *analyzer) exprTaint(n ast.Node, st *scopeState) *trace {
	lang := a.lang
	switch {
	case lang.callKinds[n.Kind]:
		callee, _ := pgSplitCall(n.Text)
		// A sanitizer or a pure check anywhere in a method chain ends the
		// taint: Path::new(p).file_name().unwrap() is a file name.
		if lang.sanitizers != nil && chainMatch(lang.sanitizers, callee) {
			return nil
		}
		if lang.pure != nil && chainMatch(lang.pure, callee) {
			return nil
		}
		numeric := lang.numeric != nil && chainMatch(lang.numeric, callee)
		if t := a.sourceOf(n, st); t != nil {
			if numeric {
				tc := *t
				tc.numeric = true
				return &tc
			}
			return t
		}
		if n.Kind == "macro_invocation" {
			if t := a.inlineFormatTaint(n, st); t != nil {
				return t
			}
		}
		// A call to a function of this file.
		if sum, ok := a.summaries[lastSegment(callee)]; ok && !a.ambiguous[lastSegment(callee)] {
			if sum.returnsSource {
				return &trace{source: fmt.Sprintf("the value `%s` returns", lastSegment(callee)), line: n.StartLine}
			}
			if argsNode, ok := n.Child("arguments"); ok {
				for i, arg := range argsNode.Children {
					if sum.returnsParam[i] {
						if t := a.exprTaint(arg, st); t != nil {
							return t
						}
					}
				}
			}
		}
		for _, c := range n.Children {
			if t := a.exprTaint(c, st); t != nil {
				if numeric {
					tc := *t
					tc.numeric = true
					return &tc
				}
				return t
			}
		}
		return nil
	case lang.exprKinds[n.Kind]:
		if t := a.sourceOf(n, st); t != nil {
			return t
		}
		if e, ok := st.taint[n.Text]; ok {
			return e.t
		}
		if obj := a.objectRoot(n, st); obj != nil {
			return obj
		}
	case isIdentifierKind(n.Kind):
		if e, ok := st.taint[n.Text]; ok {
			return e.t
		}
		if obj, ok := st.objects[n.Text]; ok {
			return &trace{source: obj.label, line: n.StartLine, local: obj.local}
		}
		return nil
	}
	if strings.HasSuffix(n.Kind, "literal") || n.Kind == "string" || n.Kind == "number" {
		return nil
	}
	// Rust format strings can name a value inline: format!("ls {path}").
	if n.Kind == "macro_invocation" {
		if t := a.inlineFormatTaint(n, st); t != nil {
			return t
		}
	}
	for _, c := range n.Children {
		if t := a.exprTaint(c, st); t != nil {
			return t
		}
	}
	return nil
}

var inlineFormatRE = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)(?::[^}]*)?\}`)

// inlineFormatTaint finds a tainted name written inside a format string.
func (a *analyzer) inlineFormatTaint(n ast.Node, st *scopeState) *trace {
	var found *trace
	var walk func(x ast.Node)
	walk = func(x ast.Node) {
		if found != nil {
			return
		}
		if x.Kind == "string_content" || x.Kind == "string_literal" {
			for _, m := range inlineFormatRE.FindAllStringSubmatch(x.Text, -1) {
				if e, ok := st.taint[m[1]]; ok {
					found = e.t
					return
				}
			}
		}
		for _, c := range x.Children {
			walk(c)
		}
	}
	walk(n)
	return found
}

// sourceOf reports whether the expression itself is a source.
func (a *analyzer) sourceOf(n ast.Node, st *scopeState) *trace {
	if len(n.Text) > 400 {
		return nil
	}
	for _, s := range a.lang.sources {
		if s.re.MatchString(n.Text) {
			return &trace{source: s.label, line: n.StartLine, local: s.local}
		}
	}
	return nil
}

// objectRoot returns the taint of a member access on a request-like object.
func (a *analyzer) objectRoot(n ast.Node, st *scopeState) *trace {
	root := rootName(n.Text)
	obj, ok := st.objects[root]
	if !ok {
		return nil
	}
	if obj.safe != nil && obj.safe.MatchString(n.Text) {
		return nil
	}
	return &trace{source: obj.label, line: n.StartLine, local: obj.local}
}

// chainMatch reports whether a regular expression matches the callee of a
// method chain or any leading part of it. "Path.new.file_name.unwrap" is
// tested as a whole and as "Path.new.file_name", "Path.new" and "Path", so a
// sanitizer in the middle of a chain is found.
func chainMatch(re *regexp.Regexp, callee string) bool {
	if re.MatchString(callee) {
		return true
	}
	for i := len(callee) - 1; i > 0; i-- {
		if callee[i] == '.' && re.MatchString(callee[:i]) {
			return true
		}
	}
	return false
}

// markGuarded notes that the function checks one of the names in an argument.
func (a *analyzer) markGuarded(t *trace, arg ast.Node, st *scopeState) *trace {
	if t.guarded {
		return t
	}
	for _, id := range identifiersIn(arg) {
		if st.guarded[id] {
			c := *t
			c.guarded = true
			return &c
		}
	}
	return t
}

// checkOutSource taints the argument a call fills with attacker data.
func (a *analyzer) checkOutSource(n ast.Node, st *scopeState) {
	if len(a.lang.outSources) == 0 {
		return
	}
	callee, _ := pgSplitCall(n.Text)
	argsNode, ok := n.Child("arguments")
	if !ok {
		return
	}
	for _, o := range a.lang.outSources {
		if !o.callee.MatchString(callee) || o.arg >= len(argsNode.Children) {
			continue
		}
		target := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(argsNode.Children[o.arg].Text), "&"))
		target = strings.TrimSpace(strings.TrimPrefix(target, "mut "))
		if target == "" {
			continue
		}
		st.taint[target] = taintEntry{t: &trace{source: o.label, line: n.StartLine, local: o.local}}
	}
}

// collectGuards finds the names a function checks, so that findings about
// them can say that a check exists.
func (a *analyzer) collectGuards(body ast.Node, st *scopeState) {
	if a.lang.guards == nil {
		return
	}
	var walk func(n ast.Node)
	walk = func(n ast.Node) {
		if a.lang.callKinds[n.Kind] {
			callee, _ := pgSplitCall(n.Text)
			if a.lang.guards.MatchString(callee) {
				for _, id := range identifiersIn(n) {
					st.guarded[id] = true
				}
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(body)
}

// ---- small helpers -------------------------------------------------------

func isIdentifierKind(kind string) bool { return kind == "identifier" }

// identifiersIn returns the identifier names inside a node.
func identifiersIn(n ast.Node) []string {
	var out []string
	var walk func(x ast.Node)
	walk = func(x ast.Node) {
		if x.Kind == "identifier" {
			out = append(out, x.Text)
		}
		for _, c := range x.Children {
			walk(c)
		}
	}
	walk(n)
	return out
}

// rootName returns the first segment of a member path ("req.query.id" is "req").
func rootName(text string) string {
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c == '.' || c == '[' || c == '(' || c == ':' || c == '-' {
			return text[:i]
		}
	}
	return text
}

func short(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func containsInt(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
