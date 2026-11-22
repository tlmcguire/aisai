package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tlmcguire/aisai/internal/advisory"
	"github.com/tlmcguire/aisai/internal/audit"
	"github.com/tlmcguire/aisai/internal/chat"
	"github.com/tlmcguire/aisai/internal/findings"
	"github.com/tlmcguire/aisai/internal/index"
	"github.com/tlmcguire/aisai/internal/llm"
	"github.com/tlmcguire/aisai/internal/model"
	"github.com/tlmcguire/aisai/internal/patch"
	"github.com/tlmcguire/aisai/internal/sbom"
	"github.com/tlmcguire/aisai/internal/scan"
	"github.com/tlmcguire/aisai/internal/version"
)

// errNoAudit is returned by every tool that needs an audit when no audit is in
// progress. The wording is deliberate: it tells the calling model exactly what
// to do next.
var errNoAudit = errors.New("no audit in progress — call scan_package first")

// errLLMDisabled is returned by the LLM-backed tools when no provider is
// configured.
var errLLMDisabled = errors.New("LLM features are disabled: no LLM provider is configured (unset DISABLE_LLM and configure an Ollama endpoint or AISAI_BYOK_* credentials)")

// maxFindingsListed bounds how many findings a listing tool returns, so a
// large audit cannot flood the model's context.
const maxFindingsListed = 200

// fixTimeout bounds one remediation request to the model.
const fixTimeout = 180 * time.Second

// Deps are the collaborators the AISAI MCP tool set needs.
//
// Every field except Manager is optional: a nil provider simply disables the
// LLM-backed tools, and the rest of the tool set keeps working.
type Deps struct {
	// Manager owns the audits and their workspaces.
	Manager *audit.Manager
	// Advisory looks up known vulnerabilities. It may be nil.
	Advisory *advisory.Client
	// Chat answers questions about the audited source. It may be nil.
	Chat *chat.Chat
	// Index backs the code search. It may be nil.
	Index *index.Index
	// Store holds the findings. It may be nil, in which case the manager's
	// store is used.
	Store *findings.Store
	// LLM is the model provider. It is nil when LLM features are disabled.
	LLM llm.Provider
	// ChatModel is the model name used for chat and remediation requests.
	ChatModel string
	// NumCtx is the context window requested from the model.
	NumCtx int
	// Patches applies unified diffs. It may be nil, in which case the patch
	// package is used directly.
	Patches *patch.Applier
}

// Toolset holds the collaborators and the session state of the AISAI MCP tool
// set.
//
// The session state is the current audit id: an agent that scans a package
// once can then query it without repeating the id on every call. The id is
// updated by scan_package and can be set or read explicitly.
type Toolset struct {
	manager   *audit.Manager
	advisory  *advisory.Client
	chat      *chat.Chat
	index     *index.Index
	store     *findings.Store
	llm       llm.Provider
	chatModel string
	numCtx    int
	patches   *patch.Applier

	mu           sync.RWMutex
	currentAudit string
}

// NewToolset creates a toolset from its collaborators.
func NewToolset(d Deps) *Toolset {
	t := &Toolset{
		manager:   d.Manager,
		advisory:  d.Advisory,
		chat:      d.Chat,
		index:     d.Index,
		store:     d.Store,
		llm:       d.LLM,
		chatModel: d.ChatModel,
		numCtx:    d.NumCtx,
		patches:   d.Patches,
	}
	if t.numCtx <= 0 {
		t.numCtx = llm.DefaultNumCtx
	}
	if t.chatModel == "" {
		t.chatModel = "default"
	}
	return t
}

// SetChat sets the chat engine used by ask_codebase.
//
// The chat engine is created after the toolset because the toolset is the
// tool runner of the chat's tool-calling loop.
func (t *Toolset) SetChat(c *chat.Chat) {
	t.mu.Lock()
	t.chat = c
	t.mu.Unlock()
}

// chatEngine returns the chat engine, or nil when none is configured.
func (t *Toolset) chatEngine() *chat.Chat {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.chat
}

// SetCurrentAudit records the audit that tools default to.
func (t *Toolset) SetCurrentAudit(id string) {
	t.mu.Lock()
	t.currentAudit = id
	t.mu.Unlock()
}

// CurrentAudit returns the audit that tools default to, or "" when none.
func (t *Toolset) CurrentAudit() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.currentAudit
}

