package audit

import (
	"context"
	"strings"
	"testing"
)

func TestAssessCommands(t *testing.T) {
	tools := &ChatTools{sandbox: t.TempDir()}
	cases := []struct {
		cmd  string
		want string
	}{
		{"grep -rn eval src", "safe"},
		{"ls", "safe"},
		{"cat ../../etc/passwd", "ask"},
		{"cat /etc/passwd", "ask"},
		{"find . -name x -exec rm {} ;", "ask"},
		{"python3 setup.py install", "ask"},
		{"sudo rm -rf /", "deny"},
		{"/usr/bin/sudo ls", "deny"},
		{"", "deny"},
		{"grep 'unterminated", "deny"},
	}
	for _, c := range cases {
		got := tools.Assess("run_command", map[string]any{"command": c.cmd}).Level
		if got != c.want {
			t.Errorf("%q: got %s, want %s", c.cmd, got, c.want)
		}
	}
	if tools.Assess("web_search", map[string]any{"query": "x"}).Level != "ask" {
		t.Error("web search must ask")
	}
	if tools.Assess("get_file", nil).Level != "safe" {
		t.Error("reading a file is safe")
	}
}

func TestRunCommandStaysInSandboxAndCapsOutput(t *testing.T) {
	dir := t.TempDir()
	tools := &ChatTools{sandbox: dir}
	out, err := tools.Run(context.Background(), "run_command", map[string]any{"command": "pwd"})
	if err != nil || !strings.Contains(out, dir[len(dir)-8:]) {
		t.Fatalf("pwd = %q, %v", out, err)
	}
	out, err = tools.Run(context.Background(), "run_command", map[string]any{"command": "ls missing-file"})
	if err != nil || !strings.Contains(out, "exit status") {
		t.Fatalf("a failing command reports its status: %q, %v", out, err)
	}
}
