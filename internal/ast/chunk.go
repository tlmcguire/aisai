package ast

import (
	"fmt"
	"strings"

	"github.com/tlmcguire/aisai/internal/model"
)

// smallFileTokens is the approximate token threshold below which a whole file
// is emitted as a single chunk of type model.ChunkFile instead of being split
// into finer semantic chunks.
const smallFileTokens = 400

// bytesPerToken is the rough characters-per-token ratio used to estimate the
// token count of a file without running a tokenizer.
const bytesPerToken = 4

// chunkBuilder accumulates the chunks of one file.
type chunkBuilder struct {
	file    *File
	lang    *Language
	imports []string
	chunks  []model.Chunk
}

// buildChunks derives the semantic chunks of a parsed file.
//
// Empty files and files that contain nothing but comments produce no chunks.
// Files below roughly 400 tokens produce exactly one chunk of type
// model.ChunkFile. Larger files are split into a module chunk (imports and
// the module documentation), block chunks (contiguous top-level statements)
// and class/function chunks for every definition, including nested ones.
func buildChunks(f *File) []model.Chunk {
	if f == nil || f.lang == nil {
		return nil
	}
	root := f.root
	if len(root.Children) == 0 {
		return nil
	}

	b := &chunkBuilder{file: f, lang: f.lang}
	b.imports = collectImports(root.Children, f.lang)

	if estimateTokens(f.Content) < smallFileTokens {
		b.chunks = append(b.chunks, b.fileChunk())
		return b.chunks
	}

	top := root.Children
	doc := moduleDoc(top, f.lang)
	moduleID := b.addModuleChunk(top, doc)
	b.addBlockChunks(top, doc, moduleID)
	b.addDefinitionChunks(root, nil, moduleID)
	return b.chunks
}

// estimateTokens approximates the token count of a source file.
func estimateTokens(content string) int {
	return len(content) / bytesPerToken
}

// fileChunk builds the single chunk used for small files.
func (b *chunkBuilder) fileChunk() model.Chunk {
	f := b.file
	start := 1
	end := 1
	if n := strings.Count(f.Content, "\n"); n > 0 {
		end = n + 1
	}
	doc := moduleDoc(f.root.Children, b.lang)
	return model.Chunk{
		ID:         chunkID(f.Path, start, "module"),
		File:       f.Path,
		StartLine:  start,
		EndLine:    end,
		Type:       model.ChunkFile,
		SymbolPath: "module",
		Docstring:  doc.text,
		Content:    f.Content,
		Calls:      collectCalls(f.root, Node{}, b.lang, true),
		Imports:    b.imports,
		Language:   b.lang.ID,
	}
}

// addModuleChunk adds the chunk covering the file's imports and module
// documentation, and returns its id. It returns an empty id when the file has
// neither.
func (b *chunkBuilder) addModuleChunk(top []Node, doc docInfo) string {
	var start, end int
	have := false
	consider := func(n Node) {
		if !have {
			start, end = n.StartByte, n.EndByte
			have = true
			return
		}
		if n.StartByte < start {
			start = n.StartByte
		}
		if n.EndByte > end {
			end = n.EndByte
		}
	}
	if doc.ok && doc.node.Kind != "" {
		consider(doc.node)
	}
	for _, c := range top {
		if isImportNode(c, b.lang) {
			consider(c)
		}
	}
	if !have {
		return ""
	}
	startLine := lineOf(b.file.Content, start)
	endLine := lineOf(b.file.Content, end)
	id := chunkID(b.file.Path, startLine, "module")
	b.chunks = append(b.chunks, model.Chunk{
		ID:         id,
		File:       b.file.Path,
		StartLine:  startLine,
		EndLine:    endLine,
		Type:       model.ChunkModule,
		SymbolPath: "module",
		Docstring:  doc.text,
		Content:    b.file.Content[start:end],
		Imports:    b.imports,
		Language:   b.lang.ID,
	})
	return id
}

