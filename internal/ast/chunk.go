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
	imports []string
	chunks  []model.Chunk
}

// buildChunks derives the semantic chunks of a parsed file.
//
// Empty files and files that contain nothing but comments produce no chunks.
// Files below roughly 400 tokens produce exactly one chunk of type
// model.ChunkFile. Larger files are split into a module chunk (imports and
// the module docstring), block chunks (contiguous top-level statements) and
// class/function chunks for every definition, including nested ones.
func buildChunks(f *File) []model.Chunk {
	if f == nil {
		return nil
	}
	root := f.root
	if len(root.Children) == 0 {
		return nil
	}

	b := &chunkBuilder{file: f}
	b.imports = collectImports(root.Children)

	if estimateTokens(f.Content) < smallFileTokens {
		b.chunks = append(b.chunks, b.fileChunk())
		return b.chunks
	}

	top := root.Children
	doc, hasDoc := moduleDocstring(top)
	moduleID := b.addModuleChunk(top, doc, hasDoc)
	b.addBlockChunks(top, doc, hasDoc, moduleID)
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
	docStmt, hasDoc := moduleDocstring(f.root.Children)
	docText := ""
	if hasDoc {
		docText = docstringText(docStmt)
	}
	return model.Chunk{
		ID:         chunkID(f.Path, start, "module"),
		File:       f.Path,
		StartLine:  start,
		EndLine:    end,
		Type:       model.ChunkFile,
		SymbolPath: "module",
		Docstring:  docText,
		Content:    f.Content,
		Calls:      collectCalls(f.root, Node{}, true),
		Imports:    b.imports,
		Language:   LanguagePython,
	}
}

// addModuleChunk adds the chunk covering the file's imports and module
// docstring, and returns its id. It returns an empty id when the file has
// neither.
func (b *chunkBuilder) addModuleChunk(top []Node, doc Node, hasDoc bool) string {
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
	if hasDoc {
		consider(doc)
	}
	for _, c := range top {
		if isImportKind(c.Kind) {
			consider(c)
		}
	}
	if !have {
		return ""
	}
	startLine := lineOf(b.file.Content, start)
	endLine := lineOf(b.file.Content, end)
	docText := ""
	if hasDoc {
		docText = docstringText(doc)
	}
	id := chunkID(b.file.Path, startLine, "module")
	b.chunks = append(b.chunks, model.Chunk{
		ID:         id,
		File:       b.file.Path,
		StartLine:  startLine,
		EndLine:    endLine,
		Type:       model.ChunkModule,
		SymbolPath: "module",
		Docstring:  docText,
		Content:    b.file.Content[start:end],
		Imports:    b.imports,
		Language:   LanguagePython,
	})
	return id
}

// addBlockChunks adds one chunk per contiguous run of top-level statements
// that are not imports, not the module docstring and not part of a definition.
func (b *chunkBuilder) addBlockChunks(top []Node, doc Node, hasDoc bool, parentID string) {
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
			Calls:      collectCalls(first, Node{}, true),
			Imports:    b.imports,
			Language:   LanguagePython,
		})
		run = nil
	}
	for _, c := range top {
		if isImportKind(c.Kind) || isDefinitionKind(c.Kind) || (hasDoc && c.StartByte == doc.StartByte && c.EndByte == doc.EndByte) {
			flush()
			continue
		}
		run = append(run, c)
	}
	flush()
}

// definition describes one class or function definition.
type definition struct {
	// node is the definition itself (function_definition or class_definition).
	node Node
	// span is the node whose source range covers the definition, including
	// decorators when the definition is decorated.
	span Node
	// kind is model.ChunkFunc or model.ChunkClass.
	kind string
	// name is the defined name.
	name string
}

