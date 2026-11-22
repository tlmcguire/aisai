package assist

import (
	"context"
	"strings"
	"testing"

	"github.com/tlmcguire/aisai/internal/llm"
)

type fake struct {
	llm.Provider
	reply string
	got   llm.CompletionRequest
}

func (f *fake) Complete(_ context.Context, req llm.CompletionRequest) (*llm.Completion, error) {
	f.got = req
	return &llm.Completion{Text: f.reply}, nil
}

func TestCleanCompletion(t *testing.T) {
	cases := []struct{ name, raw, prefix, suffix, want string }{
		{"plain", "return a + b", "def add(a, b):\n    ", "", "return a + b"},
		{"fenced", "```python\nreturn a + b\n```", "", "", "return a + b\n"},
		{"repeats the line", "    return a + b", "def add(a, b):\n    ", "", "return a + b"},
		{"empty", "  \n", "", "", ""},
		{"duplicates the suffix", "x = 1\nprint(x)", "", "print(x)\n", "x = 1"},
		{"is only the suffix", "print(x)", "", "print(x)\n", ""},
	}
	for _, c := range cases {
		if got := CleanCompletion(c.raw, c.prefix, c.suffix); strings.TrimRight(got, "\n") != strings.TrimRight(c.want, "\n") {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
	long := strings.Repeat("x\n", 100)
	if n := strings.Count(CleanCompletion(long, "", ""), "\n"); n >= maxCompletionLines {
		t.Errorf("completion not capped: %d lines", n+1)
	}
}

func TestCompleteSendsWindowsAndTreatsFileAsData(t *testing.T) {
	f := &fake{reply: "```\nfoo()\n```"}
	out, err := Complete(context.Background(), f, "m", 4096, CompleteRequest{
		Path: "a.py", Language: "python", Prefix: "x = ", Suffix: "\ny = 2\n",
	})
	if err != nil || strings.TrimSpace(out) != "foo()" {
		t.Fatalf("out=%q err=%v", out, err)
	}
	if !strings.Contains(f.got.System, "DATA") || !strings.Contains(f.got.User, "<prefix>") {
		t.Errorf("prompt does not frame the file as data: %+v", f.got)
	}
	if _, err := Complete(context.Background(), nil, "m", 1, CompleteRequest{}); err != ErrNoProvider {
		t.Errorf("want ErrNoProvider, got %v", err)
	}
}

func TestEdit(t *testing.T) {
	f := &fake{reply: "```python\nsubprocess.run(args)\n```"}
	out, err := Edit(context.Background(), f, "m", 4096, EditRequest{
		Path: "a.py", Language: "python", Selection: "os.system(cmd)\n", Instruction: "fix the injection",
		Findings: []string{"CWE-78 command injection"},
	})
	if err != nil || out != "subprocess.run(args)\n" {
		t.Fatalf("out=%q err=%v", out, err)
	}
	if !strings.Contains(f.got.User, "CWE-78") {
		t.Error("findings must reach the prompt")
	}
	if _, err := Edit(context.Background(), f, "m", 1, EditRequest{Selection: "x"}); err == nil {
		t.Error("an empty instruction must be refused")
	}
	if _, err := Edit(context.Background(), f, "m", 1, EditRequest{Selection: strings.Repeat("x", maxSelectionBytes+1), Instruction: "do"}); err == nil {
		t.Error("an oversized selection must be refused")
	}
}

func TestTitle(t *testing.T) {
	f := &fake{reply: "\"Session signing and secrets.\"\nextra"}
	if got := Title(context.Background(), f, "m", 1, "how are sessions signed?", "with an HMAC"); got != "Session signing and secrets" {
		t.Errorf("title = %q", got)
	}
	if got := Title(context.Background(), nil, "m", 1, "how does it authenticate? and what else is there to say about it", ""); !strings.HasSuffix(got, "…") || len([]rune(got)) > 41 {
		t.Errorf("fallback = %q", got)
	}
	if got := Title(context.Background(), &fake{reply: ""}, "m", 1, "", ""); got != "New chat" {
		t.Errorf("empty = %q", got)
	}
}
