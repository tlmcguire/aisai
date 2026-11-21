// Package audit orchestrates an analysis of one package version.
//
// The lifecycle is:
//
//	create -> acquire -> index -> ready
//
// "acquire" resolves the dependency graph and downloads and safely extracts
// the source of every dependency into an isolated workspace. "index" parses
// the Python files into semantic chunks and builds the retrieval index.
// "ready" means the audit can be scanned, browsed and chatted with.
package audit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tlmcguire/aisai/internal/advisory"
	"github.com/tlmcguire/aisai/internal/ast"
	"github.com/tlmcguire/aisai/internal/config"
	"github.com/tlmcguire/aisai/internal/deps"
	"github.com/tlmcguire/aisai/internal/findings"
	"github.com/tlmcguire/aisai/internal/index"
	"github.com/tlmcguire/aisai/internal/llm"
	"github.com/tlmcguire/aisai/internal/model"
	"github.com/tlmcguire/aisai/internal/pypi"
	"github.com/tlmcguire/aisai/internal/sast"
	"github.com/tlmcguire/aisai/internal/scan"
)

// Emitter receives progress events. It is normally the server's event hub.
type Emitter func(model.Event)

// Deps are the collaborators an audit Manager needs.
type Deps struct {
	Config   *config.Config
	PyPI     *pypi.Client
	Resolver *deps.Resolver
	SAST     *sast.Engine
	Store    *findings.Store
	Index    *index.Index
	LLM      llm.Provider
	// Advisory queries for known vulnerabilities. It is nil when advisory
	// lookups are disabled.
	Advisory *advisory.Client
	// Detector runs the LLM detection pipeline. It is nil when LLM features
	// are disabled.
	Detector *scan.Detector
	Emit     Emitter
}

// Manager owns the set of audits for the process.
type Manager struct {
	deps Deps

	mu     sync.RWMutex
	audits map[string]*Audit
}

// Audit is one in-flight or completed analysis.
type Audit struct {
	ID      string
	Package string
	Version string
	Root    string

	mu       sync.RWMutex
	status   model.AuditStatus
	phase    string
	progress float64
	message  string
	failure  string
	created  time.Time
	updated  time.Time

	packages []model.Package
	edges    []model.GraphEdge
	files    []*ast.File

	// pkgByDir maps a package directory name under "pkgs" to its PURL. It is
	// what lets a finding be attributed to the package it came from, so the
	// dependency graph can show a finding count per node.
	pkgByDir map[string]model.PURL

	// advisories holds the flattened advisory list fetched for this audit,
	// and advisoriesByPkg the same advisories keyed by package PURL.
	advisories      []model.Advisory
	advisoriesByPkg map[string][]model.Advisory
	// advisoriesFetched distinguishes "fetched and found nothing" from
	// "never fetched". Without it an audit whose packages have no known
	// advisories would re-query the source on every request.
	advisoriesFetched bool
}

// NewManager creates an audit manager.
func NewManager(d Deps) *Manager {
	return &Manager{deps: d, audits: map[string]*Audit{}}
}

// Create allocates an audit and its workspace, but does not start work.
func (m *Manager) Create(ctx context.Context, pkg, version string) (*model.Audit, error) {
	id, err := newAuditID()
	if err != nil {
		return nil, err
	}
	root := filepath.Join(m.deps.Config.Workspace, id)
	if err := os.MkdirAll(filepath.Join(root, "pkgs"), 0o755); err != nil {
		return nil, fmt.Errorf("create audit workspace: %w", err)
	}

	a := &Audit{
		ID:      id,
		Package: pkg,
		Version: version,
		Root:    root,
		status:  model.AuditPending,
		created: time.Now().UTC(),
		updated: time.Now().UTC(),
	}
	m.mu.Lock()
	m.audits[id] = a
	m.mu.Unlock()

	return a.snapshot(), nil
}

