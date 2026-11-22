package audit

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tlmcguire/aisai/internal/chat"
	"github.com/tlmcguire/aisai/internal/llm"
	"github.com/tlmcguire/aisai/internal/websearch"
)

// The command and web tools of the chat. They are the only tools that reach
// beyond the audit's own data, so each call is judged first (see Assess):
// read-only commands inside the workspace run at once, everything else waits
// for the user, and a few things never run.

const (
	commandTimeout   = 20 * time.Second
	maxCommandOutput = 16 << 10
)

// readOnlyCommands may run without asking: they only read, and the arguments
// are checked to stay inside the workspace.
var readOnlyCommands = map[string]bool{
	"ls": true, "cat": true, "head": true, "tail": true, "wc": true, "grep": true,
	"egrep": true, "fgrep": true, "rg": true, "find": true, "file": true, "stat": true,
	"tree": true, "pwd": true, "echo": true, "sort": true, "uniq": true, "diff": true,
	"cut": true, "nl": true, "basename": true, "dirname": true,
}

// neverCommands are refused outright, even when the user would say yes.
var neverCommands = map[string]bool{
	"sudo": true, "su": true, "doas": true, "shutdown": true, "reboot": true, "halt": true,
	"mkfs": true, "dd": true, "diskutil": true, "chmod": true, "chown": true, "kill": true,
	"killall": true, "launchctl": true, "systemctl": true, "passwd": true,
}

// findWrites are the find options that run programs or write files.
var findWrites = map[string]bool{"-exec": true, "-execdir": true, "-ok": true, "-okdir": true, "-delete": true, "-fprint": true, "-fprintf": true, "-fls": true}

// WebTools is what the chat needs to look things up online.
type WebTools = *websearch.Client

// ChatToolDefinitions returns the tools of the chat: the read-only audit tools,
// the command tool, and the web tools when web access is switched on.
func ChatToolDefinitions(web bool) []llm.Tool {
	tools := ToolDefinitions()
	tools = append(tools, llm.Tool{
		Name:        "run_command",
		Description: "Run one command in the package workspace (for example `grep -rn eval src`, `ls`, or `python3 -c ...`). There is no shell, so pipes and redirects do not work. Read-only commands such as ls, cat, grep and find run at once; anything else asks the user first. Prefer get_file and search_symbols for reading code.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{"type": "string", "description": "The command line, e.g. grep -rn subprocess ."},
			},
			"required": []string{"command"},
		},
	})
	if web {
		tools = append(tools,
			llm.Tool{
				Name:        "web_search",
				Description: "Search the web, for example for a CVE, an advisory or the documentation of a library. Returns numbered results with their addresses. Use it only when the package source cannot answer the question.",
				Parameters: map[string]any{
					"type":       "object",
					"properties": map[string]any{"query": map[string]any{"type": "string", "description": "What to search for. Do not include the user's code."}},
					"required":   []string{"query"},
				},
			},
			llm.Tool{
				Name:        "fetch_url",
				Description: "Read a web page returned by web_search and get its text.",
				Parameters: map[string]any{
					"type":       "object",
					"properties": map[string]any{"url": map[string]any{"type": "string", "description": "A http or https address"}},
					"required":   []string{"url"},
				},
			},
		)
	}
	return tools
}

// ChatTools is the tool runner of the chat: the audit tools plus commands and
// web access. It also says which calls need approval.
type ChatTools struct {
	*Tools
	web *websearch.Client
	// sandbox is where commands run.
	sandbox string
}

// NewChatTools builds the chat's tool runner. web is nil when web access is
// off.
func NewChatTools(m *Manager, auditID string, web *websearch.Client) (*ChatTools, error) {
	root, err := m.WorkspaceRoot(auditID)
	if err != nil {
		return nil, err
	}
	return &ChatTools{Tools: NewTools(m, auditID), web: web, sandbox: sandboxDir(root)}, nil
}

// splitCommand splits a command line into words, honouring single and double
// quotes. It does not interpret anything else: there is no shell.
func splitCommand(line string) ([]string, error) {
	var words []string
	var cur strings.Builder
	var quote rune
	started := false
	for _, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			started = true
		case r == ' ' || r == '\t' || r == '\n':
			if started || cur.Len() > 0 {
				words = append(words, cur.String())
				cur.Reset()
				started = false
			}
		default:
			cur.WriteRune(r)
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote in %q", line)
	}
	if started || cur.Len() > 0 {
		words = append(words, cur.String())
	}
	return words, nil
}

