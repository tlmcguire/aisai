package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/tlmcguire/aisai/internal/ast"
	"github.com/tlmcguire/aisai/internal/findings"
	"github.com/tlmcguire/aisai/internal/llm"
)

// ToolDefinitions returns the tools the chat model may call.
//
// The descriptions are what the calling model actually reads, so they state
// what the tool does and when to use it rather than just naming it.
func ToolDefinitions() []llm.Tool {
	return []llm.Tool{
		{
			Name:        "get_file",
			Description: "Read a source file from the package under audit. Use this when you need the full text of a file that a search result only summarised.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{"type": "string", "description": "Workspace-relative path, e.g. src/auth.py or src/auth.go"},
				},
				"required": []string{"path"},
			},
		},
		{
			Name:        "get_ast",
			Description: "Return the syntax tree of a source file: node types, line numbers and enclosing scopes. Use this to understand structure and call relationships.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{"type": "string", "description": "Workspace-relative path"},
				},
				"required": []string{"path"},
			},
		},
		{
			Name:        "search_symbols",
			Description: "Find functions, classes and methods by name or partial name. Use this when you know what something is called but not where it is defined.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string", "description": "Symbol name or fragment"},
				},
				"required": []string{"query"},
			},
		},
		{
			Name:        "list_findings",
			Description: "List the security findings already detected in the package under audit, with severity, confidence and CWE. Use this to ground an answer in what was actually found.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"severity": map[string]any{"type": "string", "description": "Optional severity filter"},
					"cwe":      map[string]any{"type": "string", "description": "Optional CWE filter"},
					"file":     map[string]any{"type": "string", "description": "Optional file filter"},
				},
			},
		},
		{
			Name:        "get_finding",
			Description: "Get the full detail of one finding, including its evidence and remediation.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{"type": "string", "description": "The finding id"},
				},
				"required": []string{"id"},
			},
		},
	}
}

// Tools implements chat.ToolRunner over an audit.
type Tools struct {
	manager *Manager
	auditID string
}

// NewTools creates a tool runner bound to one audit.
func NewTools(m *Manager, auditID string) *Tools {
	return &Tools{manager: m, auditID: auditID}
}

// Run executes a tool call.
//
// Every tool is confined to the audit workspace: no tool can read outside it,
// reach the network, or modify the user's files.
func (t *Tools) Run(ctx context.Context, name string, args map[string]any) (string, error) {
	switch name {
	case "get_file":
		return t.getFile(ctx, args)
	case "get_ast":
		return t.getAST(ctx, args)
	case "search_symbols":
		return t.searchSymbols(ctx, args)
	case "list_findings":
		return t.listFindings(ctx, args)
	case "get_finding":
		return t.getFinding(ctx, args)
	default:
		return "", fmt.Errorf("unknown tool %q", name)
	}
}

func argString(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}

func (t *Tools) getFile(ctx context.Context, args map[string]any) (string, error) {
	path := argString(args, "path")
	if path == "" {
		return "", fmt.Errorf("get_file requires a path")
	}
	f, err := t.manager.File(ctx, t.auditID, path)
	if err != nil {
		return "", err
	}
	return strings.Join(f.Lines, "\n"), nil
}

func (t *Tools) getAST(ctx context.Context, args map[string]any) (string, error) {
	path := argString(args, "path")
	if path == "" {
		return "", fmt.Errorf("get_ast requires a path")
	}
	a, err := t.manager.AST(ctx, t.auditID, path)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Syntax tree of %s\n", a.Path)
	for _, n := range a.Nodes {
		fmt.Fprintf(&b, "%s%s (line %d)\n", strings.Repeat("  ", n.Depth), n.Type, n.StartLine)
	}
	return b.String(), nil
}

func (t *Tools) searchSymbols(ctx context.Context, args map[string]any) (string, error) {
	query := strings.ToLower(argString(args, "query"))
	if query == "" {
		return "", fmt.Errorf("search_symbols requires a query")
	}
	mgr := t.manager
	mgr.mu.RLock()
	a := mgr.audits[t.auditID]
	mgr.mu.RUnlock()
	if a == nil {
		return "", findings.ErrNotFound
	}

	a.mu.RLock()
	files := append([]*ast.File(nil), a.files...)
	a.mu.RUnlock()

	var matches []string
	for _, f := range files {
		for _, c := range f.Chunks {
			if c.Type == "file" || c.Type == "module" {
				continue
			}
			if strings.Contains(strings.ToLower(c.SymbolPath), query) {
				matches = append(matches, fmt.Sprintf("%s:%d %s (%s)", c.File, c.StartLine, c.SymbolPath, c.Type))
			}
		}
	}
	sort.Strings(matches)
	if len(matches) == 0 {
		return fmt.Sprintf("No symbol matches %q.", query), nil
	}
	if len(matches) > 100 {
		matches = matches[:100]
	}
	return strings.Join(matches, "\n"), nil
}

func (t *Tools) listFindings(ctx context.Context, args map[string]any) (string, error) {
	f := findings.Filter{}
	if v := argString(args, "severity"); v != "" {
		f.Severities = []string{v}
	}
	if v := argString(args, "cwe"); v != "" {
		f.CWEs = []string{v}
	}
	if v := argString(args, "file"); v != "" {
		f.Files = []string{v}
	}
	list, err := t.manager.Store().List(ctx, t.auditID, f)
	if err != nil {
		return "", err
	}
	type row struct {
		ID         string  `json:"id"`
		Severity   string  `json:"severity"`
		Confidence float64 `json:"confidence"`
		CWE        string  `json:"cwe,omitempty"`
		File       string  `json:"file"`
		Line       int     `json:"line"`
		Message    string  `json:"message"`
	}
	rows := make([]row, 0, len(list))
	for _, x := range list {
		rows = append(rows, row{
			ID:         x.ID,
			Severity:   string(x.Severity),
			Confidence: x.Confidence,
			CWE:        x.CWE,
			File:       x.File,
			Line:       x.StartLine,
			Message:    x.Message,
		})
	}
	data, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (t *Tools) getFinding(ctx context.Context, args map[string]any) (string, error) {
	id := argString(args, "id")
	if id == "" {
		return "", fmt.Errorf("get_finding requires an id")
	}
	f, err := t.manager.Store().Get(ctx, t.auditID, id)
	if err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// Store returns the findings store.
func (m *Manager) Store() *findings.Store { return m.deps.Store }