// Run performs the acquisition and indexing phases. It is safe to call
// concurrently with reads.
func (m *Manager) Run(ctx context.Context, id string) error {
	m.mu.RLock()
	a := m.audits[id]
	m.mu.RUnlock()
	if a == nil {
		return findings.ErrNotFound
	}

	a.setStatus(model.AuditAcquiring, "acquiring", 0.05, "Resolving dependencies")

	if m.deps.Resolver == nil {
		err := errors.New("no dependency resolver configured")
		a.fail(err)
		return err
	}

	res, err := m.deps.Resolver.Resolve(ctx, a.Package, a.Version)
	if err != nil {
		a.fail(fmt.Errorf("resolve dependencies: %w", err))
		return err
	}

	a.mu.Lock()
	a.packages = res.Packages
	a.edges = res.Edges
	// Adopt the resolved root version so the UI shows the real version.
	for _, p := range res.Packages {
		if strings.EqualFold(p.Name, a.Package) {
			a.Version = p.Version
			break
		}
	}
	a.mu.Unlock()

	a.setStatus(model.AuditAcquiring, "acquiring", 0.2, "Downloading sources")

	files, err := m.acquire(ctx, a, res)
	if err != nil {
		a.fail(err)
		return err
	}

	a.mu.Lock()
	a.files = files
	a.mu.Unlock()

	a.setStatus(model.AuditIndexing, "indexing", 0.7, "Parsing and indexing")

	chunks := make([]model.Chunk, 0, len(files)*8)
	for _, f := range files {
		chunks = append(chunks, f.Chunks...)
	}

	if m.deps.Index != nil {
		if err := m.deps.Index.AddChunks(a.ID, chunks); err != nil {
			// A broken index must not fail the audit: keyword search still
			// degrades gracefully and the code view is unaffected.
			m.emit(model.Event{
				Type:    model.EventAuditProgress,
				AuditID: a.ID,
				Phase:   "indexing",
				Percent: 0.9,
				Message: "Indexing unavailable: " + err.Error(),
			})
		}
	}

	a.mu.Lock()
	a.status = model.AuditReady
	a.phase = "ready"
	a.progress = 1
	a.message = "Ready"
	a.updated = time.Now().UTC()
	a.mu.Unlock()

	m.emit(model.Event{Type: model.EventAuditDone, AuditID: a.ID})

	// Advisories are fetched in the background: the audit is already usable
	// and the graph must not appear to show "0 vulnerabilities" while the
	// lookup is still in flight. The client is told when it lands.
	if m.deps.Advisory != nil {
		go func() {
			// The audit request may have been cancelled by the time the
			// graph is drawn; the advisory lookup outlives it.
			bgCtx := context.WithoutCancel(ctx)
			advisories, err := m.FetchAdvisories(bgCtx, a.ID, false)
			if err != nil {
				m.emit(model.Event{
					Type:    model.EventAuditProgress,
					AuditID: a.ID,
					Phase:   "advisories",
					Error:   err.Error(),
				})
				return
			}
			m.emit(model.Event{
				Type:    model.EventAdvisories,
				AuditID: a.ID,
				Data:    map[string]any{"count": len(advisories), "mode": m.AdvisoryMode()},
			})
		}()
	}
	return nil
}

// acquire downloads and safely extracts the source of every resolved package.
func (m *Manager) acquire(ctx context.Context, a *Audit, res *deps.Resolution) ([]*ast.File, error) {
	pkgs := append([]model.Package(nil), res.Packages...)
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].Name < pkgs[j].Name })

	var files []*ast.File
	pkgByDir := make(map[string]model.PURL, len(pkgs))
	for i, p := range pkgs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		percent := 0.2 + 0.45*float64(i)/float64(max(1, len(pkgs)))
		m.emit(model.Event{
			Type:    model.EventAuditProgress,
			AuditID: a.ID,
			Phase:   "acquiring",
			Percent: percent,
			Message: fmt.Sprintf("Fetching %s %s", p.Name, p.Version),
		})

		dirName := safeDirName(p.Name, p.Version)
		dest := filepath.Join(a.Root, "pkgs", dirName)
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return nil, err
		}
		// Record the directory before the download: even a package whose
		// source cannot be fetched still belongs in the graph.
		pkgByDir[dirName] = p.PURL

		tarball, err := m.deps.PyPI.DownloadSdist(ctx, p.Name, p.Version, dest)
		if err != nil {
			// A package we cannot fetch is not fatal — the rest of the graph
			// is still worth scanning. Record and continue.
			m.emit(model.Event{
				Type:    model.EventAuditProgress,
				AuditID: a.ID,
				Phase:   "acquiring",
				Percent: percent,
				Message: fmt.Sprintf("Skipped %s: %v", p.Name, err),
			})
			continue
		}
		if err := extractTarball(tarball, dest); err != nil {
			m.emit(model.Event{
				Type:    model.EventAuditProgress,
				AuditID: a.ID,
				Phase:   "acquiring",
				Percent: percent,
				Message: fmt.Sprintf("Skipped %s: %v", p.Name, err),
			})
			continue
		}

		parsed, err := parseTree(dest, a.Root, a.ID)
		if err == nil {
			files = append(files, parsed...)
		}
	}

	a.mu.Lock()
	a.pkgByDir = pkgByDir
	a.mu.Unlock()

	return files, nil
}