// addDefinitionChunks walks a scope body and adds a chunk for every class and
// function definition it contains, recursing into nested definitions.
//
// scopePath is the list of enclosing names, used to build the symbol path;
// parentID is the id of the chunk that owns the scope.
func (b *chunkBuilder) addDefinitionChunks(body Node, scopePath []string, parentID string) {
	for _, def := range definitionsOf(body) {
		symbolPath := "module"
		for _, s := range scopePath {
			symbolPath += "." + s
		}
		symbolPath += "." + def.name

		id := chunkID(b.file.Path, def.span.StartLine, symbolPath)
		b.chunks = append(b.chunks, model.Chunk{
			ID:         id,
			File:       b.file.Path,
			StartLine:  def.span.StartLine,
			EndLine:    def.span.EndLine,
			Type:       def.kind,
			SymbolPath: symbolPath,
			Docstring:  docstringOf(def.node),
			Content:    b.file.Content[def.span.StartByte:def.span.EndByte],
			ParentID:   parentID,
			Calls:      collectCalls(def.span, def.node, false),
			Imports:    b.imports,
			Language:   LanguagePython,
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
func definitionsOf(body Node) []definition {
	var out []definition
	for _, c := range body.Children {
		switch c.Kind {
		case "function_definition":
			out = append(out, definition{node: c, span: c, kind: model.ChunkFunc, name: definitionName(c)})
		case "class_definition":
			out = append(out, definition{node: c, span: c, kind: model.ChunkClass, name: definitionName(c)})
		case "decorated_definition":
			inner, ok := c.Child("definition")
			if !ok {
				continue
			}
			for _, d := range definitionsOf(inner) {
				d.span = c // include the decorators in the chunk range
				out = append(out, d)
			}
		}
	}
	return out
}

// definitionName returns the name defined by a class or function node.
func definitionName(n Node) string {
	if name, ok := n.Child("name"); ok {
		return name.Text
	}
	return ""
}

// isDefinitionKind reports whether a node kind introduces a new scope.
func isDefinitionKind(kind string) bool {
	switch kind {
	case "function_definition", "class_definition", "decorated_definition":
		return true
	}
	return false
}

// isImportKind reports whether a node kind is an import statement.
func isImportKind(kind string) bool {
	switch kind {
	case "import_statement", "import_from_statement", "future_import_statement":
		return true
	}
	return false
}

// moduleDocstring returns the module docstring statement, if the first
// statement of the module is a string literal.
func moduleDocstring(top []Node) (Node, bool) {
	if len(top) == 0 {
		return Node{}, false
	}
	first := top[0]
	if first.Kind != "expression_statement" || len(first.Children) != 1 || first.Children[0].Kind != "string" {
		return Node{}, false
	}
	return first, true
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
	return strings.Trim(s.Text, `"'`)
}

// collectImports returns the module names imported at file level, in source
// order and without duplicates.
func collectImports(top []Node) []string {
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
		switch c.Kind {
		case "import_statement":
			for _, ch := range c.Children {
				switch ch.Kind {
				case "dotted_name":
					add(ch.Text)
				case "aliased_import":
					if n, ok := ch.Child("name"); ok {
						add(n.Text)
					}
				}
			}
		case "import_from_statement", "future_import_statement":
			if m, ok := c.Child("module_name"); ok {
				if m.Kind == "dotted_name" {
					add(m.Text)
				}
			}
		}
	}
	return out
}

// collectCalls returns the callee names of every call inside the node.
//
// When own is the zero Node, nested definitions are only descended into when
// includeNested is set. When own is a real definition, calls inside that
// definition are collected but nested definitions are not descended into.
func collectCalls(root Node, own Node, includeNested bool) []string {
	var out []string
	var walk func(n Node, isRoot bool)
	walk = func(n Node, isRoot bool) {
		if !isRoot && isDefinitionKind(n.Kind) {
			if own.Kind == "" {
				if !includeNested {
					return
				}
			} else if n.StartByte != own.StartByte || n.EndByte != own.EndByte {
				return
			}
		}
		if n.Kind == "call" {
			if fn, ok := n.Child("function"); ok {
				out = append(out, fn.Text)
			}
		}
		for _, c := range n.Children {
			walk(c, false)
		}
	}
	walk(root, true)
	return out
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
