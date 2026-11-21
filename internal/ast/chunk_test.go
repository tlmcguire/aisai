package ast

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tlmcguire/aisai/internal/model"
)

// paddedModule returns a module whose first line is a long docstring, so the
// file is above the small-file threshold and gets split into fine chunks.
// The line numbering of the remaining lines is unaffected.
func paddedModule(body string) string {
	return `"""Module docstring. ` + strings.Repeat("padding ", 220) + `"""` + "\n" + body
}

// chunkSummary is the comparable shape of a chunk.
type chunkSummary struct {
	Type       string
	SymbolPath string
	StartLine  int
	EndLine    int
}

func summarize(chunks []model.Chunk) []chunkSummary {
	out := make([]chunkSummary, 0, len(chunks))
	for _, c := range chunks {
		out = append(out, chunkSummary{c.Type, c.SymbolPath, c.StartLine, c.EndLine})
	}
	return out
}

func TestChunkingSplit(t *testing.T) {
	body := strings.Join([]string{
		"import os",        // line 2
		"import sys",       // line 3
		"",                 // line 4
		"CONST = 1",        // line 5
		"",                 // line 6
		"def top():",       // line 7
		`    """Doc."""`,   // line 8
		"    return 1",     // line 9
		"",                 // line 10
		"class C:",         // line 11
		`    """Doc."""`,   // line 12
		"",                 // line 13
		"    def m(self):", // line 14
		"        return 1", // line 15
	}, "\n")

	f, err := ParseString("pkg/mod.py", paddedModule(body))
	if err != nil {
		t.Fatalf("ParseString: %v", err)
	}

	got := summarize(f.Chunks)
	want := []chunkSummary{
		{model.ChunkModule, "module", 1, 3},
		{model.ChunkBlock, "module", 5, 5},
		{model.ChunkFunc, "module.top", 7, 9},
		{model.ChunkClass, "module.C", 11, 15},
		{model.ChunkFunc, "module.C.m", 14, 15},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d chunks, want %d:\ngot  %+v\nwant %+v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("chunk %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestChunkingParentAndMetadata(t *testing.T) {
	body := strings.Join([]string{
		"import os",
		"",
		"def top():",
		"    return os.getcwd()",
		"",
		"class C:",
		"    def m(self):",
		"        return 1",
	}, "\n")

	f, err := ParseString("pkg/mod.py", paddedModule(body))
	if err != nil {
		t.Fatalf("ParseString: %v", err)
	}

	byPath := map[string]model.Chunk{}
	for _, c := range f.Chunks {
		byPath[c.SymbolPath] = c
	}

	module, ok := byPath["module"]
	if !ok {
		t.Fatal("no module chunk")
	}
	if module.Type != model.ChunkModule {
		t.Errorf("module chunk type = %q, want %q", module.Type, model.ChunkModule)
	}
	if len(module.Imports) != 1 || module.Imports[0] != "os" {
		t.Errorf("module imports = %v, want [os]", module.Imports)
	}
	if !strings.HasPrefix(module.Docstring, "Module docstring.") {
		t.Errorf("module docstring = %q", module.Docstring)
	}
	if module.ParentID != "" {
		t.Errorf("module chunk parent = %q, want empty", module.ParentID)
	}

	top, ok := byPath["module.top"]
	if !ok {
		t.Fatal("no module.top chunk")
	}
	if top.ParentID != module.ID {
		t.Errorf("module.top parent = %q, want %q", top.ParentID, module.ID)
	}
	if len(top.Calls) != 1 || top.Calls[0] != "os.getcwd" {
		t.Errorf("module.top calls = %v, want [os.getcwd]", top.Calls)
	}
	if len(top.Imports) != 1 || top.Imports[0] != "os" {
		t.Errorf("module.top imports = %v, want [os]", top.Imports)
	}
	if top.Content != "def top():\n    return os.getcwd()" {
		t.Errorf("module.top content = %q", top.Content)
	}

	class, ok := byPath["module.C"]
	if !ok {
		t.Fatal("no module.C chunk")
	}
	if class.ParentID != module.ID {
		t.Errorf("module.C parent = %q, want %q", class.ParentID, module.ID)
	}

	method, ok := byPath["module.C.m"]
	if !ok {
		t.Fatal("no module.C.m chunk")
	}
	if method.ParentID != class.ID {
		t.Errorf("module.C.m parent = %q, want %q", method.ParentID, class.ID)
	}
}

func TestChunkingSmallFileIsOneFileChunk(t *testing.T) {
	f, err := ParseString("pkg/small.py", "import os\n\ndef f():\n    return os.getcwd()\n")
	if err != nil {
		t.Fatalf("ParseString: %v", err)
	}
	if len(f.Chunks) != 1 {
		t.Fatalf("got %d chunks, want 1", len(f.Chunks))
	}
	c := f.Chunks[0]
	if c.Type != model.ChunkFile {
		t.Errorf("chunk type = %q, want %q", c.Type, model.ChunkFile)
	}
	if c.StartLine != 1 {
		t.Errorf("chunk start line = %d, want 1", c.StartLine)
	}
	if c.Content != f.Content {
		t.Error("file chunk content does not match the file content")
	}
}

func TestChunkingEmptyAndCommentOnly(t *testing.T) {
	for name, src := range map[string]string{
		"empty":       "",
		"whitespace":  "   \n\t\n",
		"comments":    "# just a comment\n# and another\n",
		"doc comment": "#!/usr/bin/env python\n",
	} {
		f, err := ParseString("pkg/x.py", src)
		if err != nil {
			t.Fatalf("%s: ParseString: %v", name, err)
		}
		if len(f.Chunks) != 0 {
			t.Errorf("%s: got %d chunks, want 0", name, len(f.Chunks))
		}
	}
}

func TestChunkIDsAreStable(t *testing.T) {
	body := "import os\n\ndef f():\n    return 1\n"
	src := paddedModule(body)

	first, err := ParseString("pkg/mod.py", src)
	if err != nil {
		t.Fatalf("ParseString: %v", err)
	}
	second, err := ParseString("pkg/mod.py", src)
	if err != nil {
		t.Fatalf("ParseString: %v", err)
	}
	if len(first.Chunks) != len(second.Chunks) {
		t.Fatalf("chunk count differs between runs: %d vs %d", len(first.Chunks), len(second.Chunks))
	}
	for i := range first.Chunks {
		if first.Chunks[i].ID != second.Chunks[i].ID {
			t.Errorf("chunk %d id differs between runs: %q vs %q", i, first.Chunks[i].ID, second.Chunks[i].ID)
		}
	}

	// A different file path must produce different ids.
	other, err := ParseString("pkg/other.py", src)
	if err != nil {
		t.Fatalf("ParseString: %v", err)
	}
	same := true
	for i := range first.Chunks {
		if first.Chunks[i].ID != other.Chunks[i].ID {
			same = false
		}
	}
	if same {
		t.Error("chunk ids did not change when the file path changed")
	}
}

func TestParseSyntaxErrorReturnsRecoverableFile(t *testing.T) {
	src := "import os\n\ndef broken(:\n    return 1\n"
	f, err := ParseString("pkg/broken.py", src)
	if err == nil {
		t.Fatal("expected an error for a syntax error")
	}
	if f == nil {
		t.Fatal("expected a recoverable file alongside the error")
	}
	if len(f.Nodes) == 0 {
		t.Error("expected recoverable AST nodes")
	}
	if f.Path != "pkg/broken.py" {
		t.Errorf("path = %q, want pkg/broken.py", f.Path)
	}
	if f.Language != LanguagePython {
		t.Errorf("language = %q, want %q", f.Language, LanguagePython)
	}
}

func TestParseRejectsNonUTF8(t *testing.T) {
	f, err := ParseString("pkg/bad.py", "x = 1\n\xff\xfe\n")
	if err == nil {
		t.Fatal("expected an error for non-UTF-8 input")
	}
	if f != nil {
		t.Error("expected a nil file for non-UTF-8 input")
	}
	if !strings.Contains(err.Error(), "UTF-8") {
		t.Errorf("error %q does not mention UTF-8", err)
	}
}

func TestParseFromDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mod.py")
	if err := os.WriteFile(path, []byte("import os\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if f.Content != "import os\n" {
		t.Errorf("content = %q", f.Content)
	}
	if f.Path != filepath.ToSlash(path) {
		t.Errorf("path = %q, want %q", f.Path, filepath.ToSlash(path))
	}
}

func TestParseNormalizesRelativePath(t *testing.T) {
	f, err := ParseString("./pkg/mod.py", "import os\n")
	if err != nil {
		t.Fatalf("ParseString: %v", err)
	}
	if f.Path != "pkg/mod.py" {
		t.Errorf("path = %q, want pkg/mod.py", f.Path)
	}
}

func TestParseMissingFile(t *testing.T) {
	if _, err := Parse(filepath.Join(t.TempDir(), "nope.py")); err == nil {
		t.Fatal("expected an error for a missing file")
	}
}

func TestASTNodesAndEdges(t *testing.T) {
	f, err := ParseString("pkg/mod.py", "import os\n\ndef f():\n    return 1\n")
	if err != nil {
		t.Fatalf("ParseString: %v", err)
	}
	if len(f.Nodes) == 0 {
		t.Fatal("no AST nodes")
	}
	if len(f.Edges) != len(f.Nodes)-1 {
		t.Errorf("got %d edges for %d nodes, want %d", len(f.Edges), len(f.Nodes), len(f.Nodes)-1)
	}
	ids := map[string]bool{}
	for _, n := range f.Nodes {
		if n.ID == "" {
			t.Error("node with empty id")
		}
		if ids[n.ID] {
			t.Errorf("duplicate node id %q", n.ID)
		}
		ids[n.ID] = true
	}
	found := false
	for _, n := range f.Nodes {
		if n.Type == "function_definition" && n.Symbol == "f" {
			found = true
		}
	}
	if !found {
		t.Error("no function_definition node for f")
	}
}

func TestNodeHelpers(t *testing.T) {
	f, err := ParseString("pkg/mod.py", "import os\n\ndef f():\n    return os.getcwd()\n")
	if err != nil {
		t.Fatalf("ParseString: %v", err)
	}
	root := f.Root()
	if root.Kind != "module" {
		t.Errorf("root kind = %q, want module", root.Kind)
	}
	fn, ok := root.Find("function_definition")
	if !ok {
		t.Fatal("no function_definition found")
	}
	if fn.Text != "def f():\n    return os.getcwd()" {
		t.Errorf("function text = %q", fn.Text)
	}
	if !fn.HasDescendant("call") {
		t.Error("expected a call descendant")
	}
	name, ok := fn.Child("name")
	if !ok {
		t.Fatal("function has no name child")
	}
	if name.Text != "f" {
		t.Errorf("function name = %q, want f", name.Text)
	}
	attr, ok := root.Find("attribute")
	if !ok {
		t.Fatal("no attribute found")
	}
	if attr.BaseName() != "getcwd" {
		t.Errorf("base name = %q, want getcwd", attr.BaseName())
	}
}