// Scan runs the SAST engine over the audit's parsed files and stores the
// results.
func (m *Manager) Scan(ctx context.Context, id string) ([]model.Finding, error) {
	m.mu.RLock()
	a := m.audits[id]
	m.mu.RUnlock()
	if a == nil {
		return nil, findings.ErrNotFound
	}

	a.mu.RLock()
	files := append([]*ast.File(nil), a.files...)
	pkgByDir := a.pkgByDir
	a.mu.RUnlock()

	if len(files) == 0 {
		return nil, nil
	}

	scanID := fmt.Sprintf("scan-%d", time.Now().UnixNano())
	started := time.Now()

	m.emit(model.Event{
		Type:    model.EventScanProgress,
		AuditID: id,
		Phase:   "sast",
		Percent: 0.1,
		Message: fmt.Sprintf("Scanning %d files", len(files)),
	})

	raw := m.deps.SAST.Scan(files)

	// Normalize every finding: severity, confidence, state and identity must
	// be consistent no matter which engine produced it.
	out := make([]model.Finding, 0, len(raw))
	for _, f := range raw {
		if !f.Severity.Valid() {
			f.Severity = model.SeverityInfo
		}
		if f.Confidence <= 0 || f.Confidence > 1 {
			f.Confidence = 0.5
		}
		if f.State == "" {
			f.State = model.StateOpen
		}
		f.File = model.RelPath(f.File)
		if f.ID == "" {
			f.ID = model.FindingID(f.File, f.StartLine, f.CWE, f.Rule)
		}
		// Attribute the finding to the package it came from, so the
		// dependency graph can show a finding count per node.
		if f.Package == "" {
			f.Package = packageForPath(pkgByDir, f.File)
		}
		out = append(out, f)
	}

	if _, err := m.deps.Store.Upsert(ctx, id, out); err != nil {
		return nil, err
	}
	if err := m.deps.Store.RecordScan(ctx, id, scanID, "sast", len(out), started, time.Now()); err != nil {
		m.emit(model.Event{Type: model.EventAuditError, AuditID: id, Error: err.Error()})
	}

	for i := range out {
		f := out[i]
		m.emit(model.Event{Type: model.EventFinding, AuditID: id, Finding: &f})
	}
	m.emit(model.Event{
		Type:    model.EventScanProgress,
		AuditID: id,
		Phase:   "sast",
		Percent: 1,
		Message: fmt.Sprintf("Found %d issues", len(out)),
	})
	return out, nil
}

// Graph returns the dependency graph, annotated with finding and advisory
// counts.
func (m *Manager) Graph(ctx context.Context, id string) (*model.Graph, error) {
	m.mu.RLock()
	a := m.audits[id]
	m.mu.RUnlock()
	if a == nil {
		return nil, findings.ErrNotFound
	}

	a.mu.RLock()
	pkgs := append([]model.Package(nil), a.packages...)
	edges := append([]model.GraphEdge(nil), a.edges...)
	a.mu.RUnlock()

	counts := map[string]int{}
	sev := map[string]model.Severity{}
	if m.deps.Store != nil {
		list, err := m.deps.Store.List(ctx, id, findings.Filter{})
		if err == nil {
			for _, f := range list {
				key := string(f.Package)
				if key == "" {
					continue
				}
				counts[key]++
				sev[key] = model.MaxSeverity(sev[key], f.Severity)
			}
		}
	}

	nodes := make([]model.GraphNode, 0, len(pkgs))
	for _, p := range pkgs {
		key := string(p.PURL)
		node := model.GraphNode{
			ID:           p.PURL,
			Label:        fmt.Sprintf("%s\nv%s", p.Name, p.Version),
			Name:         p.Name,
			Version:      p.Version,
			Direct:       p.Direct,
			MaxSeverity:  p.MaxSeverity,
			FindingCount: counts[key],
			VulnCount:    len(p.Vulnerabilities),
		}
		if s, ok := sev[key]; ok {
			node.MaxSeverity = model.MaxSeverity(node.MaxSeverity, s)
		}
		nodes = append(nodes, node)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Name < nodes[j].Name })

	return &model.Graph{Nodes: nodes, Edges: edges}, nil
}

