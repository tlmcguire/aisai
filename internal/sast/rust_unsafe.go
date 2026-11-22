package sast

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/tlmcguire/aisai/internal/ast"
	"github.com/tlmcguire/aisai/internal/model"
)

// This file holds the rules for Rust's escape hatches.
//
// Safe Rust cannot corrupt memory, which is the reason to use it. `unsafe`
// is the keyword that turns the compiler's proof off for a block, a function
// or an impl, and the author takes over the proof. A security review of Rust
// code is mostly a review of those places: each one is a promise that the
// compiler could not check. These rules find them and say what each one
// promises. See https://doc.rust-lang.org/nomicon/ (the Rustonomicon) for the
// obligations an unsafe block has to meet.

// The identifiers of the Rust rules.
const (
	// RuleRustUnsafe marks an unsafe block or function.
	RuleRustUnsafe = "rust-unsafe-block"
	// RuleRustUnsafeOp marks a call that is dangerous when its arguments are wrong.
	RuleRustUnsafeOp = "rust-unsafe-operation"
	// RuleRustSendSync marks an `unsafe impl Send` or `Sync`.
	RuleRustSendSync = "rust-unsafe-send-sync"
	// RuleRustStaticMut marks a `static mut`.
	RuleRustStaticMut = "rust-static-mut"
	// RuleRustFFI marks the boundary to code written in another language.
	RuleRustFFI = "rust-ffi-boundary"
)

var rustRuleTable = []Rule{
	{ID: RuleRustUnsafe, Name: "Unsafe code", CWE: "CWE-119", Severity: model.SeverityLow, Confidence: 0.50,
		Description: "Code in an unsafe block or function opts out of Rust's memory-safety checks. Its safety rests on an argument the compiler cannot verify, so it needs a human review of that argument."},
	{ID: RuleRustUnsafeOp, Name: "Unchecked memory operation", CWE: "CWE-119", Severity: model.SeverityMedium, Confidence: 0.60,
		Description: "A call that is undefined behaviour when its pointer, length or layout is wrong, such as transmute, from_raw_parts or get_unchecked. Check what guarantees the arguments."},
	{ID: RuleRustSendSync, Name: "Unsafe Send or Sync", CWE: "CWE-362", Severity: model.SeverityMedium, Confidence: 0.60,
		Description: "`unsafe impl Send` or `Sync` tells the compiler a type is safe to share between threads without proof. If it is not, the result is a data race."},
	{ID: RuleRustStaticMut, Name: "Mutable global", CWE: "CWE-362", Severity: model.SeverityMedium, Confidence: 0.60,
		Description: "A `static mut` can be changed from any thread, and every access needs unsafe. Two threads touching it is a data race. Prefer an atomic, a Mutex or a OnceLock."},
	{ID: RuleRustFFI, Name: "Foreign function boundary", CWE: "CWE-119", Severity: model.SeverityLow, Confidence: 0.50,
		Description: "Rust's guarantees stop at the boundary to C or another language: foreign functions can do anything, and functions exported over the C ABI trust their callers."},
}

// rustOps are the calls reported inside unsafe code, with what makes each one dangerous.
var rustOps = []struct {
	re   *regexp.Regexp
	what string
}{
	{regexp.MustCompile(`(?:^|\.)transmute(?:_copy)?$`), "transmute reinterprets bytes as another type; a wrong size or layout is undefined behaviour"},
	{regexp.MustCompile(`(?:^|\.)from_raw_parts(?:_mut)?$`), "builds a slice or vector from a raw pointer and length that nothing checks"},
	{regexp.MustCompile(`\.get_unchecked(?:_mut)?$`), "indexes without a bounds check"},
	{regexp.MustCompile(`\.set_len$`), "sets a vector's length without initialising or checking the elements"},
	{regexp.MustCompile(`(?:^|\.)(?:assume_init|assume_init_ref|assume_init_mut|zeroed|uninitialized)$`), "uses memory that may be uninitialised"},
	{regexp.MustCompile(`(?:^|\.)ptr\.(?:read|read_volatile|read_unaligned|write|write_volatile|write_unaligned|copy|copy_nonoverlapping|write_bytes|swap|drop_in_place)$`), "reads or writes through a raw pointer"},
	{regexp.MustCompile(`(?:^|\.)from_utf8_unchecked(?:_mut)?$`), "skips UTF-8 validation"},
	{regexp.MustCompile(`(?:^|\.)(?:Box|Vec|String|CString|Rc|Arc)\.from_raw$`), "takes ownership of a raw pointer; a wrong pointer is a double free or a dangling reference"},
	{regexp.MustCompile(`(?:^|\.)unreachable_unchecked$`), "promises the compiler a branch never runs"},
	{regexp.MustCompile(`\.unchecked_(?:add|sub|mul|shl|shr)$`), "does arithmetic without an overflow check"},
}

