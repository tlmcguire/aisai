package sast

import (
	"strings"

	"github.com/tlmcguire/aisai/internal/ast"
)

// eventKind classifies the syntactic events collected during a scan.
type eventKind int

const (
	// evtCall is a call expression.
	evtCall eventKind = iota
	// evtAssign is an assignment statement.
	evtAssign
	// evtAssert is an assert statement.
	evtAssert
)

// event is one syntactic event collected during a scan.
type event struct {
	// kind classifies the event.
	kind eventKind
	// node is the node the event was collected from.
	node ast.Node
	// scope is the lexical scope the node belongs to.
	scope *scope
}

// scope holds the name resolution state of one lexical scope.
//
// Scopes are chained: an inner scope sees the aliases and taints of its
// enclosing scopes, which is how a value tainted at module level or in an
// enclosing function stays visible in a nested one.
type scope struct {
	// parent is the enclosing scope, nil for the module scope.
	parent *scope
	// aliases maps a local name to the qualified name it refers to, for
	// example "run" to "subprocess.run".
	aliases map[string]string
	// taints marks local names whose value was derived from a request
	// parameter, with the line of the assignment that derived it. The line
	// matters: a value tainted after a sink does not make that sink
	// reachable.
	taints map[string]int
	// startLine and endLine bound the scope in the source. They are zero
	// for the module scope, which spans the whole file.
	startLine int
	endLine   int
}

// newScope returns an empty scope nested in parent.
func newScope(parent *scope) *scope {
	return &scope{
		parent:  parent,
		aliases: map[string]string{},
		taints:  map[string]int{},
	}
}

// lookupAlias returns the qualified name bound to a local name, searching the
// scope chain from the innermost scope outwards.
func (s *scope) lookupAlias(name string) (string, bool) {
	for cur := s; cur != nil; cur = cur.parent {
		if v, ok := cur.aliases[name]; ok {
			return v, true
		}
	}
	return "", false
}

// lookupTaint reports whether a local name is tainted, searching the scope
// chain from the innermost scope outwards.
func (s *scope) lookupTaint(name string) bool {
	_, ok := s.lookupTaintLine(name)
	return ok
}

// lookupTaintLine returns the line at which a local name became tainted.
func (s *scope) lookupTaintLine(name string) (int, bool) {
	for cur := s; cur != nil; cur = cur.parent {
		if line, ok := cur.taints[name]; ok {
			return line, true
		}
	}
	return 0, false
}

// taintedBefore reports whether the scope chain holds a request-derived
// value that was tainted at or before the given line.
//
// The line bound matters: a sink on line 10 is not made reachable by an
// assignment on line 20.
func (s *scope) taintedBefore(line int) bool {
	for cur := s; cur != nil; cur = cur.parent {
		for _, taintLine := range cur.taints {
			if taintLine <= line {
				return true
			}
		}
	}
	return false
}

// resolve expands a callee expression into a qualified name.
//
// A dotted expression is resolved through the head segment, so that
// "run" bound by "from subprocess import run" resolves to "subprocess.run".
// Expressions that cannot be resolved are returned unchanged.
func (s *scope) resolve(name string) string {
	if name == "" {
		return ""
	}
	if i := strings.Index(name, "."); i > 0 {
		head, rest := name[:i], name[i+1:]
		if v, ok := s.lookupAlias(head); ok {
			return v + "." + rest
		}
		return name
	}
	if v, ok := s.lookupAlias(name); ok {
		return v
	}
	return name
}

// scanner walks one parsed file and collects the events the rules evaluate.
type scanner struct {
	// file is the file being scanned.
	file *ast.File
	// module is the outermost scope.
	module *scope
	// events are the collected events, in source order.
	events []event
	// scopes are every scope opened in the file, in source order. They are
	// used to find the scope that contains a given line.
	scopes []*scope
}

