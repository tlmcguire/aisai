package ast

import (
	"path/filepath"
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"
	csgrammar "github.com/tree-sitter/tree-sitter-c-sharp/bindings/go"
	cgrammar "github.com/tree-sitter/tree-sitter-c/bindings/go"
	gogrammar "github.com/tree-sitter/tree-sitter-go/bindings/go"
	javagrammar "github.com/tree-sitter/tree-sitter-java/bindings/go"
	jsgrammar "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	pygrammar "github.com/tree-sitter/tree-sitter-python/bindings/go"
	rubygammar "github.com/tree-sitter/tree-sitter-ruby/bindings/go"
	rustgrammar "github.com/tree-sitter/tree-sitter-rust/bindings/go"
	tsgrammar "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

// The language identifiers recorded on parsed files and their chunks.
const (
	// LanguagePython is the Python language.
	LanguagePython = "python"
	// LanguageJavaScript is the JavaScript language.
	LanguageJavaScript = "javascript"
	// LanguageTypeScript is the TypeScript language.
	LanguageTypeScript = "typescript"
	// LanguageTSX is the TypeScript-with-JSX dialect.
	LanguageTSX = "tsx"
	// LanguageGo is the Go language.
	LanguageGo = "go"
	// LanguageRust is the Rust language.
	LanguageRust = "rust"
	// LanguageJava is the Java language.
	LanguageJava = "java"
	// LanguageCSharp is the C# language.
	LanguageCSharp = "csharp"
	// LanguageRuby is the Ruby language.
	LanguageRuby = "ruby"
	// LanguageC is the C language.
	LanguageC = "c"
)

// docStyle selects how the documentation of a definition is found.
type docStyle int

const (
	// docPython means the documentation is the docstring at the start of
	// the body of a definition.
	docPython docStyle = iota + 1
	// docComment means the documentation is the comment block that
	// directly precedes the definition.
	docComment
)

// Language describes a source language AISAI can parse.
//
// A Language ties together the Tree-sitter grammar of a language with the
// node kinds that matter for chunking: which nodes define a scope, which
// ones import another module and which ones are a call. Keeping that
// knowledge next to the grammar is what lets the chunker work for every
// language instead of only for Python.
type Language struct {
	// ID is the stable identifier of the language, for example "python".
	// It is recorded on parsed files and on their chunks.
	ID string
	// DisplayName is the human readable name of the language.
	DisplayName string
	// Extensions are the file extensions that map to this language. They
	// are lowercase and carry the leading dot.
	Extensions []string
	// Grammar returns the Tree-sitter grammar of the language.
	Grammar func() *ts.Language

	// functionKinds are the node kinds that define a function or method.
	functionKinds []string
	// classKinds are the node kinds that define a class, type or module.
	classKinds []string
	// importKinds are the node kinds that import another module.
	importKinds []string
	// callKinds are the node kinds that represent a call expression.
	callKinds []string
	// importNames extracts the imported module names from an import node.
	importNames func(n Node) []string
	// doc selects how the documentation of a definition is found.
	doc docStyle
	// commentKinds are the node kinds that hold a comment.
	commentKinds []string
	// wrapperKinds are the node kinds that wrap a definition, for example
	// a decorated definition in Python.
	wrapperKinds []string
	// functionLabel is the word a function definition is labelled with,
	// for example "def" or "func".
	functionLabel string
	// classLabel is the word a class definition is labelled with.
	classLabel string
}

// IsFunction reports whether a node kind defines a function or method.
func (l *Language) IsFunction(kind string) bool {
	return contains(l.functionKinds, kind)
}

// IsClass reports whether a node kind defines a class, type or module.
func (l *Language) IsClass(kind string) bool {
	return contains(l.classKinds, kind)
}

// IsImport reports whether a node kind imports another module.
func (l *Language) IsImport(kind string) bool {
	return contains(l.importKinds, kind)
}

// IsCall reports whether a node kind is a call expression.
func (l *Language) IsCall(kind string) bool {
	return contains(l.callKinds, kind)
}

// IsComment reports whether a node kind holds a comment.
func (l *Language) IsComment(kind string) bool {
	return contains(l.commentKinds, kind)
}

// IsWrapper reports whether a node kind wraps a definition.
func (l *Language) IsWrapper(kind string) bool {
	return contains(l.wrapperKinds, kind)
}

// IsDefinition reports whether a node kind introduces a new scope.
func (l *Language) IsDefinition(kind string) bool {
	return l.IsFunction(kind) || l.IsClass(kind) || l.IsWrapper(kind)
}

// Imports returns the module names imported by an import node.
func (l *Language) Imports(n Node) []string {
	if l.importNames == nil {
		return nil
	}
	return l.importNames(n)
}

// languageRegistry holds every language AISAI knows, in the order they are
// offered to the user.
var languageRegistry = []*Language{
	{
		ID:            LanguagePython,
		DisplayName:   "Python",
		Extensions:    []string{".py", ".pyi"},
		Grammar:       func() *ts.Language { return ts.NewLanguage(pygrammar.Language()) },
		commentKinds:  []string{"comment"},
		functionLabel: "def",
		classLabel:    "class",
		functionKinds: []string{
			"function_definition",
		},
		classKinds: []string{
			"class_definition",
		},
		importKinds: []string{
			"import_statement",
			"import_from_statement",
			"future_import_statement",
		},
		callKinds: []string{"call"},
		doc:       docPython,
		importNames: func(n Node) []string {
			var out []string
			switch n.Kind {
			case "import_statement":
				for _, ch := range n.Children {
					switch ch.Kind {
					case "dotted_name":
						out = append(out, ch.Text)
					case "aliased_import":
						if name, ok := ch.Child("name"); ok {
							out = append(out, name.Text)
						}
					}
				}
			case "import_from_statement", "future_import_statement":
				if m, ok := n.Child("module_name"); ok {
					if m.Kind == "dotted_name" {
						out = append(out, m.Text)
					}
				}
			}
			return out
		},
	},
	{
		ID:            LanguageJavaScript,
		DisplayName:   "JavaScript",
		Extensions:    []string{".js", ".jsx", ".mjs", ".cjs"},
		Grammar:       func() *ts.Language { return ts.NewLanguage(jsgrammar.Language()) },
		commentKinds:  []string{"comment"},
		wrapperKinds:  []string{"export_statement", "decorated_class_declaration"},
		functionLabel: "function",
		classLabel:    "class",
		functionKinds: []string{
			"function_declaration",
			"function_expression",
			"generator_function_declaration",
			"arrow_function",
			"method_definition",
		},
		classKinds: []string{
			"class_declaration",
			"class",
		},
		importKinds: []string{
			"import_statement",
		},
		callKinds: []string{"call_expression", "new_expression"},
		doc:       docComment,
		importNames: func(n Node) []string {
			return stringImports(n)
		},
	},
	{
		ID:            LanguageTypeScript,
		DisplayName:   "TypeScript",
		Extensions:    []string{".ts", ".mts", ".cts"},
		Grammar:       func() *ts.Language { return ts.NewLanguage(tsgrammar.LanguageTypescript()) },
		commentKinds:  []string{"comment"},
		wrapperKinds:  []string{"export_statement", "decorated_class_declaration"},
		functionLabel: "function",
		classLabel:    "class",
		functionKinds: []string{
			"function_declaration",
			"function_expression",
			"generator_function_declaration",
			"arrow_function",
			"method_definition",
			"function_signature",
			"method_signature",
			"abstract_method_signature",
		},
		classKinds: []string{
			"class_declaration",
			"class",
			"abstract_class_declaration",
			"interface_declaration",
			"enum_declaration",
			"type_alias_declaration",
		},
		importKinds: []string{
			"import_statement",
		},
		callKinds: []string{"call_expression", "new_expression"},
		doc:       docComment,
		importNames: func(n Node) []string {
			return stringImports(n)
		},
	},
	{
		ID:            LanguageTSX,
		DisplayName:   "TSX",
		Extensions:    []string{".tsx"},
		Grammar:       func() *ts.Language { return ts.NewLanguage(tsgrammar.LanguageTSX()) },
		commentKinds:  []string{"comment"},
		wrapperKinds:  []string{"export_statement", "decorated_class_declaration"},
		functionLabel: "function",
		classLabel:    "class",
		functionKinds: []string{
			"function_declaration",
			"function_expression",
			"generator_function_declaration",
			"arrow_function",
			"method_definition",
			"function_signature",
			"method_signature",
		},
		classKinds: []string{
			"class_declaration",
			"class",
			"abstract_class_declaration",
			"interface_declaration",
			"enum_declaration",
			"type_alias_declaration",
		},
		importKinds: []string{
			"import_statement",
		},
		callKinds: []string{"call_expression", "new_expression"},
		doc:       docComment,
		importNames: func(n Node) []string {
			return stringImports(n)
		},
	},
	{
		ID:            LanguageGo,
		DisplayName:   "Go",
		Extensions:    []string{".go"},
		Grammar:       func() *ts.Language { return ts.NewLanguage(gogrammar.Language()) },
		commentKinds:  []string{"comment"},
		functionLabel: "func",
		classLabel:    "type",
		functionKinds: []string{
			"function_declaration",
			"method_declaration",
			"func_literal",
		},
		classKinds: []string{
			"type_declaration",
		},
		importKinds: []string{
			"import_declaration",
		},
		callKinds: []string{"call_expression"},
		doc:       docComment,
		importNames: func(n Node) []string {
			return stringImports(n)
		},
	},
	{
		ID:            LanguageRust,
		DisplayName:   "Rust",
		Extensions:    []string{".rs"},
		Grammar:       func() *ts.Language { return ts.NewLanguage(rustgrammar.Language()) },
		commentKinds:  []string{"line_comment", "block_comment", "doc_comment"},
		functionLabel: "fn",
		classLabel:    "type",
		functionKinds: []string{
			"function_item",
			"closure_expression",
		},
		classKinds: []string{
			"struct_item",
			"enum_item",
			"trait_item",
			"impl_item",
			"mod_item",
		},
		importKinds: []string{
			"use_declaration",
		},
		callKinds: []string{"call_expression", "macro_invocation"},
		doc:       docComment,
		importNames: func(n Node) []string {
			var out []string
			for _, ch := range n.Children {
				switch ch.Kind {
				case "scoped_identifier", "identifier", "use_list", "use_as_clause", "use_wildcard":
					out = append(out, ch.Text)
				}
			}
			return out
		},
	},
	{
		ID:            LanguageJava,
		DisplayName:   "Java",
		Extensions:    []string{".java"},
		Grammar:       func() *ts.Language { return ts.NewLanguage(javagrammar.Language()) },
		commentKinds:  []string{"line_comment", "block_comment", "comment"},
		functionLabel: "method",
		classLabel:    "class",
		functionKinds: []string{
			"method_declaration",
			"constructor_declaration",
			"compact_constructor_declaration",
		},
		classKinds: []string{
			"class_declaration",
			"interface_declaration",
			"enum_declaration",
			"record_declaration",
			"annotation_type_declaration",
		},
		importKinds: []string{
			"import_declaration",
		},
		callKinds: []string{"method_invocation", "object_creation_expression"},
		doc:       docComment,
		importNames: func(n Node) []string {
			return identifierImports(n)
		},
	},
	{
		ID:            LanguageCSharp,
		DisplayName:   "C#",
		Extensions:    []string{".cs"},
		Grammar:       func() *ts.Language { return ts.NewLanguage(csgrammar.Language()) },
		commentKinds:  []string{"comment"},
		functionLabel: "method",
		classLabel:    "class",
		functionKinds: []string{
			"method_declaration",
			"constructor_declaration",
			"local_function_statement",
			"operator_declaration",
		},
		classKinds: []string{
			"class_declaration",
			"interface_declaration",
			"struct_declaration",
			"enum_declaration",
			"record_declaration",
			"record_struct_declaration",
		},
		importKinds: []string{
			"using_directive",
		},
		callKinds: []string{"invocation_expression", "object_creation_expression"},
		doc:       docComment,
		importNames: func(n Node) []string {
			return identifierImports(n)
		},
	},
	{
		ID:            LanguageRuby,
		DisplayName:   "Ruby",
		Extensions:    []string{".rb", ".rake", ".gemspec"},
		Grammar:       func() *ts.Language { return ts.NewLanguage(rubygammar.Language()) },
		commentKinds:  []string{"comment"},
		functionLabel: "def",
		classLabel:    "class",
		functionKinds: []string{
			"method",
			"singleton_method",
		},
		classKinds: []string{
			"class",
			"module",
		},
		importKinds: []string{
			"call",
		},
		callKinds: []string{"call", "method_call"},
		doc:       docComment,
		importNames: func(n Node) []string {
			// Ruby imports with "require" and "require_relative" calls.
			method, ok := n.Child("method")
			if !ok || (method.Text != "require" && method.Text != "require_relative") {
				return nil
			}
			args, ok := n.Child("arguments")
			if !ok {
				return nil
			}
			var out []string
			for _, arg := range args.Children {
				if arg.Kind == "string" || arg.Kind == "bare_string" {
					out = append(out, unquote(arg.Text))
				}
			}
			return out
		},
	},
	{
		ID:            LanguageC,
		DisplayName:   "C",
		Extensions:    []string{".c", ".h"},
		Grammar:       func() *ts.Language { return ts.NewLanguage(cgrammar.Language()) },
		commentKinds:  []string{"comment"},
		functionLabel: "function",
		classLabel:    "struct",
		functionKinds: []string{
			"function_definition",
		},
		classKinds: []string{
			"struct_specifier",
			"union_specifier",
			"enum_specifier",
		},
		importKinds: []string{
			"preproc_include",
		},
		callKinds: []string{"call_expression"},
		doc:       docComment,
		importNames: func(n Node) []string {
			return stringImports(n)
		},
	},
}

// Languages returns every language AISAI can parse.
func Languages() []*Language {
	out := make([]*Language, len(languageRegistry))
	copy(out, languageRegistry)
	return out
}

// LanguageByID returns the language with the given identifier, or nil.
func LanguageByID(id string) *Language {
	for _, l := range languageRegistry {
		if l.ID == id {
			return l
		}
	}
	return nil
}

// LanguageForPath returns the language of a file, chosen by its extension,
// or nil when the extension is not one AISAI can parse.
func LanguageForPath(path string) *Language {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == "" {
		return nil
	}
	for _, l := range languageRegistry {
		for _, candidate := range l.Extensions {
			if candidate == ext {
				return l
			}
		}
	}
	return nil
}

// IsSourceFile reports whether a file is written in a language AISAI can
// parse.
func IsSourceFile(path string) bool {
	return LanguageForPath(path) != nil
}

// SupportedExtensions returns the file extensions AISAI can parse.
func SupportedExtensions() []string {
	var out []string
	for _, l := range languageRegistry {
		out = append(out, l.Extensions...)
	}
	return out
}

// stringImports returns the module names of an import node whose module is
// written as a string, which is how JavaScript, Go and C import.
func stringImports(n Node) []string {
	if s, ok := findString(n, 0); ok {
		return []string{unquote(s.Text)}
	}
	return nil
}

// findString searches a subtree for the first string literal.
func findString(n Node, depth int) (Node, bool) {
	if depth > 6 {
		return Node{}, false
	}
	switch n.Kind {
	case "string", "string_literal", "interpreted_string_literal", "raw_string_literal", "system_lib_string":
		return n, true
	}
	for _, c := range n.Children {
		if found, ok := findString(c, depth+1); ok {
			return found, true
		}
	}
	return Node{}, false
}

// identifierImports returns the module names of an import node whose module is
// written as an identifier, which is how Java and C# import.
func identifierImports(n Node) []string {
	var out []string
	var walk func(node Node, depth int)
	walk = func(node Node, depth int) {
		if depth > 4 {
			return
		}
		for _, ch := range node.Children {
			switch ch.Kind {
			case "identifier", "scoped_identifier", "qualified_name", "name":
				out = append(out, ch.Text)
				continue
			}
			walk(ch, depth+1)
		}
	}
	walk(n, 0)
	return out
}

// unquote removes the surrounding quotes or angle brackets of a string
// literal. C writes its system headers as <stdio.h>.
func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		first := s[0]
		last := s[len(s)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			return s[1 : len(s)-1]
		}
		if first == '<' && last == '>' {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// contains reports whether a slice holds a value.
func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