// addBlockChunks adds one chunk per contiguous run of top-level statements
// that are not imports, not the module documentation and not part of a
// definition.
func (b *chunkBuilder) addBlockChunks(top []Node, doc docInfo, parentID string) {
	var run []Node
	flush := func() {
		if len(run) == 0 {
			return
		}
		first, last := run[0], run[len(run)-1]
		symbolPath := "module"
		b.chunks = append(b.chunks, model.Chunk{
			ID:         chunkID(b.file.Path, first.StartLine, symbolPath),
			File:       b.file.Path,
			StartLine:  first.StartLine,
			EndLine:    last.EndLine,
			Type:       model.ChunkBlock,
			SymbolPath: symbolPath,
			Content:    b.file.Content[first.StartByte:last.EndByte],
			ParentID:   parentID,
			Calls:      collectCalls(first, Node{}, b.lang, true),
			Imports:    b.imports,
			Language:   b.lang.ID,
		})
		run = nil
	}
	for _, c := range top {
		if isImportNode(c, b.lang) || b.lang.IsDefinition(c.Kind) || b.lang.IsComment(c.Kind) || (doc.ok && doc.node.Kind != "" && c.StartByte == doc.node.StartByte && c.EndByte == doc.node.EndByte) {
			flush()
			continue
		}
		run = append(run, c)
	}
	flush()
}

// definition describes one class or function definition.
type definition struct {
	// node is the definition itself.
	node Node
	// span is the node whose source range covers the definition.
	span Node
	// kind is model.ChunkFunc or model.ChunkClass.
	kind string
	// name is the defined name.
	name string
	// doc is the documentation of the definition.
	doc docInfo
}

// addDefinitionChunks walks a scope body and adds a chunk for every class and
// function definition it contains, recursing into nested definitions.
//
// scopePath is the list of enclosing names, used to build the symbol path;
// parentID is the id of the chunk that owns the scope.
func (b *chunkBuilder) addDefinitionChunks(body Node, scopePath []string, parentID string) {
	for _, def := range definitionsOf(body, b.lang) {
		symbolPath := "module"
		for _, s := range scopePath {
			symbolPath += "." + s
		}
		symbolPath += "." + def.name

		startByte := def.span.StartByte
		startLine := def.span.StartLine
		if def.doc.ok && def.doc.node.Kind != "" && def.doc.node.StartByte < startByte {
			startByte = def.doc.node.StartByte
			startLine = def.doc.node.StartLine
		}

		id := chunkID(b.file.Path, startLine, symbolPath)
		b.chunks = append(b.chunks, model.Chunk{
			ID:         id,
			File:       b.file.Path,
			StartLine:  startLine,
			EndLine:    def.span.EndLine,
			Type:       def.kind,
			SymbolPath: symbolPath,
			Docstring:  def.doc.text,
			Content:    b.file.Content[startByte:def.span.EndByte],
			ParentID:   parentID,
			Calls:      collectCalls(def.span, def.node, b.lang, false),
			Imports:    b.imports,
			Language:   b.lang.ID,
		})

		inner, ok := def.node.Child("body")
		if !ok {
			continue
		}
		next := make([]string, 0, len(scopePath)+1)
		next = append(next, scopePath...)
		next = append(next, def.name)
		b.addDefinitionChunks(inner, next, id)
	}
}

// definitionsOf returns the class and function definitions directly contained
// in a scope body.
func definitionsOf(body Node, lang *Language) []definition {
	var out []definition
	for i, c := range body.Children {
		switch {
		case lang.IsFunction(c.Kind):
			out = append(out, definition{
				node: c,
				span: c,
				kind: model.ChunkFunc,
				name: definitionName(c),
				doc:  defDoc(body.Children, i, c, lang),
			})
		case lang.IsClass(c.Kind):
			out = append(out, definition{
				node: c,
				span: c,
				kind: model.ChunkClass,
				name: definitionName(c),
				doc:  defDoc(body.Children, i, c, lang),
			})
		case lang.IsWrapper(c.Kind):
			inner, ok := wrapperInner(c)
			if !ok {
				continue
			}
			switch {
			case lang.IsFunction(inner.Kind):
				out = append(out, definition{
					node: inner,
					span: c, // include the decorators in the chunk range
					kind: model.ChunkFunc,
					name: definitionName(inner),
					doc:  defDoc(body.Children, i, inner, lang),
				})
			case lang.IsClass(inner.Kind):
				out = append(out, definition{
					node: inner,
					span: c,
					kind: model.ChunkClass,
					name: definitionName(inner),
					doc:  defDoc(body.Children, i, inner, lang),
				})
			}
		}
	}
	return out
}

