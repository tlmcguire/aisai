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
	"github.com/tlmcguire/aisai/internal/gitsrc"
	"github.com/tlmcguire/aisai/internal/index"
	"github.com/tlmcguire/aisai/internal/llm"
	"github.com/tlmcguire/aisai/internal/localsrc"
	"github.com/tlmcguire/aisai/internal/model"
	"github.com/tlmcguire/aisai/internal/purl"
	"github.com/tlmcguire/aisai/internal/pypi"
	"github.com/tlmcguire/aisai/internal/registry"
	"github.com/tlmcguire/aisai/internal/sast"
	"github.com/tlmcguire/aisai/internal/scan"
)

// Emitter receives progress events. It is normally the server's event hub.
type Emitter func(model.Event)

// Deps are the collaborators an audit Manager needs.
type Deps struct {
	Config *config.Config
	PyPI   *pypi.Client
	// Registries are the package indexes sources can be downloaded from. When
	// nil, only PyPI (through the PyPI client) is available.
	Registries *registry.Set
	Resolver   *deps.Resolver
	SAST       *sast.Engine
	Store      *findings.Store
	Index      *index.Index
	LLM        llm.Provider
	// Advisory queries for known vulnerabilities. It is nil when advisory
	// lookups are disabled.
	Advisory *advisory.Client
	// GitProtocols is git's transport allow-list for repository audits;
	// "https" when empty. Only tests change it.
	GitProtocols string
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

// The sources an audit can be created from.
const (
	// sourcePyPI is an audit of a package published on the package index.
	sourcePyPI = "pypi"
	// sourceLocal is an audit of a project folder on the local disk.
	sourceLocal = "local"
	// sourceGit is an audit of a cloned repository.
	sourceGit = "git"
)

// Audit is one in-flight or completed analysis.
type Audit struct {
	ID      string
	Package string
	Version string
	Root    string
	// ecosystem is the package index of a published package.
	ecosystem string
	// repo is the repository of a git audit.
	repo gitsrc.Repo

	// source says where the code came from: sourcePyPI or sourceLocal.
	source string
	// sourceDir is the folder a local audit was copied from. It never leaves
	// the backend.
	sourceDir string

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

	// filesByPath caches the parsed files by their workspace-relative path.
	// Opening the syntax tree of a file must not parse it again.
	filesByPath map[string]*ast.File

	// sandboxBytes is the size of the downloaded sources of the audit.
	sandboxBytes int64
	// sourcesDeleted reports that the downloaded sources were deleted.
	sourcesDeleted bool
	// advisoriesChecked is how many packages the last advisory lookup queried.
	advisoriesChecked int
	// truncated reports that a local folder was larger than the copy limits
	// (20,000 files or 256 MB), so only part of it is audited.
	truncated bool
	// skippedRepos are the nested repositories left out of a local folder.
	skippedRepos []string

	// findingCount is the number of findings the last scan stored.
	findingCount int

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

// Create allocates an audit of a published package and its workspace, but
// does not start work.
func (m *Manager) Create(ctx context.Context, pkg, version string) (*model.Audit, error) {
	return m.CreateFor(ctx, registry.PyPI, pkg, version)
}

// CreateFor allocates an audit of a package published on the index of an
// ecosystem (pypi, npm, cargo), but does not start work.
func (m *Manager) CreateFor(ctx context.Context, ecosystem, pkg, version string) (*model.Audit, error) {
	ecosystem = registry.Normalize(ecosystem)
	// PyPI is always accepted (its absence is reported when the audit runs,
	// as it always was); any other ecosystem must have a configured index.
	if ecosystem != registry.PyPI {
		if _, err := m.registry(ecosystem); err != nil {
			return nil, fmt.Errorf("%w: %s", ErrBadSource, err)
		}
	}
	return m.create(pkg, version, ecosystem, "")
}

// registry returns the index of an ecosystem, falling back to the PyPI client
// for PyPI when no registry set was configured.
func (m *Manager) registry(ecosystem string) (registry.Registry, error) {
	if m.deps.Registries != nil {
		return m.deps.Registries.Get(ecosystem)
	}
	if registry.Normalize(ecosystem) == registry.PyPI && m.deps.PyPI != nil {
		return registry.NewPyPI(m.deps.PyPI), nil
	}
	return nil, registry.ErrUnknownEcosystem
}

// CreateLocal allocates an audit of a project folder on the local disk and its
// workspace, but does not start work.
//
// The folder is validated here so that a bad choice is reported to the caller
// at once instead of as a failed audit. The files are copied when the audit
// runs: the audit works on the copy and never touches the original.
func (m *Manager) CreateLocal(ctx context.Context, dir string) (*model.Audit, error) {
	abs, err := localsrc.Validate(dir)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrBadSource, err)
	}
	return m.create(localsrc.Name(abs), "", sourceLocal, abs)
}

