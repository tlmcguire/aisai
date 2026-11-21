package scan

import (
	"strings"
	"testing"
)

func TestPromptsNameTheLanguageOfTheChunk(t *testing.T) {
	d := NewDetector(&fakeProvider{}, "m", 8192).WithFewShot([]Example{{
		CWE: "CWE-78", Vulnerable: "os.system(cmd)", Patched: "subprocess.run(['ls'])",
	}})

	chunk := goldenChunk()
	chunk.Language = "go"
	chunk.Content = "func run(cmd string) {\n\texec.Command(\"sh\", \"-c\", cmd).Run()\n}\n"

	sys := d.detectSystemPromptFor("go")
	if !strings.Contains(sys, "reviewing Go source code") {
		t.Error("the system prompt must name Go")
	}
	if strings.Contains(sys, "Python") || strings.Contains(sys, "subprocess.run") {
		t.Error("a Go prompt must not mention Python")
	}
	if strings.Contains(sys, "Example 1") {
		t.Error("the few-shot examples are Python and must not reach a Go prompt")
	}

	user := d.detectUserPrompt(chunk)
	if !strings.Contains(user, "following Go code") || !strings.Contains(user, "```go\n") {
		t.Errorf("the user prompt must name Go and fence the code as go:\n%s", user)
	}

	verify := d.verifyUserPrompt(chunk, goldenFinding())
	if !strings.Contains(verify, "```go\n") {
		t.Error("the verification prompt must fence the code as go")
	}

	if fix := FixSystemPromptFor("rust"); !strings.Contains(fix, "a Rust project") {
		t.Errorf("fix prompt = %q", fix)
	}
}

func TestPythonPromptsAreUnchanged(t *testing.T) {
	d := NewDetector(&fakeProvider{}, "m", 8192)
	if d.detectSystemPrompt() != d.detectSystemPromptFor("python") || d.detectSystemPrompt() != d.detectSystemPromptFor("") {
		t.Error("the Python prompt must not depend on how Python is spelled")
	}
	if !strings.Contains(d.detectSystemPrompt(), "reviewing Python source code") {
		t.Error("the Python prompt must name Python")
	}
	if FixSystemPrompt() != FixSystemPromptFor("python") {
		t.Error("FixSystemPrompt must be the Python prompt")
	}
}

func TestLanguageOfFile(t *testing.T) {
	cases := map[string]string{
		"pkgs/x/main.go":    "go",
		"pkgs/x/app.tsx":    "tsx",
		"pkgs/x/lib.py":     "python",
		"pkgs/x/README.txt": "python",
	}
	for path, want := range cases {
		if got := languageOfFile(path); got != want {
			t.Errorf("languageOfFile(%q) = %q, want %q", path, got, want)
		}
	}
}