// wrapperInner returns the definition a wrapper node holds.
//
// A decorated Python definition holds a "definition", and an exported
// TypeScript declaration holds a "declaration".
func wrapperInner(n Node) (Node, bool) {
	for _, field := range []string{"definition", "declaration"} {
		if inner, ok := n.Child(field); ok {
			return inner, true
		}
	}
	for _, c := range n.Children {
		switch c.Kind {
		case "definition", "declaration", "class", "function":
			return c, true
		}
	}
	return Node{}, false
}

// docInfo is the documentation of a module or a definition.
type docInfo struct {
	// node is the node that spans the documentation, when there is one.
	node Node
	// text is the documentation text.
	text string
	// ok reports whether there is any documentation.
	ok bool
}

// moduleDoc returns the documentation of a file.
//
// Python documents a module with a docstring, the other languages with a
// comment block at the top of the file.
func moduleDoc(top []Node, lang *Language) docInfo {
	if len(top) == 0 {
		return docInfo{}
	}
	if lang.doc == docPython {
		first := top[0]
		if first.Kind != "expression_statement" || len(first.Children) != 1 || first.Children[0].Kind != "string" {
			return docInfo{}
		}
		return docInfo{node: first, text: docstringText(first), ok: true}
	}
	var comments []Node
	for _, c := range top {
		if !lang.IsComment(c.Kind) {
			break
		}
		comments = append(comments, c)
	}
	if len(comments) == 0 {
		return docInfo{}
	}
	return docInfo{
		node: Node{
			Kind:      "comment",
			StartByte: comments[0].StartByte,
			EndByte:   comments[len(comments)-1].EndByte,
			StartLine: comments[0].StartLine,
			EndLine:   comments[len(comments)-1].EndLine,
		},
		text: commentText(comments),
		ok:   true,
	}
}

// defDoc returns the documentation of the definition at the given index of a
// scope body.
func defDoc(siblings []Node, index int, node Node, lang *Language) docInfo {
	if lang.doc == docPython {
		text := docstringOf(node)
		if text == "" {
			return docInfo{}
		}
		return docInfo{text: text, ok: true}
	}
	var comments []Node
	for i := index - 1; i >= 0; i-- {
		s := siblings[i]
		if !lang.IsComment(s.Kind) {
			break
		}
		comments = append([]Node{s}, comments...)
	}
	if len(comments) == 0 {
		return docInfo{}
	}
	return docInfo{
		node: Node{
			Kind:      "comment",
			StartByte: comments[0].StartByte,
			EndByte:   comments[len(comments)-1].EndByte,
			StartLine: comments[0].StartLine,
			EndLine:   comments[len(comments)-1].EndLine,
		},
		text: commentText(comments),
		ok:   true,
	}
}

// commentText renders a block of comments as plain documentation text.
func commentText(comments []Node) string {
	parts := make([]string, 0, len(comments))
	for _, c := range comments {
		parts = append(parts, strings.TrimSpace(stripCommentMarkers(c.Text)))
	}
	return strings.Join(parts, "\n")
}

// stripCommentMarkers removes the comment syntax of a line.
func stripCommentMarkers(text string) string {
	trimmed := strings.TrimSpace(text)
	for _, marker := range []string{"//", "/*", "*/", "#", "*"} {
		trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, marker))
	}
	return strings.TrimSpace(trimmed)
}

// docstringText returns the text of a docstring statement.
func docstringText(stmt Node) string {
	if len(stmt.Children) != 1 || stmt.Children[0].Kind != "string" {
		return ""
	}
	return stringContent(stmt.Children[0])
}