// newScanner returns a scanner for a file, with the module scope populated
// with the file's imports.
func newScanner(f *ast.File) *scanner {
	sc := &scanner{file: f, module: newScope(nil)}
	for local, qualified := range collectImportAliases(f.Root()) {
		sc.module.aliases[local] = qualified
	}
	return sc
}

// collect walks the file and records every call, assignment and assert.
func (sc *scanner) collect() {
	sc.walk(sc.file.Root(), sc.module)
}

// walk visits a node, recording events and maintaining the scope chain.
func (sc *scanner) walk(n ast.Node, s *scope) {
	switch n.Kind {
	case "function_definition", "class_definition":
		// Decorators and parameters belong to the enclosing scope, the body
		// opens a new one.
		for _, c := range n.Children {
			if c.Field == "body" {
				continue
			}
			sc.walk(c, s)
		}
		if body, ok := n.Child("body"); ok {
			inner := newScope(s)
			inner.startLine = n.StartLine
			inner.endLine = n.EndLine
			sc.scopes = append(sc.scopes, inner)
			sc.walk(body, inner)
		}
		return
	case "call":
		sc.events = append(sc.events, event{kind: evtCall, node: n, scope: s})
	case "assignment":
		sc.events = append(sc.events, event{kind: evtAssign, node: n, scope: s})
		sc.recordAssignment(n, s)
	case "assert_statement":
		sc.events = append(sc.events, event{kind: evtAssert, node: n, scope: s})
	}
	for _, c := range n.Children {
		sc.walk(c, s)
	}
}

// recordAssignment updates the scope with what an assignment reveals: a name
// derived from a request parameter becomes tainted, and a name bound to an
// HTTP client object becomes an alias.
func (sc *scanner) recordAssignment(n ast.Node, s *scope) {
	left, ok := n.Child("left")
	if !ok {
		return
	}
	right, ok := n.Child("right")
	if !ok {
		return
	}
	names := assignedNames(left)
	if len(names) == 0 {
		return
	}
	if containsRequestSource(right) || referencesTainted(right, s) {
		for _, name := range names {
			s.taints[name] = n.StartLine
		}
	}
	if qualified, ok := aliasTarget(right, s); ok {
		for _, name := range names {
			s.aliases[name] = qualified
		}
	}
}

// httpClientModules are the modules whose objects are tracked as aliases, so
// that a client held in a variable is still recognized as an HTTP client.
var httpClientModules = map[string]bool{
	"requests": true,
	"httpx":    true,
	"urllib":   true,
	"urllib3":  true,
	"aiohttp":  true,
	"http":     true,
}

// aliasTarget returns the qualified name a right-hand side binds to, when the
// right-hand side is an HTTP client object or attribute.
func aliasTarget(right ast.Node, s *scope) (string, bool) {
	var qualified string
	switch right.Kind {
	case "attribute":
		qualified = s.resolve(right.Text)
	case "call":
		fn, ok := right.Child("function")
		if !ok {
			return "", false
		}
		qualified = s.resolve(fn.Text)
	default:
		return "", false
	}
	if qualified == "" {
		return "", false
	}
	head := qualified
	if i := strings.Index(head, "."); i > 0 {
		head = head[:i]
	}
	if !httpClientModules[head] {
		return "", false
	}
	return qualified, true
}

// assignedNames returns the local names bound by the left-hand side of an
// assignment.
func assignedNames(left ast.Node) []string {
	switch left.Kind {
	case "identifier":
		return []string{left.Text}
	case "attribute":
		if attr, ok := left.Child("attribute"); ok {
			return []string{attr.Text}
		}
	}
	return nil
}

