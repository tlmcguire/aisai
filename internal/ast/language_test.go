package ast

import (
	"strings"
	"testing"

	"github.com/tlmcguire/aisai/internal/model"
)

// pad returns the source with a long trailing comment, so that the file is
// big enough to be split into semantic chunks instead of staying one file
// chunk.
func pad(marker, src string) string {
	return src + "\n" + marker + " " + strings.Repeat("x", 2000) + "\n"
}

// TestParseEachLanguage parses a snippet in every supported language and
// checks that the language is recorded, that the tree has nodes and that the
// file produces chunks.
func TestParseEachLanguage(t *testing.T) {
	sources := map[string]string{
		LanguagePython:     "import os\n\n\ndef run(path):\n    \"\"\"Run it.\"\"\"\n    return os.getcwd()\n",
		LanguageJavaScript: "// loads the module\nimport { readFile } from 'fs';\n\nfunction read(path) {\n  return readFile(path);\n}\n",
		LanguageTypeScript: "import { readFile } from 'fs';\n\nexport function read(path: string): string {\n  return path;\n}\n",
		LanguageTSX:        "export function View() {\n  return null;\n}\n",
		LanguageGo:         "package main\n\nimport \"fmt\"\n\nfunc Hello() {\n\tfmt.Println(\"hi\")\n}\n",
		LanguageRust:       "use std::fmt;\n\n/// Greets.\nfn hello() {\n    println!(\"hi\");\n}\n",
		LanguageJava:       "package demo;\n\nimport java.util.List;\n\nclass Demo {\n  void run() {\n    System.out.println(\"hi\");\n  }\n}\n",
		LanguageCSharp:     "using System;\n\nnamespace Demo {\n  class Runner {\n    void Run() {\n      Console.WriteLine(\"hi\");\n    }\n  }\n}\n",
		LanguageRuby:       "require 'json'\n\nclass Runner\n  def run\n    puts 'hi'\n  end\nend\n",
		LanguageCPP:        "#include <iostream>\n\nint main() {\n  std::cout << \"hi\";\n  return 0;\n}\n",
		LanguagePHP:        "<?php\nuse Foo\\Bar;\n\nfunction run($x) {\n  return strlen($x);\n}\n",
		LanguageBash:       "#!/bin/bash\nrun() {\n  echo hi\n}\nrun\n",
		LanguageScala:      "import scala.util.Try\n\nobject Demo {\n  def run(x: Int): Int = x + 1\n}\n",
		LanguageC:          "#include <stdio.h>\n\nint main(void) {\n  printf(\"hi\\n\");\n  return 0;\n}\n",
	}

	for _, lang := range Languages() {
		src, ok := sources[lang.ID]
		if !ok {
			t.Fatalf("no source snippet for %s", lang.ID)
		}
		file, err := ParseString("pkg/sample."+strings.TrimPrefix(lang.Extensions[0], "."), src)
		if err != nil {
			t.Fatalf("%s: ParseString: %v", lang.ID, err)
		}
		if file.Language != lang.ID {
			t.Errorf("%s: language = %q, want %q", lang.ID, file.Language, lang.ID)
		}
		if len(file.Nodes) == 0 {
			t.Errorf("%s: no AST nodes", lang.ID)
		}
		if len(file.Chunks) == 0 {
			t.Errorf("%s: no chunks", lang.ID)
		}
	}
}

// TestParseEachLanguageFindsDefinitions checks that the chunker produces a
// chunk for the definition of every language.
func TestParseEachLanguageFindsDefinitions(t *testing.T) {
	cases := []struct {
		lang string
		ext  string
		mark string
		src  string
		want string
	}{
		{LanguagePython, "py", "#", "def run():\n    return 1\n", "run"},
		{LanguageJavaScript, "js", "//", "function run() { return 1; }\n", "run"},
		{LanguageTypeScript, "ts", "//", "export function run(): number { return 1; }\n", "run"},
		{LanguageTSX, "tsx", "//", "export function run() { return 1; }\n", "run"},
		{LanguageGo, "go", "//", "package main\n\nfunc Run() int { return 1 }\n", "Run"},
		{LanguageRust, "rs", "//", "fn run() -> i32 { 1 }\n", "run"},
		{LanguageJava, "java", "//", "class Demo {\n  void run() {}\n}\n", "run"},
		{LanguageCSharp, "cs", "//", "class Demo {\n  void Run() {}\n}\n", "Run"},
		{LanguageRuby, "rb", "#", "class Demo\n  def run\n  end\nend\n", "run"},
		{LanguageC, "c", "//", "int run(void) { return 1; }\n", "run"},
	}

	for _, tc := range cases {
		file, err := ParseString("pkg/sample."+tc.ext, pad(tc.mark, tc.src))
		if err != nil {
			t.Fatalf("%s: ParseString: %v", tc.lang, err)
		}
		found := false
		for _, chunk := range file.Chunks {
			if strings.HasSuffix(chunk.SymbolPath, "."+tc.want) {
				found = true
				if chunk.Language != tc.lang {
					t.Errorf("%s: chunk language = %q, want %q", tc.lang, chunk.Language, tc.lang)
				}
			}
		}
		if !found {
			t.Errorf("%s: no chunk for %q, chunks: %v", tc.lang, tc.want, chunkPaths(file.Chunks))
		}
	}
}