// CreateRepo allocates an audit of a git repository and its workspace, but
// does not start work. The repository is cloned when the audit runs.
func (m *Manager) CreateRepo(ctx context.Context, repo gitsrc.Repo) (*model.Audit, error) {
	snap, err := m.create(repo.Name, repo.Ref, sourceGit, "")
	if err != nil {
		return nil, err
	}
	m.mu.RLock()
	a := m.audits[snap.ID]
	m.mu.RUnlock()
	a.mu.Lock()
	a.repo = repo
	a.mu.Unlock()
	return snap, nil
}

// ErrBadSource is returned when the folder chosen for a local audit cannot be
// inspected.
var ErrBadSource = errors.New("cannot inspect that folder")

// create allocates an audit and its workspace.
func (m *Manager) create(pkg, version, source, sourceDir string) (*model.Audit, error) {
	id, err := newAuditID()
	if err != nil {
		return nil, err
	}
	root := filepath.Join(m.deps.Config.Workspace, id)
	if err := os.MkdirAll(filepath.Join(root, "pkgs"), 0o755); err != nil {
		return nil, fmt.Errorf("create audit workspace: %w", err)
	}

	eco := ""
	if source != sourceLocal && source != sourceGit {
		eco = source
	}
	a := &Audit{
		ID:        id,
		Package:   pkg,
		Version:   version,
		Root:      root,
		source:    source,
		ecosystem: eco,

		sourceDir: sourceDir,
		status:    model.AuditPending,
		created:   time.Now().UTC(),
		updated:   time.Now().UTC(),
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

	// Refuse a nearly full disk up front, and explain a disk that fills up
	// half way, instead of failing with a bare write error.
	if err := checkDiskSpace(a.Root); err != nil {
		a.fail(err)
		return err
	}
	err := m.run(ctx, a)
	if explained := explainDiskFull(err, a.Root); explained != err {
		_ = os.RemoveAll(sandboxDir(a.Root))
		a.fail(explained)
		return explained
	}
	return err
}

// run is Run after the checks.
func (m *Manager) run(ctx context.Context, a *Audit) error {

	if a.source == sourceLocal {
		return m.runLocal(ctx, a)
	}
	if a.source == sourceGit {
		return m.runGit(ctx, a)
	}

	a.setStatus(model.AuditAcquiring, "acquiring", 0.05, "Resolving dependencies")

	if m.deps.Resolver == nil {
		err := errors.New("no dependency resolver configured")
		a.fail(err)
		return err
	}

	eco := a.ecosystem
	if eco == "" {
		eco = registry.PyPI
	}
	reg, err := m.registry(eco)
	if err != nil {
		a.fail(err)
		return err
	}
	if a.Version == "" && eco != registry.PyPI {
		// Only PyPI resolves its own latest version; the others are asked.
		info, lerr := reg.Lookup(ctx, a.Package)
		if lerr != nil {
			a.fail(fmt.Errorf("look up %s: %w", a.Package, lerr))
			return lerr
		}
		a.mu.Lock()
		a.Version = info.Latest
		a.Package = info.Name
		a.mu.Unlock()
	}

	res, err := m.deps.Resolver.ResolveFor(ctx, eco, a.Package, a.Version)
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

	return m.finishRun(ctx, a, files)
}

// runGit clones a repository, copies its source files into the sandbox, parses
// them and makes the audit ready.
//
// The clone is made in the scratch space outside the sandbox and then copied
// through the same snapshot as a local folder, which leaves out .git, symlinks,
// dependency folders and large files and caps the size. Nothing is executed.
func (m *Manager) runGit(ctx context.Context, a *Audit) error {
	a.mu.RLock()
	repo := a.repo
	a.mu.RUnlock()
	a.setStatus(model.AuditAcquiring, "acquiring", 0.1, "Cloning "+repo.Display())
	m.emit(model.Event{Type: model.EventAuditProgress, AuditID: a.ID, Phase: "acquiring", Percent: 0.1, Message: "Cloning " + repo.Display()})

	scratchRoot := downloadDir(a.Root)
	if err := os.MkdirAll(scratchRoot, 0o700); err != nil {
		a.fail(err)
		return err
	}
	defer os.RemoveAll(scratchRoot)
	scratch := filepath.Join(scratchRoot, "clone")
	if err := gitsrc.Clone(ctx, repo, scratch, gitsrc.Options{Protocols: m.deps.GitProtocols}); err != nil {
		a.fail(err)
		return err
	}

	dirName := safeDirName(repo.Name, "")
	dest := filepath.Join(sandboxDir(a.Root), dirName)
	res, err := localsrc.Snapshot(ctx, scratch, dest, isInterestingTextFile, localsrc.Limits{})
	if err != nil {
		a.fail(fmt.Errorf("copy the repository: %w", err))
		return err
	}
	if res.Files == 0 {
		err := errors.New("no source files were found in that repository")
		a.fail(err)
		return err
	}
	if err := hardenSandbox(dest); err != nil {
		a.fail(fmt.Errorf("harden the copy: %w", err))
		return err
	}
	msg := fmt.Sprintf("Cloned %d files", res.Files)
	if res.Truncated {
		msg += " (the repository is larger than the limit, so the copy is partial)"
	}
	a.setStatus(model.AuditAcquiring, "acquiring", 0.4, msg)
	m.emit(model.Event{Type: model.EventAuditProgress, AuditID: a.ID, Phase: "acquiring", Percent: 0.4, Message: msg})

	files, err := parseTree(dest, a.Root, a.ID)
	if err != nil {
		a.fail(fmt.Errorf("parse the repository: %w", err))
		return err
	}

	version := repo.Ref
	if version == "" {
		version = "HEAD"
	}
	purl := model.PURL("pkg:generic/" + strings.ToLower(dirName))
	a.mu.Lock()
	a.packages = []model.Package{{PURL: purl, Name: repo.Name, Version: version, Ecosystem: "git", Direct: true}}
	a.edges = nil
	a.pkgByDir = map[string]model.PURL{dirName: purl}
	a.sandboxBytes = sandboxSize(a.Root)
	a.mu.Unlock()

	return m.finishRun(ctx, a, files)
}

// runLocal copies a project folder into the workspace, parses it and makes the
// audit ready.
//
// A local audit downloads nothing: its graph is the project plus the
// dependencies its manifests declare, unresolved.
func (m *Manager) runLocal(ctx context.Context, a *Audit) error {
	a.setStatus(model.AuditAcquiring, "acquiring", 0.1, "Copying the project")

	dirName := safeDirName(a.Package, "")
	dest := filepath.Join(sandboxDir(a.Root), dirName)
	res, err := localsrc.Snapshot(ctx, a.sourceDir, dest, isInterestingTextFile, localsrc.Limits{})
	if err != nil {
		a.fail(fmt.Errorf("copy the project: %w", err))
		return err
	}
	if res.Files == 0 {
		err := errors.New("no source files were found in that folder")
		a.fail(err)
		return err
	}
	if err := hardenSandbox(dest); err != nil {
		a.fail(fmt.Errorf("harden the copy: %w", err))
		return err
	}

	msg := fmt.Sprintf("Copied %d files", res.Files)
	if res.Truncated {
		msg += " (the folder is larger than the limit, so the copy is partial)"
	}
	a.setStatus(model.AuditAcquiring, "acquiring", 0.4, msg)
	m.emit(model.Event{Type: model.EventAuditProgress, AuditID: a.ID, Phase: "acquiring", Percent: 0.4, Message: msg})

	files, err := parseTree(dest, a.Root, a.ID)
	if err != nil {
		a.fail(fmt.Errorf("parse the project: %w", err))
		return err
	}

	purl := model.PURL("pkg:generic/" + strings.ToLower(dirName))
	a.mu.Lock()
	// The graph is the project and its dependencies. A lockfile gives the whole
	// resolved tree with exact versions, transitive dependencies included; with
	// none, the graph is only what the manifests declare, one level deep.
	declared, _ := readLocalDeps(a.sourceDir)
	rootPkg := model.Package{
		PURL:      purl,
		Name:      a.Package,
		Version:   "local",
		Ecosystem: "local",
		Direct:    true,
	}
	if pkgs, edges, ok := lockedGraph(rootPkg, readLockfiles(a.sourceDir, declared)); ok {
		a.packages, a.edges = pkgs, edges
	} else {
		a.packages, a.edges = localGraph(rootPkg, declared)
	}
	a.pkgByDir = map[string]model.PURL{dirName: purl}
	a.sandboxBytes = sandboxSize(a.Root)
	a.truncated = res.Truncated
	a.skippedRepos = res.NestedRepos
	a.mu.Unlock()

	return m.finishRun(ctx, a, files)
}

// finishRun stores the parsed files of an audit, builds the retrieval index
// and marks the audit ready. Both kinds of audit end here.
func (m *Manager) finishRun(ctx context.Context, a *Audit, files []*ast.File) error {
	a.mu.Lock()
	a.files = files
	a.filesByPath = make(map[string]*ast.File, len(files))
	for _, f := range files {
		a.filesByPath[f.Path] = f
	}
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
	if m.deps.Advisory != nil && a.source != sourceLocal && a.source != sourceGit {
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

// acquireDownloads is the number of packages that are fetched at the same
// time.
//
// The downloads dominate the time an audit takes: each one is a round trip
// to the package index, and a package with twenty dependencies would spend
// most of the audit waiting on the network. Fetching a few at a time turns
// that waiting into overlapping waiting.
const acquireDownloads = 4

// downloadState is the payload of a per-package download event.
func downloadState(p model.Package, state, detail string, done, total int, bytes int64) map[string]any {
	return map[string]any{
		"download": map[string]any{
			"package": p.Name,
			"version": p.Version,
			"state":   state,
			"detail":  detail,
			"done":    done,
			"total":   total,
			"bytes":   bytes,
		},
	}
}

// acquire downloads and safely extracts the source of every resolved package.
func (m *Manager) acquire(ctx context.Context, a *Audit, res *deps.Resolution) ([]*ast.File, error) {
	pkgs := append([]model.Package(nil), res.Packages...)
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].Name < pkgs[j].Name })

	// A full disk stops every download at once: carrying on would skip each
	// remaining package with the same error and leave a broken audit.
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	var diskErr error

	type fetched struct {
		files []*ast.File
	}

	var (
		wg        sync.WaitGroup
		sem       = make(chan struct{}, acquireDownloads)
		results   = make([]fetched, len(pkgs))
		mu        sync.Mutex
		completed int
	)

	pkgByDir := make(map[string]model.PURL, len(pkgs))
	for i, p := range pkgs {
		dirName := safeDirName(p.Name, p.Version)
		// Record the directory before the download: even a package whose
		// source cannot be fetched still belongs in the graph.
		pkgByDir[dirName] = p.PURL

		wg.Add(1)
		go func(i int, p model.Package, dirName string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if ctx.Err() != nil {
				return
			}

			// The app's download tracker lists every package with its state.
			m.emit(model.Event{
				Type:    model.EventAuditProgress,
				AuditID: a.ID,
				Phase:   "acquiring",
				Message: fmt.Sprintf("Downloading %s %s", p.Name, p.Version),
				Data:    downloadState(p, "downloading", "", 0, len(pkgs), 0),
			})

			files, size, err := m.fetchPackage(ctx, a, p, dirName)
			if err != nil && isNoSpace(err) {
				mu.Lock()
				if diskErr == nil {
					diskErr = err
				}
				mu.Unlock()
				stop()
			}

			mu.Lock()
			completed++
			done := completed
			if len(files) > 0 {
				results[i] = fetched{files: files}
			}
			mu.Unlock()

			percent := 0.2 + 0.45*float64(done)/float64(max(1, len(pkgs)))
			switch {
			case err != nil && len(files) == 0:
				// A package we cannot fetch is not fatal — the rest of the
				// graph is still worth scanning. Record and continue.
				m.emit(model.Event{
					Type:    model.EventAuditProgress,
					AuditID: a.ID,
					Phase:   "acquiring",
					Percent: percent,
					Message: fmt.Sprintf("Skipped %s: %v", p.Name, err),
					Data:    downloadState(p, "failed", err.Error(), done, len(pkgs), size),
				})
			case err != nil:
				m.emit(model.Event{
					Type:    model.EventAuditProgress,
					AuditID: a.ID,
					Phase:   "acquiring",
					Percent: percent,
					Message: fmt.Sprintf("Fetched %s with warnings: %v", p.Name, err),
					Data:    downloadState(p, "warning", err.Error(), done, len(pkgs), size),
				})
			default:
				m.emit(model.Event{
					Type:    model.EventAuditProgress,
					AuditID: a.ID,
					Phase:   "acquiring",
					Percent: percent,
					Message: fmt.Sprintf("Fetched %s %s", p.Name, p.Version),
					Data:    downloadState(p, "done", "", done, len(pkgs), size),
				})
			}
		}(i, p, dirName)
	}
	wg.Wait()

	// The scratch space of the downloads is not needed any more.
	_ = os.RemoveAll(downloadDir(a.Root))

	if diskErr != nil {
		return nil, explainDiskFull(diskErr, a.Root)
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var files []*ast.File
	for _, r := range results {
		files = append(files, r.files...)
	}

	a.mu.Lock()
	a.pkgByDir = pkgByDir
	a.sandboxBytes = sandboxSize(a.Root)
	a.mu.Unlock()

	return files, nil
}

// fetchPackage downloads, extracts and parses the source of one package.
//
// The archive is downloaded into a scratch space outside the sandbox and
// deleted as soon as it has been extracted, so the sandbox only ever holds
// source files. The extracted files are hardened before they are parsed:
// nothing that came out of an archive is allowed to be executable.
func (m *Manager) fetchPackage(ctx context.Context, a *Audit, p model.Package, dirName string) ([]*ast.File, int64, error) {
	dest := filepath.Join(sandboxDir(a.Root), dirName)
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return nil, 0, err
	}
	scratch := filepath.Join(downloadDir(a.Root), dirName)
	if err := os.MkdirAll(scratch, 0o700); err != nil {
		return nil, 0, err
	}
	defer os.RemoveAll(scratch)

	eco := p.Ecosystem
	if eco == "" {
		eco = a.ecosystem
	}
	reg, err := m.registry(eco)
	if err != nil {
		return nil, 0, err
	}
	tarball, _, err := reg.Download(ctx, p.Name, p.Version, scratch)
	if err != nil {
		return nil, 0, err
	}
	var size int64
	if info, statErr := os.Stat(tarball); statErr == nil {
		size = info.Size()
	}
	if err := extractTarball(tarball, dest); err != nil {
		return nil, 0, err
	}
	// A source distribution wraps everything in a directory named after the
	// project; the file tree is easier to read without it.
	if err := flattenSingleRoot(dest); err != nil {
		return nil, 0, err
	}
	if err := hardenSandbox(dest); err != nil {
		return nil, 0, err
	}
	parsed, err := parseTree(dest, a.Root, a.ID)
	if err != nil {
		return parsed, size, err
	}
	return parsed, size, nil
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
	if m.deps.SAST == nil {
		return nil, errors.New("no static analysis engine configured")
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
	// The audit reports how many findings it holds, which includes the ones
	// that earlier scans stored.
	total := len(out)
	if list, err := m.deps.Store.List(ctx, id, findings.Filter{}); err == nil {
		total = len(list)
	}
	a.mu.Lock()
	a.findingCount = total
	a.mu.Unlock()
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
	rootName := purl.NormalizeName(a.Package)
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
			Root:         purl.NormalizeName(p.Name) == rootName,
			MaxSeverity:  p.MaxSeverity,
			FindingCount: counts[key],
			VulnCount:    len(p.Vulnerabilities),
		}
		if s, ok := sev[key]; ok {
			node.MaxSeverity = model.MaxSeverity(node.MaxSeverity, s)
		}
		nodes = append(nodes, node)
	}
	// Count the vulnerable packages below each package, so a dependency that
	// brings one in shows it even when it is itself clean.
	vulnerable := make([]model.PURL, 0)
	for _, p := range pkgs {
		if len(p.Vulnerabilities) > 0 {
			vulnerable = append(vulnerable, p.PURL)
		}
	}
	if len(vulnerable) > 0 {
		below := transitiveVulnCounts(edges, vulnerable)
		for i := range nodes {
			nodes[i].TransitiveVulns = below[nodes[i].ID]
		}
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
	rel := model.RelPath(relPath)
	f := m.parsedFile(a, rel, abs)
	if f == nil {
		return nil, findings.ErrNotFound
	}
	return &model.AST{Path: rel, Language: f.Language, Nodes: f.Nodes, Edges: f.Edges}, nil
}