// Run executes a chat tool call against the current audit.
//
// It implements chat.ToolRunner so that the toolset can back the tool-calling
// loop of the chat engine without a per-audit wiring dance.
func (t *Toolset) Run(ctx context.Context, name string, args map[string]any) (string, error) {
	id := t.CurrentAudit()
	if id == "" {
		return "", errNoAudit
	}
	if t.manager == nil {
		return "", errors.New("no audit manager configured")
	}
	return audit.NewTools(t.manager, id).Run(ctx, name, args)
}

// RegisterTools wires the AISAI tool set onto s.
//
// The tool names are contractual: external agents are configured against
// them, so they must not change.
func RegisterTools(s *Server, t *Toolset) {
	if s == nil || t == nil {
		return
	}

	s.Register(Tool{
		Name: "scan_package",
		Description: "Prepare the source of a project for audit and return the audit id. Give either a PyPI package name or the path of a project folder on this computer. " +
			"This is the first step of every audit: a package is never installed and its setup code never runs, its source distribution is only downloaded into an isolated workspace; a folder is copied into the workspace and the original is never modified. " +
			"Source files in Python, JavaScript, TypeScript, Go, Rust, Java, C#, Ruby, C, C++, PHP, Scala and shell scripts are indexed and scanned. " +
			"The returned audit id becomes the current audit, so later tools can be called without repeating it.",
		InputSchema: ObjectSchema(map[string]any{
			"package": StringProp("The PyPI package name to audit, e.g. \"requests\"."),
			"version": StringProp("Optional version to audit, e.g. \"2.31.0\". Defaults to the latest release. Ignored with path."),
			"path":    StringProp("Absolute path of a project folder to audit instead of a package, e.g. \"/Users/me/projects/app\"."),
		}),
		Handler: t.scanPackage,
	})

	s.Register(Tool{
		Name: "get_dependency_graph",
		Description: "Return the resolved dependency graph of the audited package: every package with its version, whether it is a direct dependency, its maximum advisory severity and its finding count, plus the dependency edges. " +
			"Use this to understand what a package pulls in and which dependency is responsible for a finding.",
		InputSchema: ObjectSchema(map[string]any{
			"audit_id": StringProp("The audit to inspect. Defaults to the current audit."),
		}),
		Handler: t.dependencyGraph,
	})

	s.Register(Tool{
		Name: "list_files",
		Description: "List the files of the audited package as a tree, with the finding count and worst severity of each file. " +
			"Call this after scan_package to discover which source files exist and where the findings are; the paths it prints are the ones get_source, get_ast and run_llm_scan expect.",
		InputSchema: ObjectSchema(map[string]any{
			"audit_id": StringProp("The audit to inspect. Defaults to the current audit."),
		}),
		Handler: t.listFiles,
	})

	s.Register(Tool{
		Name: "get_source",
		Description: "Return the full text of one source file of the audited package, together with any findings in it. " +
			"Use this when a search result or an AST summary only told you what a file contains and you need to read it.",
		InputSchema: ObjectSchema(map[string]any{
			"path":       StringProp("Workspace-relative path of the file, e.g. \"pkgs/requests-2.31.0/requests/__init__.py\"."),
			"start_line": IntProp("First line to return, 1-based. Use it to read a file that was truncated by the output cap."),
			"end_line":   IntProp("Last line to return, inclusive. Use it together with start_line to page through a long file."),
			"audit_id":   StringProp("The audit to read from. Defaults to the current audit."),
		}, "path"),
		Handler: t.getSource,
	})

	s.Register(Tool{
		Name: "get_ast",
		Description: "Return the syntax tree of one source file of the audited package: node types, line numbers and enclosing scopes. " +
			"Use this to understand the structure of a file and how functions and calls relate, without pulling the whole source into the context.",
		InputSchema: ObjectSchema(map[string]any{
			"path":     StringProp("Workspace-relative path of the source file."),
			"audit_id": StringProp("The audit to read from. Defaults to the current audit."),
		}, "path"),
		Handler: t.getAST,
	})

	s.Register(Tool{
		Name: "run_sast",
		Description: "Run the static analysis rule engine over every file of the audited package and store the findings. " +
			"Use this to get the deterministic findings (dangerous calls, weak crypto, hardcoded secrets and similar) before triaging with list_findings. " +
			"This is fast and needs no model.",
		InputSchema: ObjectSchema(map[string]any{
			"audit_id": StringProp("The audit to scan. Defaults to the current audit."),
		}),
		Handler: t.runSAST,
	})

	s.Register(Tool{
		Name: "run_llm_scan",
		Description: "Run the LLM detection-and-verification pipeline over one file of the audited package and store the findings it confirms. " +
			"Use this after run_sast on files that look risky, to find logic flaws the static rules cannot see. " +
			"It is slow (one model round trip per chunk) and requires a configured LLM provider.",
		InputSchema: ObjectSchema(map[string]any{
			"path":     StringProp("Workspace-relative path of the file to analyse."),
			"audit_id": StringProp("The audit to scan. Defaults to the current audit."),
		}, "path"),
		Handler: t.runLLMScan,
	})

	s.Register(Tool{
		Name: "list_findings",
		Description: "List the findings of the audited package, most severe first, with severity, confidence, CWE and location. " +
			"Use this to triage: filter by severity, CWE or state to narrow the list down to what you want to act on.",
		InputSchema: ObjectSchema(map[string]any{
			"audit_id": StringProp("The audit to inspect. Defaults to the current audit."),
			"severity": StringProp("Optional severity filter: critical, high, medium, low or info."),
			"cwe":      StringProp("Optional CWE filter, e.g. \"CWE-798\"."),
			"state":    StringProp("Optional triage state filter: open, confirmed, suppressed or fixed."),
		}),
		Handler: t.listFindings,
	})

	s.Register(Tool{
		Name: "get_finding",
		Description: "Return the full detail of one finding: the offending code, the data-flow evidence, the reachability, the remediation hint and any patch the scanner already produced. " +
			"Use this after list_findings when you need to judge a specific finding.",
		InputSchema: ObjectSchema(map[string]any{
			"finding_id": StringProp("The id of the finding, as printed by list_findings."),
			"audit_id":   StringProp("The audit the finding belongs to. Defaults to the current audit."),
		}, "finding_id"),
		Handler: t.getFinding,
	})

	s.Register(Tool{
		Name: "propose_fix",
		Description: "Propose a remediation patch for one finding: a unified diff that fixes the vulnerability in the audited source, plus an explanation of why it is safe. " +
			"Use this after get_finding to turn a finding into an actionable change, then pass the returned diff to apply_patch. " +
			"The diff is validated against the current file before it is returned, so it is safe to apply as-is.",
		InputSchema: ObjectSchema(map[string]any{
			"finding_id": StringProp("The id of the finding to fix, as printed by list_findings."),
			"audit_id":   StringProp("The audit the finding belongs to. Defaults to the current audit."),
		}, "finding_id"),
		Handler: t.proposeFix,
	})

	s.Register(Tool{
		Name: "apply_patch",
		Description: "Apply a unified diff to a file inside the audited package's workspace. " +
			"The diff is checked against the current file content first and refused if it does not apply cleanly, so a patch never lands half-applied. " +
			"Paths that point outside the audit workspace are refused. " +
			"Use this with the diff returned by propose_fix.",
		InputSchema: ObjectSchema(map[string]any{
			"path":         StringProp("Workspace-relative path of the file to patch."),
			"unified_diff": StringProp("A unified diff, as returned by propose_fix."),
			"audit_id":     StringProp("The audit to patch. Defaults to the current audit."),
		}, "path", "unified_diff"),
		Handler: t.applyPatch,
	})

	s.Register(Tool{
		Name: "export_report",
		Description: "Export the findings of the audited package as a SARIF 2.1.0 document or as plain JSON. " +
			"Use this to hand the results to another tool (code scanning, a CI job, a ticket).",
		InputSchema: ObjectSchema(map[string]any{
			"format":   StringProp("Export format: \"sarif\" (default), \"cyclonedx\" or \"spdx\" for an SBOM, or \"json\"."),
			"audit_id": StringProp("The audit to export. Defaults to the current audit."),
		}),
		Handler: t.exportReport,
	})

	s.Register(Tool{
		Name: "search_code",
		Description: "Search the audited package for code matching a natural-language or keyword query, using hybrid keyword and semantic retrieval over the indexed chunks. " +
			"Use this to find where something is implemented when you do not know the file or the symbol name.",
		InputSchema: ObjectSchema(map[string]any{
			"query":    StringProp("What to look for, e.g. \"where the auth token is validated\"."),
			"limit":    map[string]any{"type": "integer", "description": "Maximum number of hits to return. Defaults to 10."},
			"audit_id": StringProp("The audit to search. Defaults to the current audit."),
		}, "query"),
		Handler: t.searchCode,
	})

	s.Register(Tool{
		Name: "ask_codebase",
		Description: "Ask a question about the audited package's source and get an answer grounded in the retrieved code, with file and line citations. " +
			"Use this for questions about behaviour and design that are better answered by reading the code than by listing findings.",
		InputSchema: ObjectSchema(map[string]any{
			"question": StringProp("The question to answer about the audited package."),
			"audit_id": StringProp("The audit to ask about. Defaults to the current audit."),
		}, "question"),
		Handler: t.askCodebase,
	})

	registerManagementTools(s, t)
}