// docstringOf returns the docstring of a class or function definition.
func docstringOf(def Node) string {
	body, ok := def.Child("body")
	if !ok || len(body.Children) == 0 {
		return ""
	}
	first := body.Children[0]
	if first.Kind != "expression_statement" {
		return ""
	}
	return docstringText(first)
}

// stringContent returns the unquoted content of a string literal node.
func stringContent(s Node) string {
	var parts []string
	for _, c := range s.Children {
		if c.Kind == "string_content" {
			parts = append(parts, c.Text)
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, "")
	}
	return strings.Trim(s.Text, "\"'")
}

// isImportNode reports whether a node imports another module.
//
// A node only counts as an import when the language can actually name what it
// imports: Ruby, for example, uses a call node for its imports, and a call
// that does not require anything is not an import.
func isImportNode(n Node, lang *Language) bool {
	if !lang.IsImport(n.Kind) {
		return false
	}
	return len(lang.Imports(n)) > 0
}

// collectImports returns the module names imported at file level, in source
// order and without duplicates.
func collectImports(top []Node, lang *Language) []string {
	var out []string
	seen := map[string]bool{}
	add := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, name)
	}
	for _, c := range top {
		if !lang.IsImport(c.Kind) {
			continue
		}
		for _, name := range lang.Imports(c) {
			add(name)
		}
	}
	return out
}

// collectCalls returns the callee names of every call inside the node.
//
// When own is the zero Node, nested definitions are only descended into when
// includeNested is set. When own is a real definition, calls inside that
// definition are collected but nested definitions are not descended into.
func collectCalls(root Node, own Node, lang *Language, includeNested bool) []string {
	var out []string
	var walk func(n Node, isRoot bool)
	walk = func(n Node, isRoot bool) {
		if !isRoot && lang.IsDefinition(n.Kind) {
			if own.Kind == "" {
				if !includeNested {
					return
				}
			} else if n.StartByte != own.StartByte || n.EndByte != own.EndByte {
				return
			}
		}
		if lang.IsCall(n.Kind) {
			if callee := calleeOf(n); callee != "" {
				out = append(out, callee)
			}
		}
		for _, c := range n.Children {
			walk(c, false)
		}
	}
	walk(root, true)
	return out
}

// calleeOf returns the name of the function a call node invokes.
//
// The grammars disagree on where the callee lives: most put it in a
// "function" field, Ruby uses "method" with an optional "receiver", and Java
// uses "name" with an optional "object".
func calleeOf(n Node) string {
	if fn, ok := n.Child("function"); ok {
		return fn.Text
	}
	if m, ok := n.Child("method"); ok {
		if recv, ok := n.Child("receiver"); ok && recv.Text != "" {
			return recv.Text + "." + m.Text
		}
		return m.Text
	}
	if nm, ok := n.Child("name"); ok {
		if obj, ok := n.Child("object"); ok && obj.Text != "" {
			return obj.Text + "." + nm.Text
		}
		return nm.Text
	}
	if macro, ok := n.Child("macro"); ok {
		return macro.Text
	}
	return ""
}

// chunkID derives a stable chunk identifier from the file path, the start
// line and the symbol path, so that identical content always yields identical
// ids.
func chunkID(file string, startLine int, symbolPath string) string {
	h := fnv64(fmt.Sprintf("%s:%d:%s", file, startLine, symbolPath))
	return fmt.Sprintf("%s-%016x", slug(symbolPath), h)
}

// slug turns a symbol path into an identifier-safe prefix.
func slug(s string) string {
	var out strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out.WriteRune(r)
		default:
			out.WriteByte('-')
		}
	}
	return strings.Trim(out.String(), "-")
}

// fnv64 is the FNV-1a 64-bit hash used for identifier derivation.
func fnv64(s string) uint64 {
	const offset64 = 14695981039346656037
	const prime64 = 1099511628211
	var h uint64 = offset64
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime64
	}
	return h
}

// lineOf returns the 1-based line number of a byte offset in content.
func lineOf(content string, offset int) int {
	if offset > len(content) {
		offset = len(content)
	}
	if offset < 0 {
		offset = 0
	}
	return strings.Count(content[:offset], "\n") + 1
}
