// Package ast parses source code into a rendered syntax tree and a set of
// semantic chunks.
//
// The parser is backed by Tree-sitter and supports every language listed in
// the language registry: Python, JavaScript, TypeScript, TSX, Go, Rust, Java,
// C#, Ruby and C. Parsing is done with cgo, so the package requires a C
// toolchain. Every C-allocated object (Parser, Tree) is closed before Parse
// returns; the returned tree is materialized into plain Go values so that no
// cgo handle escapes this package.
//
// Parse and ParseString are safe for concurrent use: each call creates and
// destroys its own parser and shares no state with any other call.
package ast

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"
	"sync"
	"unicode/utf8"

	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/tlmcguire/aisai/internal/model"
)

// maxMaterializeDepth bounds the recursion depth used while materializing a
// syntax tree, so that a pathologically nested file cannot exhaust the stack.
const maxMaterializeDepth = 400

// File is a parsed source file together with its rendered syntax tree and its
// semantic chunks.
type File struct {
	// Path is the workspace-relative, slash-separated path of the file.
	Path string
	// Content is the full source text of the file.
	Content string
	// Language is the language identifier of the file, for example
	// "python" or "go".
	Language string
	// Nodes are the rendered syntax-tree nodes, in depth-first pre-order.
	Nodes []model.ASTNode
	// Edges are the parent/child relations between Nodes.
	Edges []model.ASTEdge
	// Chunks are the semantic chunks of the file.
	Chunks []model.Chunk

	// root is the materialized syntax tree. It is unexported so that the
	// exported shape of File stays exactly as documented; use Root to reach it.
	root Node
	// lang is the language the file was parsed with.
	lang *Language
}

// Root returns the root node of the materialized syntax tree. The zero Node is
// returned when the file was not parsed.
func (f *File) Root() Node {
	if f == nil {
		return Node{}
	}
	return f.root
}

// LanguageDef returns the language the file was parsed with, or nil.
func (f *File) LanguageDef() *Language {
	if f == nil {
		return nil
	}
	return f.lang
}

// parserPools keeps one pool of Tree-sitter parsers per language.
//
// Allocating a parser is expensive and an audit parses thousands of files, so
// parsers are recycled. Each pool only ever hands out parsers that are
// configured for its language.
var parserPools sync.Map

// acquireParser returns a parser configured for the language.
func acquireParser(lang *Language) *ts.Parser {
	value, _ := parserPools.LoadOrStore(lang.ID, &sync.Pool{
		New: func() any {
			parser := ts.NewParser()
			if parser == nil {
				return nil
			}
			if err := parser.SetLanguage(lang.Grammar()); err != nil {
				parser.Close()
				return nil
			}
			return parser
		},
	})
	pool, ok := value.(*sync.Pool)
	if !ok {
		return nil
	}
	parser, ok := pool.Get().(*ts.Parser)
	if !ok {
		return nil
	}
	return parser
}

// releaseParser returns a parser to its pool.
func releaseParser(lang *Language, parser *ts.Parser) {
	if parser == nil {
		return
	}
	value, ok := parserPools.Load(lang.ID)
	if !ok {
		parser.Close()
		return
	}
	value.(*sync.Pool).Put(parser)
}

// Parse parses a source file from disk.
//
// The path is used to read the file, to derive the recorded path and to
// choose the language. It is normalized to slash separators, and relative
// paths are cleaned; an absolute path stays absolute. Callers that need the
// recorded path to be relative to the audit workspace should pass a
// workspace-relative path, or read the file themselves and call ParseString
// with the relative path.
//
// A file whose extension belongs to no supported language is rejected with an
// error: parsing it as Python would produce a meaningless tree.
//
// A syntax error does not abort the parse: the returned File holds whatever
// was recoverable together with a non-nil error, so callers can still scan
// the recoverable part.
//
// Files that are not valid UTF-8 are skipped: Parse returns a nil File and an
// error explaining why.
func Parse(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("ast: read %s: %w", path, err)
	}
	rel := normalizePath(path)
	lang := LanguageForPath(rel)
	if lang == nil {
		return nil, fmt.Errorf("ast: %s: unsupported language", rel)
	}
	return ParseSource(rel, string(data), lang)
}