// scanPackage resolves, downloads and indexes a package, then makes it the
// current audit.
func (t *Toolset) scanPackage(ctx context.Context, args map[string]any) (string, error) {
	pkg := strings.TrimSpace(ArgString(args, "package"))
	folder := strings.TrimSpace(ArgString(args, "path"))
	if pkg == "" && folder == "" {
		return "", errors.New("scan_package requires a package name or a folder path")
	}
	pkgVersion := strings.TrimSpace(ArgString(args, "version"))
	if t.manager == nil {
		return "", errors.New("no audit manager configured")
	}

	var (
		a   *model.Audit
		err error
	)
	if folder != "" {
		a, err = t.manager.CreateLocal(ctx, folder)
		pkg = folder
	} else {
		a, err = t.manager.Create(ctx, pkg, pkgVersion)
	}
	if err != nil {
		return "", fmt.Errorf("create audit: %w", err)
	}
	if err := t.manager.Run(ctx, a.ID); err != nil {
		return "", fmt.Errorf("scan %s: %w", pkg, err)
	}
	t.SetCurrentAudit(a.ID)

	snap, err := t.manager.Snapshot(a.ID)
	if err != nil {
		return "", fmt.Errorf("read audit: %w", err)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Audit %s is ready for %s", a.ID, snap.Package)
	if snap.Version != "" {
		fmt.Fprintf(&b, " %s", snap.Version)
	}
	b.WriteString(".\n")
	fmt.Fprintf(&b, "audit_id: %s\n", a.ID)
	fmt.Fprintf(&b, "files: %d\n", snap.FileCount)
	fmt.Fprintf(&b, "chunks: %d\n", snap.ChunkCount)

	if advisories := t.queryAdvisories(ctx, snap.Package, snap.Version); len(advisories) > 0 {
		fmt.Fprintf(&b, "known advisories: %d\n", len(advisories))
		for _, adv := range advisories {
			fmt.Fprintf(&b, "  [%s] %s %s\n", adv.Severity, adv.ID, adv.Summary)
		}
	}

	b.WriteString("\nNext: call run_sast to run the static rule engine, then list_findings to triage.")
	return b.String(), nil
}

// queryAdvisories looks up known vulnerabilities for a package. The lookup is
// best effort: an unreachable advisory source must not fail the scan.
func (t *Toolset) queryAdvisories(ctx context.Context, pkg, pkgVersion string) []model.Advisory {
	if t.advisory == nil || pkg == "" || pkgVersion == "" {
		return nil
	}
	advs, err := t.advisory.Query(ctx, pkg, pkgVersion)
	if err != nil {
		return nil
	}
	return advs
}

// dependencyGraph renders the dependency graph of an audit.
func (t *Toolset) dependencyGraph(ctx context.Context, args map[string]any) (string, error) {
	id, err := t.resolveAudit(args)
	if err != nil {
		return "", err
	}
	g, err := t.manager.Graph(ctx, id)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Dependency graph of audit %s: %d packages, %d edges.\n", id, len(g.Nodes), len(g.Edges))
	for _, n := range g.Nodes {
		fmt.Fprintf(&b, "%s  v%s", n.ID, n.Version)
		if n.Direct {
			b.WriteString("  [direct]")
		}
		if n.MaxSeverity != "" {
			fmt.Fprintf(&b, "  severity=%s", n.MaxSeverity)
		}
		if n.FindingCount > 0 {
			fmt.Fprintf(&b, "  findings=%d", n.FindingCount)
		}
		if n.VulnCount > 0 {
			fmt.Fprintf(&b, "  advisories=%d", n.VulnCount)
		}
		b.WriteString("\n")
	}
	if len(g.Edges) > 0 {
		b.WriteString("edges:\n")
		for _, e := range g.Edges {
			fmt.Fprintf(&b, "  %s -> %s\n", e.Source, e.Target)
		}
	}
	return b.String(), nil
}

// listFiles renders the file tree of an audit.
func (t *Toolset) listFiles(ctx context.Context, args map[string]any) (string, error) {
	id, err := t.resolveAudit(args)
	if err != nil {
		return "", err
	}
	tree, err := t.manager.FileTree(ctx, id)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Files in audit %s:\n", id)
	renderTree(&b, tree, "")
	return b.String(), nil
}

// renderTree writes one file tree node and its children.
//
// The paths printed are relative to the audit workspace root, so they are
// exactly the paths get_source, get_ast and run_llm_scan expect.
func renderTree(b *strings.Builder, node *model.FileNode, rel string) {
	switch node.Type {
	case "dir":
		if rel != "" {
			fmt.Fprintf(b, "%s/\n", rel)
		}
	default:
		fmt.Fprintf(b, "%s", rel)
		if node.FindingCount > 0 {
			fmt.Fprintf(b, "  (%d findings", node.FindingCount)
			if node.MaxSeverity != "" {
				fmt.Fprintf(b, ", worst %s", node.MaxSeverity)
			}
			b.WriteString(")")
		}
		b.WriteString("\n")
	}
	for _, child := range node.Children {
		childRel := child.Name
		if rel != "" {
			childRel = rel + "/" + child.Name
		}
		renderTree(b, child, childRel)
	}
}

// getSource returns the contents of one file.
//
// The tool takes an optional line range, because a long file can exceed the
// output cap of the transport. The header always states the total number of
// lines and the range that was returned, so an agent can tell that it is
// looking at a part of a file and can ask for the rest.
func (t *Toolset) getSource(ctx context.Context, args map[string]any) (string, error) {
	path := strings.TrimSpace(ArgString(args, "path"))
	if path == "" {
		return "", errors.New("get_source requires a path")
	}
	id, err := t.resolveAudit(args)
	if err != nil {
		return "", err
	}
	f, err := t.manager.File(ctx, id, path)
	if err != nil {
		return "", err
	}

	lines := f.Lines
	start := ArgInt(args, "start_line")
	end := ArgInt(args, "end_line")
	if start < 1 {
		start = 1
	}
	if end < 1 || end > len(lines) {
		end = len(lines)
	}
	if start > end {
		return "", fmt.Errorf("get_source: start_line %d is past end_line %d", start, end)
	}

	var b strings.Builder
	if start == 1 && end == len(lines) {
		fmt.Fprintf(&b, "%s (%s, %d lines)\n", f.Path, f.Language, len(lines))
	} else {
		fmt.Fprintf(&b, "%s (%s, lines %d-%d of %d)\n", f.Path, f.Language, start, end, len(lines))
	}
	b.WriteString(strings.Join(lines[start-1:end], "\n"))
	if len(f.Findings) > 0 {
		b.WriteString("\n\nFindings in this file:\n")
		for _, finding := range f.Findings {
			b.WriteString(FindingSummary(finding))
			b.WriteString("\n")
		}
	}
	return b.String(), nil
}

// getAST returns the syntax tree of one file.
func (t *Toolset) getAST(ctx context.Context, args map[string]any) (string, error) {
	path := strings.TrimSpace(ArgString(args, "path"))
	if path == "" {
		return "", errors.New("get_ast requires a path")
	}
	id, err := t.resolveAudit(args)
	if err != nil {
		return "", err
	}
	a, err := t.manager.AST(ctx, id, path)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Syntax tree of %s (%d nodes)\n", a.Path, len(a.Nodes))
	for _, n := range a.Nodes {
		fmt.Fprintf(&b, "%s%s (line %d)\n", strings.Repeat("  ", n.Depth), n.Type, n.StartLine)
	}
	return b.String(), nil
}

// runSAST runs the static rule engine over the audit.
func (t *Toolset) runSAST(ctx context.Context, args map[string]any) (string, error) {
	id, err := t.resolveAudit(args)
	if err != nil {
		return "", err
	}
	list, err := t.manager.Scan(ctx, id)
	if err != nil {
		return "", err
	}
	if len(list) == 0 {
		return fmt.Sprintf("The static rule engine found no issues in audit %s.", id), nil
	}
	return renderFindings(id, list), nil
}

// runLLMScan runs the LLM detection pipeline over one file.
func (t *Toolset) runLLMScan(ctx context.Context, args map[string]any) (string, error) {
	path := strings.TrimSpace(ArgString(args, "path"))
	if path == "" {
		return "", errors.New("run_llm_scan requires a path")
	}
	id, err := t.resolveAudit(args)
	if err != nil {
		return "", err
	}
	if t.llm == nil {
		return "", errLLMDisabled
	}
	list, err := t.manager.ScanLLM(ctx, id, path)
	if err != nil {
		return "", err
	}
	if len(list) == 0 {
		return fmt.Sprintf("The LLM scan of %s in audit %s found no confirmed issues.", path, id), nil
	}
	return renderFindings(id, list), nil
}

// listFindings lists the findings of an audit, filtered.
func (t *Toolset) listFindings(ctx context.Context, args map[string]any) (string, error) {
	id, err := t.resolveAudit(args)
	if err != nil {
		return "", err
	}
	store := t.findingsStore()
	if store == nil {
		return "", errors.New("no findings store configured")
	}

	filter := findings.Filter{}
	if v := strings.TrimSpace(ArgString(args, "severity")); v != "" {
		filter.Severities = []string{strings.ToLower(v)}
	}
	if v := strings.TrimSpace(ArgString(args, "cwe")); v != "" {
		filter.CWEs = []string{strings.ToUpper(v)}
	}
	if v := strings.TrimSpace(ArgString(args, "state")); v != "" {
		filter.States = []string{strings.ToLower(v)}
	}

	list, err := store.List(ctx, id, filter)
	if err != nil {
		return "", err
	}
	if len(list) == 0 {
		return fmt.Sprintf("No findings matched in audit %s.", id), nil
	}
	return renderFindings(id, list), nil
}

// getFinding returns the full detail of one finding.
func (t *Toolset) getFinding(ctx context.Context, args map[string]any) (string, error) {
	findingID := strings.TrimSpace(ArgString(args, "finding_id"))
	if findingID == "" {
		return "", errors.New("get_finding requires a finding_id")
	}
	id, err := t.resolveAudit(args)
	if err != nil {
		return "", err
	}
	store := t.findingsStore()
	if store == nil {
		return "", errors.New("no findings store configured")
	}
	f, err := store.Get(ctx, id, findingID)
	if err != nil {
		return "", fmt.Errorf("finding %s: %w", findingID, err)
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// fixSchema is the JSON schema the remediation model must conform to.
// proposeFix asks the model for a remediation patch for one finding.
func (t *Toolset) proposeFix(ctx context.Context, args map[string]any) (string, error) {
	findingID := strings.TrimSpace(ArgString(args, "finding_id"))
	if findingID == "" {
		return "", errors.New("propose_fix requires a finding_id")
	}
	id, err := t.resolveAudit(args)
	if err != nil {
		return "", err
	}
	store := t.findingsStore()
	if store == nil {
		return "", errors.New("no findings store configured")
	}
	f, err := store.Get(ctx, id, findingID)
	if err != nil {
		return "", fmt.Errorf("finding %s: %w", findingID, err)
	}

	root, err := t.manager.AuditRoot(id)
	if err != nil {
		return "", err
	}
	abs, err := audit.ResolvePath(root, f.File)
	if err != nil {
		return "", err
	}
	content, err := os.ReadFile(abs)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", f.File, err)
	}

	// A scanner that already produced a diff saves the model round trip.
	if strings.TrimSpace(f.Patch) != "" {
		return renderFix(f, f.Patch, string(content), ""), nil
	}
	if t.llm == nil {
		return "", fmt.Errorf("propose_fix needs an LLM provider and this finding carries no patch: %w", errLLMDisabled)
	}

	reqCtx, cancel := context.WithTimeout(ctx, fixTimeout)
	defer cancel()

	fix, err := scan.ProposeFix(reqCtx, t.llm, t.chatModel, t.numCtx, *f, string(content))
	if err != nil {
		return "", fmt.Errorf("propose_fix: %w", err)
	}
	return renderFix(f, fix.Unified, string(content), fix.Explanation), nil
}

// renderFix renders a proposed patch and whether it applies cleanly.
func renderFix(f *model.Finding, unified, content, explanation string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Proposed fix for %s (%s) in %s:%d\n", f.ID, f.CWE, f.File, f.StartLine)
	if explanation != "" {
		fmt.Fprintf(&b, "%s\n", explanation)
	}
	if err := patch.Validate(content, unified); err != nil {
		fmt.Fprintf(&b, "The diff does not apply cleanly to the current file: %v\n", err)
	} else {
		b.WriteString("The diff applies cleanly to the current file.\n")
	}
	b.WriteString("\n")
	b.WriteString(unified)
	b.WriteString("\n\nPass this diff to apply_patch to apply it.")
	return b.String()
}

// applyPatch applies a unified diff inside the audit workspace.
func (t *Toolset) applyPatch(ctx context.Context, args map[string]any) (string, error) {
	path := strings.TrimSpace(ArgString(args, "path"))
	if path == "" {
		return "", errors.New("apply_patch requires a path")
	}
	unified := ArgString(args, "unified_diff")
	if strings.TrimSpace(unified) == "" {
		return "", errors.New("apply_patch requires a unified_diff")
	}
	id, err := t.resolveAudit(args)
	if err != nil {
		return "", err
	}

	root, err := t.manager.AuditRoot(id)
	if err != nil {
		return "", err
	}
	// audit.ResolvePath is the single path-traversal chokepoint: anything that
	// resolves outside the audit root is refused here, before anything is
	// written.
	abs, err := audit.ResolvePath(root, path)
	if err != nil {
		return "", fmt.Errorf("refusing to patch %s: %w", path, err)
	}
	current, err := os.ReadFile(abs)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	// Validate before applying so a diff that does not fit never lands
	// half-applied.
	if err := patch.Validate(string(current), unified); err != nil {
		return "", fmt.Errorf("the patch does not apply cleanly to %s: %w", path, err)
	}

	if t.patches != nil {
		if err := t.patches.Apply(ctx, root, path, unified); err != nil {
			return "", fmt.Errorf("apply patch: %w", err)
		}
	} else if err := patch.Apply(root, path, unified); err != nil {
		return "", fmt.Errorf("apply patch: %w", err)
	}

	return fmt.Sprintf("Applied the patch to %s in audit %s.", path, id), nil
}

// exportReport exports the findings of an audit as SARIF, an SBOM or JSON.
func (t *Toolset) exportReport(ctx context.Context, args map[string]any) (string, error) {
	id, err := t.resolveAudit(args)
	if err != nil {
		return "", err
	}
	store := t.findingsStore()
	if store == nil {
		return "", errors.New("no findings store configured")
	}
	format := strings.ToLower(strings.TrimSpace(ArgString(args, "format")))
	if format == "" {
		format = "sarif"
	}

	list, err := store.List(ctx, id, findings.Filter{})
	if err != nil {
		return "", err
	}

	switch format {
	case "sarif":
		data, err := findings.WriteSARIF(list, findings.SARIFOptions{ToolVersion: version.Short()})
		if err != nil {
			return "", err
		}
		return string(data), nil
	case "json":
		data, err := findings.WriteJSON(list)
		if err != nil {
			return "", err
		}
		return string(data), nil
	case "cyclonedx", "spdx":
		packages, err := t.manager.Packages(ctx, id)
		if err != nil {
			return "", err
		}
		advisories, err := t.manager.Advisories(id)
		if err != nil {
			return "", err
		}
		opts := sbom.Options{ToolVersion: version.Short(), AuditID: id}
		if snap, err := t.manager.Snapshot(id); err == nil {
			opts.PackageName = snap.Package
			opts.PackageVersion = snap.Version
		}
		var data []byte
		if format == "cyclonedx" {
			data, err = sbom.WriteCycloneDX(packages, advisories, list, opts)
		} else {
			data, err = sbom.WriteSPDX(packages, advisories, list, opts)
		}
		if err != nil {
			return "", err
		}
		return string(data), nil
	default:
		return "", fmt.Errorf("format must be sarif, cyclonedx, spdx or json, got %q", format)
	}
}

// searchCode runs hybrid retrieval over the indexed chunks of an audit.
func (t *Toolset) searchCode(ctx context.Context, args map[string]any) (string, error) {
	query := strings.TrimSpace(ArgString(args, "query"))
	if query == "" {
		return "", errors.New("search_code requires a query")
	}
	id, err := t.resolveAudit(args)
	if err != nil {
		return "", err
	}
	if t.index == nil {
		return "", errors.New("no index configured")
	}
	limit := argInt(args, "limit", 10)
	if limit <= 0 || limit > 50 {
		limit = 10
	}

	hits, keywordOnly, err := t.index.Search(id, query, limit)
	if err != nil {
		return "", err
	}
	if len(hits) == 0 {
		return fmt.Sprintf("No code matched %q in audit %s.", query, id), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d match(es) for %q in audit %s", len(hits), query, id)
	if keywordOnly {
		b.WriteString(" (keyword-only: no embeddings available)")
	}
	b.WriteString(":\n")
	for _, h := range hits {
		fmt.Fprintf(&b, "%s:%d-%d %s (%s, score %.4f)\n",
			h.Chunk.File, h.Chunk.StartLine, h.Chunk.EndLine,
			h.Chunk.SymbolPath, h.Chunk.Type, h.Score)
		if snippet := snippet(h.Chunk.Content, 200); snippet != "" {
			fmt.Fprintf(&b, "    %s\n", snippet)
		}
	}
	return b.String(), nil
}

// askCodebase answers a question about the audited source.
func (t *Toolset) askCodebase(ctx context.Context, args map[string]any) (string, error) {
	question := strings.TrimSpace(ArgString(args, "question"))
	if question == "" {
		return "", errors.New("ask_codebase requires a question")
	}
	id, err := t.resolveAudit(args)
	if err != nil {
		return "", err
	}
	if t.llm == nil || t.chatEngine() == nil {
		return "", errLLMDisabled
	}

	result, err := t.chatEngine().Ask(ctx, chat.Request{AuditID: id, Question: question}, nil)
	if err != nil {
		return "", err
	}
	if result == nil {
		return "", errors.New("ask_codebase: no answer was produced")
	}

	var b strings.Builder
	b.WriteString(result.Answer)
	if len(result.Citations) > 0 {
		b.WriteString("\n\nCitations:\n")
		for _, c := range result.Citations {
			fmt.Fprintf(&b, "  %s:%d", c.File, c.Line)
			if c.Symbol != "" {
				fmt.Fprintf(&b, " (%s)", c.Symbol)
			}
			b.WriteString("\n")
		}
	}
	return b.String(), nil
}

// resolveAudit returns the audit a tool should act on: the explicit
// audit_id argument when given, otherwise the current audit.
func (t *Toolset) resolveAudit(args map[string]any) (string, error) {
	if t.manager == nil {
		return "", errors.New("no audit manager configured")
	}
	id := strings.TrimSpace(ArgString(args, "audit_id"))
	if id == "" {
		id = t.CurrentAudit()
	}
	if id == "" {
		return "", errNoAudit
	}
	if _, err := t.manager.Snapshot(id); err != nil {
		return "", fmt.Errorf("unknown audit %q — call scan_package first", id)
	}
	return id, nil
}

// findingsStore returns the store the findings live in.
func (t *Toolset) findingsStore() *findings.Store {
	if t.store != nil {
		return t.store
	}
	if t.manager == nil {
		return nil
	}
	return t.manager.Store()
}

// renderFindings renders a list of findings as compact summaries.
func renderFindings(auditID string, list []model.Finding) string {
	counts := map[model.Severity]int{}
	for _, f := range list {
		counts[f.Severity]++
	}

	severities := make([]model.Severity, 0, len(counts))
	for sev := range counts {
		severities = append(severities, sev)
	}
	sort.Slice(severities, func(i, j int) bool { return severities[i].Rank() > severities[j].Rank() })

	var b strings.Builder
	fmt.Fprintf(&b, "%d finding(s) in audit %s", len(list), auditID)
	if len(severities) > 0 {
		b.WriteString(":")
		for _, sev := range severities {
			fmt.Fprintf(&b, " %s=%d", sev, counts[sev])
		}
	}
	b.WriteString("\n")

	shown := list
	if len(shown) > maxFindingsListed {
		shown = shown[:maxFindingsListed]
	}
	for _, f := range shown {
		// The id is printed first: it is what get_finding and propose_fix
		// take as an argument.
		fmt.Fprintf(&b, "%s %s\n", f.ID, FindingSummary(f))
	}
	if len(list) > len(shown) {
		fmt.Fprintf(&b, "... %d more finding(s) not shown\n", len(list)-len(shown))
	}
	return b.String()
}

// snippet returns the first n characters of s on a single line.
func snippet(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n]) + "…"
}

// argInt reads an integer argument, falling back to def.
func argInt(args map[string]any, key string, def int) int {
	switch v := args[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return int(n)
		}
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return def
}