// FileTree returns the workspace file tree with finding counts.
func (m *Manager) FileTree(ctx context.Context, id string) (*model.FileNode, error) {
	m.mu.RLock()
	a := m.audits[id]
	m.mu.RUnlock()
	if a == nil {
		return nil, findings.ErrNotFound
	}

	counts := map[string]int{}
	sev := map[string]model.Severity{}
	if m.deps.Store != nil {
		list, err := m.deps.Store.List(ctx, id, findings.Filter{})
		if err == nil {
			for _, f := range list {
				counts[f.File]++
				sev[f.File] = model.MaxSeverity(sev[f.File], f.Severity)
			}
		}
	}

	root := &model.FileNode{Name: filepath.Base(a.Root), Path: "", Type: "dir"}
	if err := buildTree(root, a.Root, a.Root, counts, sev); err != nil {
		return nil, err
	}
	return root, nil
}

// File returns the content of one workspace file plus its findings.
func (m *Manager) File(ctx context.Context, id, relPath string) (*model.FileContent, error) {
	m.mu.RLock()
	a := m.audits[id]
	m.mu.RUnlock()
	if a == nil {
		return nil, findings.ErrNotFound
	}

	abs, err := ResolvePath(a.Root, relPath)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, findings.ErrNotFound
	}

	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	out := &model.FileContent{
		Path:     model.RelPath(relPath),
		Language: languageFor(abs),
		Lines:    lines,
	}
	if m.deps.Store != nil {
		list, err := m.deps.Store.List(ctx, id, findings.Filter{Files: []string{model.RelPath(relPath)}})
		if err == nil {
			out.Findings = list
		}
	}
	return out, nil
}

// AST returns the parsed syntax tree for one file.
func (m *Manager) AST(ctx context.Context, id, relPath string) (*model.AST, error) {
	m.mu.RLock()
	a := m.audits[id]
	m.mu.RUnlock()
	if a == nil {
		return nil, findings.ErrNotFound
	}

	abs, err := ResolvePath(a.Root, relPath)
	if err != nil {
		return nil, err
	}
	f, err := ast.Parse(abs)
	if err != nil || f == nil {
		return nil, findings.ErrNotFound
	}
	return &model.AST{Path: model.RelPath(relPath), Nodes: f.Nodes, Edges: f.Edges}, nil
}

// Chunks returns the semantic chunks for one file.
func (m *Manager) Chunks(ctx context.Context, id, relPath string) ([]model.Chunk, error) {
	m.mu.RLock()
	a := m.audits[id]
	m.mu.RUnlock()
	if a == nil {
		return nil, findings.ErrNotFound
	}
	abs, err := ResolvePath(a.Root, relPath)
	if err != nil {
		return nil, err
	}
	f, err := ast.Parse(abs)
	if err != nil || f == nil {
		return nil, findings.ErrNotFound
	}
	return f.Chunks, nil
}