// ParseString parses source that is already in memory.
//
// relPath is the workspace-relative path recorded on the result. The language
// is chosen from the file extension of relPath. As with Parse, a syntax error
// yields a partially populated File plus a non-nil error, and non-UTF-8 input
// is rejected outright.
func ParseString(relPath, content string) (*File, error) {
	path := normalizePath(relPath)
	lang := LanguageForPath(path)
	if lang == nil {
		return nil, fmt.Errorf("ast: %s: unsupported language", path)
	}
	return ParseSource(path, content, lang)
}

// ParseSource parses source that is already in memory with an explicit
// language.
//
// It is the entry point for callers that know the language already, for
// example because they resolved it from a file name that has no extension.
func ParseSource(relPath, content string, lang *Language) (*File, error) {
	if lang == nil {
		return nil, fmt.Errorf("ast: %s: no language", relPath)
	}
	path := normalizePath(relPath)
	if !utf8.ValidString(content) {
		return nil, fmt.Errorf("ast: %s: not valid UTF-8, skipping file", path)
	}

	file := &File{
		Path:     path,
		Content:  content,
		Language: lang.ID,
		lang:     lang,
	}

	parser := acquireParser(lang)
	if parser == nil {
		return nil, fmt.Errorf("ast: %s: could not allocate tree-sitter parser", path)
	}
	defer releaseParser(lang, parser)

	tree := parser.ParseCtx(context.Background(), []byte(content), nil)
	if tree == nil {
		return nil, fmt.Errorf("ast: %s: tree-sitter returned no tree", path)
	}
	defer tree.Close()

	tsRoot := tree.RootNode()
	file.root = materialize(tsRoot, content, 0, lang)
	file.Nodes, file.Edges = render(file.root, lang)

	if tsRoot.HasError() {
		file.Chunks = buildChunks(file)
		return file, fmt.Errorf("ast: %s: syntax error at %s", path, errorPosition(tsRoot))
	}

	file.Chunks = buildChunks(file)
	return file, nil
}

// materialize converts a Tree-sitter node into a plain Go Node, recursing into
// named children only. Anonymous tokens are dropped: they carry no semantic
// value for chunking or rule evaluation.
//
// Comments are kept for the languages that document a definition with a
// comment block, because the chunker needs them to recover the documentation.
// They are dropped for Python, whose documentation lives in a docstring.
func materialize(n *ts.Node, src string, depth int, lang *Language) Node {
	out := Node{
		Kind:      n.Kind(),
		Text:      src[n.StartByte():n.EndByte()],
		StartLine: int(n.StartPosition().Row) + 1,
		EndLine:   int(n.EndPosition().Row) + 1,
		StartByte: int(n.StartByte()),
		EndByte:   int(n.EndByte()),
	}
	if depth >= maxMaterializeDepth {
		return out
	}
	keepComments := lang.doc == docComment
	for i := uint(0); i < n.ChildCount(); i++ {
		child := n.Child(i)
		if !child.IsNamed() {
			continue
		}
		kind := child.Kind()
		if lang.IsComment(kind) && !keepComments {
			continue
		}
		m := materialize(child, src, depth+1, lang)
		m.Field = n.FieldNameForChild(uint32(i))
		out.Children = append(out.Children, m)
	}
	return out
}