// scanRustUnsafe finds the unsafe code of a Rust file.
func scanRustUnsafe(f *ast.File) []model.Finding {
	root := f.Root()
	sc := &scanner{file: f}
	foreign := foreignFunctions(root)
	var out []model.Finding
	add := func(id string, n ast.Node, message, sink string) {
		r, ok := patternRuleByID(id)
		if !ok {
			return
		}
		out = append(out, newFinding(r, sc, n, message, model.Evidence{Sink: sink}))
	}

	var walk func(n ast.Node, unsafeCtx bool)
	walk = func(n ast.Node, unsafeCtx bool) {
		switch n.Kind {
		case "static_item":
			if _, ok := n.ChildByKind("mutable_specifier"); ok {
				name, _ := n.Child("name")
				add(RuleRustStaticMut, n, fmt.Sprintf("`static mut %s` is global state any thread can change; each access needs unsafe.", name.Text), "static mut")
			}
		case "impl_item":
			if strings.HasPrefix(strings.TrimSpace(n.Text), "unsafe impl") {
				if trait, ok := n.Child("trait"); ok && (trait.Text == "Send" || trait.Text == "Sync") {
					typ, _ := n.Child("type")
					add(RuleRustSendSync, n, fmt.Sprintf("`unsafe impl %s for %s` asserts thread safety the compiler could not prove.", trait.Text, typ.Text), "unsafe impl "+trait.Text)
				}
			}
		case "foreign_mod_item":
			names := foreignNames(n)
			add(RuleRustFFI, n, fmt.Sprintf("Declares %d foreign function%s (%s). Calling one is unsafe and Rust cannot check what it does.", len(names), plural(len(names)), strings.Join(names, ", ")), "extern block")
		case "function_item":
			mods, _ := n.ChildByKind("function_modifiers")
			isUnsafe := strings.Contains(mods.Text, "unsafe")
			name, _ := n.Child("name")
			if strings.Contains(mods.Text, "extern") && strings.Contains(n.Text[:min(len(n.Text), 200)], "pub") {
				add(RuleRustFFI, n, fmt.Sprintf("`%s` is exported over the C ABI, so its callers are not checked by Rust: validate every pointer and length it receives.", name.Text), "extern \"C\" fn")
			}
			if isUnsafe {
				add(RuleRustUnsafe, n, fmt.Sprintf("`unsafe fn %s`: every caller must uphold its safety contract, which the compiler cannot check.", name.Text), "unsafe fn")
			}
			for _, c := range n.Children {
				walk(c, unsafeCtx || isUnsafe)
			}
			return
		case "unsafe_block":
			ops := unsafeOps(n, foreign)
			msg := "Unsafe block: its safety rests on an argument the compiler cannot check."
			if len(ops) > 0 {
				msg = "Unsafe block containing " + strings.Join(uniqueSorted(opNames(ops)), ", ") + "."
			}
			add(RuleRustUnsafe, n, msg, "unsafe block")
			for _, op := range ops {
				// Calls to foreign functions are named in the block's finding;
				// one finding each would bury the operations that matter.
				if op.foreign {
					continue
				}
				add(RuleRustUnsafeOp, op.node, fmt.Sprintf("`%s` %s.", op.name, op.what), op.name)
			}
			// The operations in the block are reported above; walk inside only
			// for nested items, so they are not reported twice.
			for _, c := range n.Children {
				walk(c, false)
			}
			return
		case "call_expression":
			// Operations inside an unsafe fn (not an unsafe block) are reported here.
			if unsafeCtx {
				callee, _ := pgSplitCall(n.Text)
				if what, ok := opWhat(callee, foreign); ok && !foreign[lastSegment(callee)] {
					add(RuleRustUnsafeOp, n, fmt.Sprintf("`%s` %s.", short(callee, 50), what), callee)
				}
			}
		}
		for _, c := range n.Children {
			walk(c, unsafeCtx)
		}
	}
	walk(root, false)
	return out
}

// unsafeOp is a dangerous call found inside an unsafe block.
type unsafeOp struct {
	node    ast.Node
	name    string
	what    string
	foreign bool
}

func unsafeOps(block ast.Node, foreign map[string]bool) []unsafeOp {
	var ops []unsafeOp
	var walk func(n ast.Node)
	walk = func(n ast.Node) {
		if n.Kind == "call_expression" {
			callee, _ := pgSplitCall(n.Text)
			if what, ok := opWhat(callee, foreign); ok {
				ops = append(ops, unsafeOp{node: n, name: short(lastTwo(callee), 40), what: what, foreign: foreign[lastSegment(callee)]})
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(block)
	return ops
}

func opWhat(callee string, foreign map[string]bool) (string, bool) {
	for _, op := range rustOps {
		if op.re.MatchString(callee) {
			return op.what, true
		}
	}
	if foreign[lastSegment(callee)] {
		return "calls a foreign function (C ABI); Rust cannot check what it does", true
	}
	return "", false
}

// foreignFunctions collects the names declared in extern blocks.
func foreignFunctions(root ast.Node) map[string]bool {
	out := map[string]bool{}
	var walk func(n ast.Node)
	walk = func(n ast.Node) {
		if n.Kind == "foreign_mod_item" {
			for _, name := range foreignNames(n) {
				out[name] = true
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	return out
}

func foreignNames(block ast.Node) []string {
	var names []string
	var walk func(n ast.Node)
	walk = func(n ast.Node) {
		if n.Kind == "function_signature_item" {
			if name, ok := n.Child("name"); ok {
				names = append(names, name.Text)
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(block)
	return names
}

func opNames(ops []unsafeOp) []string {
	out := make([]string, len(ops))
	for i, o := range ops {
		out[i] = o.name
	}
	return out
}

func uniqueSorted(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	sort.Strings(out)
	if len(out) > 4 {
		out = append(out[:4], fmt.Sprintf("and %d more", len(out)-4))
	}
	return out
}

func lastTwo(callee string) string {
	parts := strings.Split(callee, ".")
	if len(parts) > 2 {
		parts = parts[len(parts)-2:]
	}
	return strings.Join(parts, "::")
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
