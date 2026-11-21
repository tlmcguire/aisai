package ast

import "strings"

// Node is a materialized node of a parsed syntax tree.
//
// A Node is a plain Go value: the underlying Tree-sitter tree is closed before
// parsing returns, so Nodes are safe to retain, share between goroutines, and
// compare by value. Text holds the source slice of the node, which shares the
// backing array of the parsed file, so keeping a Node alive keeps the file
// content alive.
type Node struct {
	// Kind is the Tree-sitter node kind, e.g. "call" or "function_definition".
	Kind string
	// Field is the Tree-sitter field name binding this node to its parent
	// ("function", "arguments", "left", ...). It is empty when the parent does
	// not label the child.
	Field string
	// Text is the source text spanned by the node.
	Text string
	// StartLine and EndLine are 1-based inclusive line numbers.
	StartLine int
	EndLine   int
	// StartByte and EndByte are 0-based byte offsets into the file content.
	StartByte int
	EndByte   int
	// Children holds the node's named children, in source order. Anonymous
	// tokens and comments are not included.
	Children []Node
}

// Child returns the child bound to the given field name.
func (n Node) Child(field string) (Node, bool) {
	for _, c := range n.Children {
		if c.Field == field {
			return c, true
		}
	}
	return Node{}, false
}

// ChildByKind returns the first child with the given kind.
func (n Node) ChildByKind(kind string) (Node, bool) {
	for _, c := range n.Children {
		if c.Kind == kind {
			return c, true
		}
	}
	return Node{}, false
}

// Find returns the first descendant with the given kind, in depth-first
// pre-order. The receiver itself is not considered.
func (n Node) Find(kind string) (Node, bool) {
	for _, c := range n.Children {
		if c.Kind == kind {
			return c, true
		}
		if found, ok := c.Find(kind); ok {
			return found, true
		}
	}
	return Node{}, false
}

// HasDescendant reports whether any descendant has the given kind.
func (n Node) HasDescendant(kind string) bool {
	_, ok := n.Find(kind)
	return ok
}

// Has reports whether the node itself or any of its descendants has the given
// kind.
func (n Node) Has(kind string) bool {
	if n.Kind == kind {
		return true
	}
	return n.HasDescendant(kind)
}

// BaseName returns the last dot-separated segment of an identifier or
// attribute expression, e.g. "run" for "subprocess.run".
func (n Node) BaseName() string {
	text := n.Text
	if i := strings.LastIndex(text, "."); i >= 0 {
		text = text[i+1:]
	}
	return text
}