// Packages returns the resolved dependency packages of an audit.
func (m *Manager) Packages(ctx context.Context, id string) ([]model.Package, error) {
	m.mu.RLock()
	a := m.audits[id]
	m.mu.RUnlock()
	if a == nil {
		return nil, findings.ErrNotFound
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return append([]model.Package(nil), a.packages...), nil
}

// Snapshot returns the audit's public state.
func (m *Manager) Snapshot(id string) (*model.Audit, error) {
	m.mu.RLock()
	a := m.audits[id]
	m.mu.RUnlock()
	if a == nil {
		return nil, findings.ErrNotFound
	}
	return a.snapshot(), nil
}

// List returns all audits, newest first.
func (m *Manager) List() []*model.Audit {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*model.Audit, 0, len(m.audits))
	for _, a := range m.audits {
		out = append(out, a.snapshot())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// WorkspaceRoot returns the workspace directory of an audit.
func (m *Manager) WorkspaceRoot(id string) (string, error) {
	m.mu.RLock()
	a := m.audits[id]
	m.mu.RUnlock()
	if a == nil {
		return "", findings.ErrNotFound
	}
	return a.Root, nil
}

// ResolvePath resolves a workspace-relative path against the audit root and
// refuses anything that escapes it.
//
// This is the single chokepoint for path traversal: every file-serving
// endpoint goes through it.
func ResolvePath(root, rel string) (string, error) {
	clean := model.RelPath(rel)
	if clean == "" {
		return "", fmt.Errorf("empty path")
	}
	abs := filepath.Join(root, filepath.FromSlash(clean))
	rootAbs := filepath.Clean(root)
	if abs != rootAbs && !strings.HasPrefix(abs, rootAbs+string(os.PathSeparator)) {
		return "", fmt.Errorf("path escapes the audit workspace")
	}
	// Resolve symlinks where the path exists, to catch a symlink pointing
	// outside the workspace.
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		rootResolved, rerr := filepath.EvalSymlinks(rootAbs)
		if rerr == nil {
			if resolved != rootResolved && !strings.HasPrefix(resolved, rootResolved+string(os.PathSeparator)) {
				return "", fmt.Errorf("path escapes the audit workspace")
			}
		}
	}
	return abs, nil
}

func (a *Audit) snapshot() *model.Audit {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return &model.Audit{
		ID:           a.ID,
		Package:      a.Package,
		Version:      a.Version,
		Status:       a.status,
		Phase:        a.phase,
		Progress:     a.progress,
		Message:      a.message,
		Error:        a.failure,
		CreatedAt:    a.created,
		UpdatedAt:    a.updated,
		Root:         a.Root,
		FileCount:    len(a.files),
		ChunkCount:   totalChunks(a.files),
		FindingCount: 0,
	}
}

func (a *Audit) setStatus(status model.AuditStatus, phase string, progress float64, message string) {
	a.mu.Lock()
	a.status = status
	a.phase = phase
	a.progress = progress
	a.message = message
	a.updated = time.Now().UTC()
	a.mu.Unlock()
}

func (a *Audit) fail(err error) {
	a.mu.Lock()
	a.status = model.AuditFailed
	a.failure = err.Error()
	a.updated = time.Now().UTC()
	a.mu.Unlock()
}

func (m *Manager) emit(ev model.Event) {
	if m.deps.Emit != nil {
		m.deps.Emit(ev)
	}
}

func totalChunks(files []*ast.File) int {
	n := 0
	for _, f := range files {
		n += len(f.Chunks)
	}
	return n
}

// safeDirName builds a filesystem-safe directory name for a package.
func safeDirName(name, version string) string {
	clean := func(s string) string {
		s = strings.Map(func(r rune) rune {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
				return r
			case r == '.', r == '-', r == '_':
				return r
			default:
				return '-'
			}
		}, s)
		return strings.Trim(s, "-")
	}
	n := clean(name)
	v := clean(version)
	if n == "" {
		n = "package"
	}
	if v == "" {
		return n
	}
	return n + "-" + v
}

func languageFor(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".py":
		return "python"
	case ".toml":
		return "toml"
	case ".cfg", ".ini":
		return "ini"
	case ".json":
		return "json"
	case ".yaml", ".yml":
		return "yaml"
	case ".md":
		return "markdown"
	case ".txt":
		return "text"
	default:
		return ""
	}
}

// packageForPath returns the PURL of the package a workspace-relative file
// path belongs to.
//
// Workspace files live under "pkgs/<package directory>/...", so the package
// directory name identifies the owner. The mapping is recorded when the
// sources are acquired rather than parsed back out of the name, because a
// package name may itself contain a dash and the version may be a
// pre-release.
func packageForPath(pkgByDir map[string]model.PURL, file string) model.PURL {
	parts := strings.Split(file, "/")
	if len(parts) < 2 || parts[0] != "pkgs" {
		return ""
	}
	return pkgByDir[parts[1]]
}

// buildTree walks a directory into a model.FileNode tree, annotating counts.
// Paths are relative to root, which is the audit workspace root. They are the
// same paths the findings store uses, so a finding count lines up with the
// file it belongs to.
func buildTree(node *model.FileNode, dir, root string, counts map[string]int, sev map[string]model.Severity) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		full := filepath.Join(dir, name)
		rel := relToRoot(root, full)
		if e.IsDir() {
			child := &model.FileNode{Name: name, Path: rel, Type: "dir"}
			if err := buildTree(child, full, root, counts, sev); err != nil {
				continue
			}
			child.FindingCount = sumSubtree(child)
			node.Children = append(node.Children, child)
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		child := &model.FileNode{
			Name:         name,
			Path:         rel,
			Type:         "file",
			Size:         info.Size(),
			FindingCount: counts[rel],
			MaxSeverity:  sev[rel],
		}
		node.Children = append(node.Children, child)
	}
	sort.Slice(node.Children, func(i, j int) bool {
		if node.Children[i].Type != node.Children[j].Type {
			return node.Children[i].Type == "dir"
		}
		return node.Children[i].Name < node.Children[j].Name
	})
	return nil
}

func sumSubtree(n *model.FileNode) int {
	total := n.FindingCount
	for _, c := range n.Children {
		total += sumSubtree(c)
	}
	return total
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func newAuditID() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "a" + hex.EncodeToString(buf), nil
}