// collectImportAliases returns the local-name to qualified-name bindings
// created by the import statements of a file.
func collectImportAliases(root ast.Node) map[string]string {
	out := map[string]string{}
	for _, c := range root.Children {
		switch c.Kind {
		case "import_statement":
			for _, ch := range c.Children {
				if ch.Field != "name" {
					continue
				}
				switch ch.Kind {
				case "dotted_name":
					out[firstSegment(ch.Text)] = ch.Text
				case "aliased_import":
					name, okName := ch.Child("name")
					alias, okAlias := ch.Child("alias")
					if okName && okAlias {
						out[alias.Text] = name.Text
					}
				}
			}
		case "import_from_statement", "future_import_statement":
			base := ""
			if m, ok := c.Child("module_name"); ok && m.Kind == "dotted_name" {
				base = m.Text
			}
			for _, ch := range c.Children {
				if ch.Field != "name" {
					continue
				}
				switch ch.Kind {
				case "dotted_name":
					out[lastSegment(ch.Text)] = joinQualified(base, ch.Text)
				case "identifier":
					out[ch.Text] = joinQualified(base, ch.Text)
				case "aliased_import":
					name, okName := ch.Child("name")
					alias, okAlias := ch.Child("alias")
					if !okName || !okAlias {
						continue
					}
					local := alias.Text
					if name.Kind == "dotted_name" {
						local = lastSegment(name.Text)
					}
					out[local] = joinQualified(base, name.Text)
				}
			}
		}
	}
	return out
}

// requestAttributes are the request members that carry attacker-controlled
// input.
var requestAttributes = map[string]bool{
	"args":         true,
	"form":         true,
	"values":       true,
	"GET":          true,
	"POST":         true,
	"json":         true,
	"data":         true,
	"cookies":      true,
	"headers":      true,
	"files":        true,
	"body":         true,
	"query_params": true,
	"get_json":     true,
	"get_data":     true,
}

// containsRequestSource reports whether a node reads attacker-controlled input
// out of an incoming HTTP request.
func containsRequestSource(n ast.Node) bool {
	if n.Kind == "attribute" {
		if attr, ok := n.Child("attribute"); ok && requestAttributes[attr.Text] {
			if obj, ok := n.Child("object"); ok && strings.Contains(strings.ToLower(obj.Text), "request") {
				return true
			}
		}
	}
	for _, c := range n.Children {
		if containsRequestSource(c) {
			return true
		}
	}
	return false
}

// requestSourceNode returns the first request-derived node inside a node.
func requestSourceNode(n ast.Node) (ast.Node, bool) {
	if n.Kind == "attribute" {
		if attr, ok := n.Child("attribute"); ok && requestAttributes[attr.Text] {
			if obj, ok := n.Child("object"); ok && strings.Contains(strings.ToLower(obj.Text), "request") {
				return n, true
			}
		}
	}
	for _, c := range n.Children {
		if found, ok := requestSourceNode(c); ok {
			return found, true
		}
	}
	return ast.Node{}, false
}

// referencesTainted reports whether a node reads a tainted local name.
func referencesTainted(n ast.Node, s *scope) bool {
	if n.Kind == "identifier" && s.lookupTaint(n.Text) {
		return true
	}
	for _, c := range n.Children {
		if referencesTainted(c, s) {
			return true
		}
	}
	return false
}

// taintedNames returns the tainted local names referenced by a node.
func taintedNames(n ast.Node, s *scope) []string {
	var out []string
	if n.Kind == "identifier" && s.lookupTaint(n.Text) {
		out = append(out, n.Text)
	}
	for _, c := range n.Children {
		out = append(out, taintedNames(c, s)...)
	}
	return out
}

// firstSegment returns the first dot-separated segment of a dotted name.
func firstSegment(s string) string {
	if i := strings.Index(s, "."); i > 0 {
		return s[:i]
	}
	return s
}

// lastSegment returns the last dot-separated segment of a dotted name.
func lastSegment(s string) string {
	if i := strings.LastIndex(s, "."); i >= 0 {
		return s[i+1:]
	}
	return s
}

// joinQualified joins a module path and a member name.
func joinQualified(base, name string) string {
	if base == "" {
		return name
	}
	return base + "." + name
}