// parsedFile returns the parsed file of a workspace path.
//
// The files parsed during the acquisition are cached, so opening the syntax
// tree of a file does not parse it a second time. A file that was not part
// of the acquisition — one that was patched, or one that was added to the
// workspace — is parsed on demand and cached as well.
func (m *Manager) parsedFile(a *Audit, rel, abs string) *ast.File {
	a.mu.RLock()
	cached, ok := a.filesByPath[rel]
	a.mu.RUnlock()
	if ok {
		return cached
	}
	f, err := ast.Parse(abs)
	if err != nil || f == nil {
		return nil
	}
	f.Path = rel
	for i := range f.Chunks {
		f.Chunks[i].AuditID = a.ID
		f.Chunks[i].File = rel
		f.Chunks[i].ID = model.FindingID(rel, f.Chunks[i].StartLine, "", f.Chunks[i].SymbolPath)
	}
	a.mu.Lock()
	if a.filesByPath == nil {
		a.filesByPath = map[string]*ast.File{}
	}
	a.filesByPath[rel] = f
	a.mu.Unlock()
	return f
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
	rel := model.RelPath(relPath)
	f := m.parsedFile(a, rel, abs)
	if f == nil {
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

// DeleteSources removes the downloaded sources of an audit.
//
// The findings, the graph and the report of the audit are kept: only the
// sandbox of downloaded code is removed, so the disk space is freed without
// losing the result of the inspection.
//
// It returns the number of bytes that were freed.
func (m *Manager) DeleteSources(ctx context.Context, id string) (int64, error) {
	m.mu.RLock()
	a := m.audits[id]
	m.mu.RUnlock()
	if a == nil {
		return 0, findings.ErrNotFound
	}

	size := sandboxSize(a.Root)
	if err := os.RemoveAll(sandboxDir(a.Root)); err != nil {
		return 0, fmt.Errorf("delete audit sources: %w", err)
	}
	// The scratch space of the downloads goes with them.
	if err := os.RemoveAll(downloadDir(a.Root)); err != nil {
		return 0, fmt.Errorf("delete audit downloads: %w", err)
	}

	a.mu.Lock()
	a.files = nil
	a.filesByPath = nil
	a.sourcesDeleted = true
	a.sandboxBytes = 0
	a.updated = time.Now().UTC()
	a.mu.Unlock()

	return size, nil
}

// SandboxSize returns the size of the downloaded sources of an audit.
func (m *Manager) SandboxSize(id string) (int64, error) {
	m.mu.RLock()
	a := m.audits[id]
	m.mu.RUnlock()
	if a == nil {
		return 0, findings.ErrNotFound
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.sourcesDeleted {
		return 0, nil
	}
	return sandboxSize(a.Root), nil
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
		FindingCount: a.findingCount,
		Source:       a.source,
		Ecosystem:    a.ecosystem,

		SandboxBytes:   a.sandboxBytes,
		SourcesDeleted: a.sourcesDeleted,
		Truncated:      a.truncated,
		SkippedRepos:   append([]string(nil), a.skippedRepos...),
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
	if lang := ast.LanguageForPath(path); lang != nil {
		return lang.ID
	}
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

// ErrNotLocal is returned when a debugger is asked for on code that was not
// opened from a folder on this computer.
var ErrNotLocal = errors.New("the debugger runs code, so it is only offered for project folders you opened yourself, not for downloaded packages")

// DebugTarget is where a local project can be run under the debugger.
type DebugTarget struct {
	// Root is the audit workspace; every path the renderer sends is
	// relative to it.
	Root string
	// ProjectDir is the copy of the project inside the workspace. The program
	// runs here, so the code that runs is the code the editor shows.
	ProjectDir string
	// SourceDir is the original folder. Only its virtual environment is
	// used, to find the project's own interpreter.
	SourceDir string
}

// DebugTarget returns where the project of a local audit can be debugged. It
// refuses any audit that is not a local folder: a downloaded package is
// untrusted and is never run.
func (m *Manager) DebugTarget(id string) (DebugTarget, error) {
	m.mu.RLock()
	a := m.audits[id]
	m.mu.RUnlock()
	if a == nil {
		return DebugTarget{}, findings.ErrNotFound
	}
	if a.source != sourceLocal || a.sourceDir == "" {
		return DebugTarget{}, ErrNotLocal
	}
	snap := a.snapshot()
	if snap.SourcesDeleted {
		return DebugTarget{}, errors.New("the copy of the project was deleted; open the folder again to debug it")
	}
	return DebugTarget{
		Root:       a.Root,
		ProjectDir: filepath.Join(sandboxDir(a.Root), safeDirName(a.Package, "")),
		SourceDir:  a.sourceDir,
	}, nil
}
