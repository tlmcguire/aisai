// Package ast parses Python source into a rendered syntax tree and a set of
// semantic chunks.
//
// The parser is backed by Tree-sitter. Parsing is done with cgo, so the
// package requires a C toolchain. Every C-allocated object (Parser, Tree) is
// closed before Parse returns; the returned tree is materialized into plain Go
// values so that no cgo handle escapes this package.
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
	"unicode/utf8"

	ts "github.com/tree-sitter/go-tree-sitter"
	py "github.com/tree-sitter/tree-sitter-python/bindings/go"

	"github.com/tlmcguire/aisai/internal/model"
)

// LanguagePython is the language identifier recorded on parsed files and
// their chunks.
const LanguagePython = "python"

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
	// Language is the language identifier, always LanguagePython for now.
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
}

// Root returns the root node of the materialized syntax tree. The zero Node is
// returned when the file was not parsed.
func (f *File) Root() Node {
	if f == nil {
		return Node{}
	}
	return f.root
}

// Parse parses a Python file from disk.
//
// The path is used to read the file and to derive the recorded path. It is
// normalized to slash separators, and relative paths are cleaned; an absolute
// path stays absolute. Callers that need the recorded path to be relative to
// the audit workspace should pass a workspace-relative path, or read the file
// themselves and call ParseString with the relative path.
//
// A syntax error does not abort the parse: the returned File holds whatever
// was recoverable together with a non-nil error, so callers can still scan the
// recoverable part.
//
// Files that are not valid UTF-8 are skipped: Parse returns a nil File and an
// error explaining why.
func Parse(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("ast: read %s: %w", path, err)
	}
	return ParseString(normalizePath(path), string(data))
}

// ParseString parses Python source that is already in memory.
//
// relPath is the workspace-relative path recorded on the result. As with
// Parse, a syntax error yields a partially populated File plus a non-nil
// error, and non-UTF-8 input is rejected outright.
func ParseString(relPath, content string) (*File, error) {
	path := normalizePath(relPath)
	if !utf8.ValidString(content) {
		return nil, fmt.Errorf("ast: %s: not valid UTF-8, skipping file", path)
	}

	file := &File{
		Path:     path,
		Content:  content,
		Language: LanguagePython,
	}

	parser := ts.NewParser()
	if parser == nil {
		return nil, fmt.Errorf("ast: %s: could not allocate tree-sitter parser", path)
	}
	defer parser.Close()

	if err := parser.SetLanguage(ts.NewLanguage(py.Language())); err != nil {
		return nil, fmt.Errorf("ast: %s: %w", path, err)
	}

	tree := parser.ParseCtx(context.Background(), []byte(content), nil)
	if tree == nil {
		return nil, fmt.Errorf("ast: %s: tree-sitter returned no tree", path)
	}
	defer tree.Close()

	tsRoot := tree.RootNode()
	file.root = materialize(tsRoot, content, 0)
	file.Nodes, file.Edges = render(file.root)

	if tsRoot.HasError() {
		file.Chunks = buildChunks(file)
		return file, fmt.Errorf("ast: %s: syntax error at %s", path, errorPosition(tsRoot))
	}

	file.Chunks = buildChunks(file)
	return file, nil
}

// materialize converts a Tree-sitter node into a plain Go Node, recursing into
// named children only. Anonymous tokens and comments are dropped: they carry
// no semantic value for chunking or rule evaluation.
func materialize(n *ts.Node, src string, depth int) Node {
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
	for i := uint(0); i < n.ChildCount(); i++ {
		child := n.Child(i)
		if !child.IsNamed() || child.Kind() == "comment" {
			continue
		}
		m := materialize(child, src, depth+1)
		m.Field = n.FieldNameForChild(uint32(i))
		out.Children = append(out.Children, m)
	}
	return out
}

// render converts the materialized tree into the flat node/edge form used by
// the renderer.
func render(root Node) ([]model.ASTNode, []model.ASTEdge) {
	var nodes []model.ASTNode
	var edges []model.ASTEdge
	var walk func(n Node, parentID string, depth int)
	walk = func(n Node, parentID string, depth int) {
		id := fmt.Sprintf("n%d", len(nodes))
		nodes = append(nodes, model.ASTNode{
			ID:        id,
			Type:      n.Kind,
			Label:     label(n),
			StartLine: n.StartLine,
			EndLine:   n.EndLine,
			Depth:     depth,
			ParentID:  parentID,
			Symbol:    symbol(n),
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
func label(n Node) string {
	switch n.Kind {
	case "function_definition":
		if name, ok := n.Child("name"); ok {
			return "def " + name.Text
		}
		return "def"
	case "class_definition":
		if name, ok := n.Child("name"); ok {
			return "class " + name.Text
		}
		return "class"
	case "identifier", "string":
		return truncate(n.Text, 40)
	default:
		return n.Kind
	}
}

// symbol returns the defined name of a node, if it defines one.
func symbol(n Node) string {
	switch n.Kind {
	case "function_definition", "class_definition":
		if name, ok := n.Child("name"); ok {
			return name.Text
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