// TestParseEachLanguageFindsImports checks that the chunker records the
// modules a file imports.
func TestParseEachLanguageFindsImports(t *testing.T) {
	cases := []struct {
		lang string
		ext  string
		mark string
		src  string
		want string
	}{
		{LanguagePython, "py", "#", "import os\n", "os"},
		{LanguageJavaScript, "js", "//", "import fs from 'fs';\n", "fs"},
		{LanguageTypeScript, "ts", "//", "import fs from 'fs';\n", "fs"},
		{LanguageGo, "go", "//", "package main\n\nimport \"fmt\"\n", "fmt"},
		{LanguageRust, "rs", "//", "use std::fmt;\n", "std::fmt"},
		{LanguageJava, "java", "//", "import java.util.List;\n", "java.util.List"},
		{LanguageCSharp, "cs", "//", "using System;\n", "System"},
		{LanguageRuby, "rb", "#", "require 'json'\n", "json"},
		{LanguageC, "c", "//", "#include <stdio.h>\n", "stdio.h"},
	}

	for _, tc := range cases {
		file, err := ParseString("pkg/sample."+tc.ext, pad(tc.mark, tc.src))
		if err != nil {
			t.Fatalf("%s: ParseString: %v", tc.lang, err)
		}
		found := false
		for _, chunk := range file.Chunks {
			for _, imp := range chunk.Imports {
				if imp == tc.want {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("%s: import %q not recorded, imports: %v", tc.lang, tc.want, file.Chunks[0].Imports)
		}
	}
}

// TestCommentDocumentationIsExtracted checks that the comment above a
// definition becomes its documentation, and that the comment itself is not
// chunked as a block of code.
func TestCommentDocumentationIsExtracted(t *testing.T) {
	src := "package demo\n\n// Greet greets the world.\nfunc Greet() {}\n"
	file, err := ParseString("pkg/sample.go", pad("//", src))
	if err != nil {
		t.Fatalf("ParseString: %v", err)
	}
	var found bool
	for _, chunk := range file.Chunks {
		if chunk.Type == model.ChunkBlock && strings.HasPrefix(strings.TrimSpace(chunk.Content), "//") {
			t.Errorf("a comment was chunked as code: %q", chunk.Content)
		}
		if chunk.SymbolPath == "module.Greet" {
			found = true
			if chunk.Docstring != "Greet greets the world." {
				t.Errorf("docstring = %q, want %q", chunk.Docstring, "Greet greets the world.")
			}
		}
	}
	if !found {
		t.Error("no chunk for the function")
	}
}

// TestParseUnsupportedLanguage rejects a file AISAI cannot parse instead of
// parsing it as Python.
func TestParseUnsupportedLanguage(t *testing.T) {
	if _, err := ParseString("pkg/notes.md", "# hello\n"); err == nil {
		t.Fatal("expected an error for an unsupported language")
	}
}

// TestLanguageForPath checks the extension mapping.
func TestLanguageForPath(t *testing.T) {
	cases := map[string]string{
		"a/b/mod.py":  LanguagePython,
		"a/b/mod.pyi": LanguagePython,
		"a/b/app.js":  LanguageJavaScript,
		"a/b/app.ts":  LanguageTypeScript,
		"a/b/app.tsx": LanguageTSX,
		"a/b/main.go": LanguageGo,
		"a/b/lib.rs":  LanguageRust,
		"a/b/A.java":  LanguageJava,
		"a/b/P.cs":    LanguageCSharp,
		"a/b/r.rb":    LanguageRuby,
		"a/b/c.c":     LanguageC,
		"a/b/r.md":    "",
	}
	for path, want := range cases {
		got := ""
		if lang := LanguageForPath(path); lang != nil {
			got = lang.ID
		}
		if got != want {
			t.Errorf("LanguageForPath(%q) = %q, want %q", path, got, want)
		}
	}
}

// TestParseIsConcurrencySafe checks that parsing can run in parallel.
func TestParseIsConcurrencySafe(t *testing.T) {
	done := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() {
			_, err := ParseString("pkg/mod.py", "def f():\n    return 1\n")
			done <- err
		}()
	}
	for i := 0; i < 8; i++ {
		if err := <-done; err != nil {
			t.Fatalf("ParseString: %v", err)
		}
	}
}

// chunkPaths lists the symbol paths of the chunks, for error messages.
func chunkPaths(chunks []model.Chunk) []string {
	out := make([]string, 0, len(chunks))
	for _, c := range chunks {
		out = append(out, c.SymbolPath)
	}
	return out
}