// escapesWorkspace reports whether an argument names a place outside the
// workspace: an absolute path, a home directory, or a ".." step.
func escapesWorkspace(arg string) bool {
	if strings.HasPrefix(arg, "-") && !strings.Contains(arg, "/") {
		return false
	}
	if filepath.IsAbs(arg) || strings.HasPrefix(arg, "~") {
		return true
	}
	for _, part := range strings.FieldsFunc(arg, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return true
		}
	}
	return false
}

// Assess judges a tool call: it runs at once, needs the user's approval, or
// never runs.
func (t *ChatTools) Assess(name string, args map[string]any) chat.Risk {
	switch name {
	case "run_command":
		line := argString(args, "command")
		words, err := splitCommand(line)
		if err != nil || len(words) == 0 {
			return chat.Risk{Level: "deny", Summary: "the command is empty or malformed"}
		}
		program := filepath.Base(words[0])
		if neverCommands[program] {
			return chat.Risk{Level: "deny", Summary: fmt.Sprintf("%s is never allowed", program)}
		}
		if readOnlyCommands[words[0]] {
			safe := true
			for _, w := range words[1:] {
				if escapesWorkspace(w) || findWrites[w] {
					safe = false
				}
			}
			if safe {
				return chat.Risk{Level: "safe", Summary: line}
			}
			return chat.Risk{Level: "ask", Summary: fmt.Sprintf("Run `%s`. It reads or writes outside the package workspace, or can run programs.", line)}
		}
		return chat.Risk{Level: "ask", Summary: fmt.Sprintf("Run `%s` on your computer. This executes a program, and it may execute code from the audited package.", line)}
	case "web_search":
		return chat.Risk{Level: "ask", Group: "web", Summary: fmt.Sprintf("Search the web for %q. The search text leaves your computer.", argString(args, "query"))}
	case "fetch_url":
		return chat.Risk{Level: "ask", Group: "web", Summary: fmt.Sprintf("Fetch %s. This contacts a server on the internet.", argString(args, "url"))}
	}
	return chat.Risk{Level: "safe"}
}

// Run executes a tool call.
func (t *ChatTools) Run(ctx context.Context, name string, args map[string]any) (string, error) {
	switch name {
	case "run_command":
		return t.runCommand(ctx, argString(args, "command"))
	case "web_search":
		if t.web == nil {
			return "", fmt.Errorf("web access is switched off in Settings")
		}
		results, err := t.web.Search(ctx, argString(args, "query"))
		if err != nil {
			return "", err
		}
		if len(results) == 0 {
			return "No results.", nil
		}
		var b strings.Builder
		for i, r := range results {
			fmt.Fprintf(&b, "[%d] %s\n%s\n%s\n\n", i+1, r.Title, r.URL, r.Snippet)
		}
		return b.String(), nil
	case "fetch_url":
		if t.web == nil {
			return "", fmt.Errorf("web access is switched off in Settings")
		}
		return t.web.Fetch(ctx, argString(args, "url"))
	}
	return t.Tools.Run(ctx, name, args)
}

func (t *ChatTools) runCommand(ctx context.Context, line string) (string, error) {
	words, err := splitCommand(line)
	if err != nil || len(words) == 0 {
		return "", fmt.Errorf("empty or malformed command")
	}
	if err := os.MkdirAll(t.sandbox, 0o755); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, words[0], words[1:]...) //nolint:gosec // gated by Assess: allowlist or the user's approval
	cmd.Dir = t.sandbox
	// A small environment: no credentials, no API keys of the app.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "LANG=C.UTF-8", "HOME=" + t.sandbox, "PYTHONDONTWRITEBYTECODE=1"}
	var out bytes.Buffer
	cmd.Stdout = &limitedWriter{w: &out, left: maxCommandOutput}
	cmd.Stderr = cmd.Stdout
	runErr := cmd.Run()
	text := strings.TrimRight(out.String(), "\n")
	if ctx.Err() == context.DeadlineExceeded {
		return text, fmt.Errorf("the command was stopped after %s", commandTimeout)
	}
	if runErr != nil {
		if exit, ok := runErr.(*exec.ExitError); ok {
			return fmt.Sprintf("%s\n(exit status %d)", text, exit.ExitCode()), nil
		}
		return "", runErr
	}
	if text == "" {
		return "(no output)", nil
	}
	return text, nil
}

// limitedWriter keeps the first left bytes and drops the rest.
type limitedWriter struct {
	w    *bytes.Buffer
	left int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	n := len(p)
	if l.left > 0 {
		if len(p) > l.left {
			p = p[:l.left]
		}
		l.w.Write(p)
		l.left -= len(p)
		if l.left == 0 {
			l.w.WriteString("\n… (output truncated)")
		}
	}
	return n, nil
}