// render converts the materialized tree into the flat node/edge form used by
// the renderer.
func render(root Node, lang *Language) ([]model.ASTNode, []model.ASTEdge) {
	var nodes []model.ASTNode
	var edges []model.ASTEdge
	var walk func(n Node, parentID string, depth int)
	walk = func(n Node, parentID string, depth int) {
		id := fmt.Sprintf("n%d", len(nodes))
		nodes = append(nodes, model.ASTNode{
			ID:        id,
			Type:      n.Kind,
			Label:     label(n, lang),
			StartLine: n.StartLine,
			EndLine:   n.EndLine,
			Depth:     depth,
			ParentID:  parentID,
			Symbol:    symbol(n, lang),
		})
		if parentID != "" {
			edges = append(edges, model.ASTEdge{Source: parentID, Target: id})
		}
		for _, c := range n.Children {
			walk(c, id, depth+1)
		}
	}
	walk(root, "", 0)
	return nodes, edges
}

// label produces a human-readable label for a node.
func label(n Node, lang *Language) string {
	switch {
	case lang.IsFunction(n.Kind):
		if name := definitionName(n); name != "" {
			return lang.functionLabel + " " + name
		}
		return lang.functionLabel
	case lang.IsClass(n.Kind):
		if name := definitionName(n); name != "" {
			return lang.classLabel + " " + name
		}
		return lang.classLabel
	}
	switch n.Kind {
	case "identifier", "string", "type_identifier", "property_identifier",
		"field_identifier", "simple_identifier", "constant":
		return truncate(n.Text, 40)
	default:
		return n.Kind
	}
}

// symbol returns the defined name of a node, if it defines one.
func symbol(n Node, lang *Language) string {
	if lang.IsFunction(n.Kind) || lang.IsClass(n.Kind) {
		return definitionName(n)
	}
	return ""
}

// definitionName returns the name a definition node introduces.
//
// The name lives in the "name" field in most grammars. C hides it behind a
// chain of declarators, and Go hides it behind a type spec, so those shapes
// are unwrapped explicitly.
func definitionName(n Node) string {
	if name, ok := n.Child("name"); ok {
		return name.Text
	}
	switch n.Kind {
	case "type_declaration":
		if spec, ok := n.Child("type_spec"); ok {
			return definitionName(spec)
		}
	case "impl_item":
		if t, ok := n.Child("type"); ok {
			return t.Text
		}
	}
	if decl, ok := n.Child("declarator"); ok {
		if name := declaratorName(decl); name != "" {
			return name
		}
	}
	for _, c := range n.Children {
		switch c.Kind {
		case "identifier", "type_identifier", "property_identifier",
			"field_identifier", "simple_identifier", "constant", "name":
			return c.Text
		}
	}
	return ""
}

// declaratorName unwraps a chain of C declarators down to the identifier.
func declaratorName(n Node) string {
	switch n.Kind {
	case "identifier", "field_identifier", "type_identifier", "operator_name":
		return n.Text
	}
	if inner, ok := n.Child("declarator"); ok {
		return declaratorName(inner)
	}
	for _, c := range n.Children {
		switch c.Kind {
		case "identifier", "field_identifier", "type_identifier", "operator_name":
			return c.Text
		}
	}
	return ""
}

// errorPosition locates the first syntax error in a tree for reporting.
func errorPosition(root *ts.Node) string {
	if err := findError(root, 0); err != nil {
		return fmt.Sprintf("line %d", int(err.StartPosition().Row)+1)
	}
	return "unknown position"
}

// findError walks the tree looking for the first ERROR or MISSING node.
func findError(n *ts.Node, depth int) *ts.Node {
	if depth > maxMaterializeDepth {
		return nil
	}
	if n.IsError() || n.IsMissing() {
		return n
	}
	for i := uint(0); i < n.ChildCount(); i++ {
		c := n.Child(i)
		if found := findError(c, depth+1); found != nil {
			return found
		}
	}
	return nil
}

// truncate shortens s to at most n characters, appending an ellipsis when it
// had to cut.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// normalizePath converts a path to slash separators and cleans it. Relative
// paths stay relative, absolute paths stay absolute.
func normalizePath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	p = path.Clean(p)
	if p == "." {
		return ""
	}
	return p
}
