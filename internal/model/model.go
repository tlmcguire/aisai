// Package model defines the canonical data types shared across the AISAI
// backend. These types are the single source of truth: the HTTP/WS API, the
// Electron renderer contract, SARIF/CycloneDX export, and the MCP tool
// surface all speak in terms of these structures.
//
// Design rules encoded here:
//   - Package identity is a PURL (pkg:pypi/name@version), never a bare name.
//     A package name alone cannot distinguish two versions of the same
//     dependency, and cannot be joined against OSV/CycloneDX/SPDX data.
//   - Every Finding carries a machine-joinable CWE id, a severity, and a
//     confidence score. Without those three, findings cannot be deduplicated,
//     triaged, or ranked.
//   - File paths are always relative to the audit workspace root, so a stored
//     finding survives the workspace being moved on disk.
package model

import (
	"fmt"
	"path"
	"strings"
	"time"
)

// Severity is the normalized severity scale used across every scanner.
type Severity string

const (
	// SeverityCritical is the most severe level. It is reserved for
	// findings that are both exploitable and severe, such as remote code
	// execution reachable from untrusted input.
	SeverityCritical Severity = "critical"
	// SeverityHigh is a serious finding, such as a command injection or a
	// deserialization of untrusted data.
	SeverityHigh Severity = "high"
	// SeverityMedium is a real weakness with a narrower precondition, such
	// as an injection that requires a privileged context.
	SeverityMedium Severity = "medium"
	// SeverityLow is a weakness with limited impact or an unlikely
	// precondition. The application counts low findings: filtering them
	// out is what made the legacy app and its benchmark disagree.
	SeverityLow Severity = "low"
	// SeverityInfo is an informational observation, such as a weak hash or
	// an outdated dependency reference.
	SeverityInfo Severity = "info"
)

// severityRank orders severities from most to least severe. Higher is worse.
var severityRank = map[Severity]int{
	SeverityCritical: 5,
	SeverityHigh:     4,
	SeverityMedium:   3,
	SeverityLow:      2,
	SeverityInfo:     1,
}

// Rank returns the numeric ordering of a severity. Unknown severities rank 0.
func (s Severity) Rank() int { return severityRank[s] }

// Valid reports whether s is a recognized severity value.
func (s Severity) Valid() bool {
	_, ok := severityRank[s]
	return ok
}

// AtLeast reports whether s is at least as severe as other.
func (s Severity) AtLeast(other Severity) bool { return s.Rank() >= other.Rank() }

// MaxSeverity returns the most severe of the two, treating an unknown value
// as less severe than a known one.
func MaxSeverity(a, b Severity) Severity {
	if b.Rank() > a.Rank() {
		return b
	}
	return a
}

// PURL is a Package URL: an ecosystem-agnostic component identifier.
// The canonical form used here is pkg:pypi/<name>@<version>.
type PURL string

// PackageURL builds a PURL for a PyPI package.
func PackageURL(name, version string) PURL {
	s := "pkg:pypi/" + strings.ToLower(name)
	if version != "" {
		s += "@" + version
	}
	return PURL(s)
}

// Name returns the package name portion of the PURL.
func (p PURL) Name() string {
	s := string(p)
	s = strings.TrimPrefix(s, "pkg:pypi/")
	if i := strings.Index(s, "@"); i >= 0 {
		s = s[:i]
	}
	return s
}

// Version returns the version portion of the PURL, or "" when absent.
func (p PURL) Version() string {
	s := string(p)
	if i := strings.Index(s, "@"); i >= 0 {
		return s[i+1:]
	}
	return ""
}

// String implements fmt.Stringer.
func (p PURL) String() string { return string(p) }

// Advisory is a known vulnerability affecting a package version.
type Advisory struct {
	ID       string   `json:"id"`     // e.g. GHSA-xxxx, PYSEC-xxxx, CVE-xxxx
	Source   string   `json:"source"` // osv | pypi
	Summary  string   `json:"summary"`
	Severity Severity `json:"severity"`
	CWE      []string `json:"cwe,omitempty"`
	FixedIn  []string `json:"fixedIn,omitempty"`
	URL      string   `json:"url,omitempty"`
	// Package is the PURL of the package the advisory affects. It is empty
	// in a flat list that is not scoped to an audit.
	Package PURL `json:"package,omitempty"`
}

// Package is one node of the dependency graph.
type Package struct {
	PURL            PURL       `json:"purl"`
	Name            string     `json:"name"`
	Version         string     `json:"version"`
	Ecosystem       string     `json:"ecosystem"`
	Direct          bool       `json:"direct"`
	LocalPath       string     `json:"localPath,omitempty"`
	Summary         string     `json:"summary,omitempty"`
	License         string     `json:"license,omitempty"`
	Homepage        string     `json:"homepage,omitempty"`
	Vulnerabilities []Advisory `json:"vulnerabilities,omitempty"`
	MaxSeverity     Severity   `json:"maxSeverity"`
}

// GraphNode is a renderable dependency node.
type GraphNode struct {
	ID           PURL     `json:"id"`
	Label        string   `json:"label"`
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Direct       bool     `json:"direct"`
	MaxSeverity  Severity `json:"maxSeverity"`
	FindingCount int      `json:"findingCount"`
	VulnCount    int      `json:"vulnCount"`
}

// GraphEdge is a dependency relationship from dependent to dependency.
type GraphEdge struct {
	Source PURL `json:"source"`
	Target PURL `json:"target"`
}

// Graph is the full dependency graph for an audit.
type Graph struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

// Evidence captures why a finding was reported.
type Evidence struct {
	Source string   `json:"source,omitempty"`
	Sink   string   `json:"sink,omitempty"`
	Flow   []string `json:"flow,omitempty"`
	Note   string   `json:"note,omitempty"`
}

// Reachability describes whether a finding's sink is reachable from an entry point.
const (
	ReachReachable   = "reachable"
	ReachUnreachable = "unreachable"
	ReachUnknown     = "unknown"
)

// FindingState is the triage state of a finding.
const (
	StateOpen       = "open"
	StateConfirmed  = "confirmed"
	StateSuppressed = "suppressed"
	StateFixed      = "fixed"
)

// Finding is a single vulnerability observation, regardless of which engine
// produced it.
type Finding struct {
	ID              string     `json:"id"`
	Rule            string     `json:"rule"`
	Tool            string     `json:"tool"`
	CWE             string     `json:"cwe,omitempty"`
	Severity        Severity   `json:"severity"`
	Confidence      float64    `json:"confidence"`
	File            string     `json:"file"`
	StartLine       int        `json:"startLine"`
	EndLine         int        `json:"endLine"`
	Code            string     `json:"code,omitempty"`
	Message         string     `json:"message"`
	Evidence        Evidence   `json:"evidence"`
	Remediation     string     `json:"remediation,omitempty"`
	Patch           string     `json:"patch,omitempty"`
	Reachability    string     `json:"reachability,omitempty"`
	State           string     `json:"state"`
	SuppressedUntil *time.Time `json:"suppressedUntil,omitempty"`
	Package         PURL       `json:"package,omitempty"`
	Verified        bool       `json:"verified"`
	VerifierNote    string     `json:"verifierNote,omitempty"`
}

// ChunkType enumerates the semantic boundaries used for chunking.
const (
	ChunkModule = "module"
	ChunkFile   = "file"
	ChunkClass  = "class"
	ChunkFunc   = "function"
	ChunkBlock  = "block"
)

// Chunk is a semantic unit of code: the retrieval and scanning unit.
type Chunk struct {
	ID         string   `json:"id"`
	AuditID    string   `json:"auditId"`
	File       string   `json:"file"`
	StartLine  int      `json:"startLine"`
	EndLine    int      `json:"endLine"`
	Type       string   `json:"type"`
	SymbolPath string   `json:"symbolPath"`
	Docstring  string   `json:"docstring,omitempty"`
	Content    string   `json:"content"`
	ParentID   string   `json:"parentId,omitempty"`
	Calls      []string `json:"calls,omitempty"`
	Imports    []string `json:"imports,omitempty"`
	Language   string   `json:"language,omitempty"`
}

// AuditStatus is the lifecycle state of an audit.
type AuditStatus string

const (
	// AuditPending means the audit has been accepted but has not started
	// fetching the package yet.
	AuditPending AuditStatus = "pending"
	// AuditAcquiring means the package source is being downloaded and
	// extracted.
	AuditAcquiring AuditStatus = "acquiring"
	// AuditIndexing means the source tree is being chunked and indexed for
	// retrieval.
	AuditIndexing AuditStatus = "indexing"
	// AuditScanning means the detection engines are running.
	AuditScanning AuditStatus = "scanning"
	// AuditReady means the audit finished and its findings are available.
	AuditReady AuditStatus = "ready"
	// AuditFailed means the audit stopped with an error. The reason is
	// carried by the audit's error field, not by the status.
	AuditFailed AuditStatus = "failed"
)

// Audit is one analysis of one package version.
type Audit struct {
	ID           string      `json:"id"`
	Package      string      `json:"package"`
	Version      string      `json:"version,omitempty"`
	Status       AuditStatus `json:"status"`
	Phase        string      `json:"phase,omitempty"`
	Progress     float64     `json:"progress"`
	Message      string      `json:"message,omitempty"`
	Error        string      `json:"error,omitempty"`
	CreatedAt    time.Time   `json:"createdAt"`
	UpdatedAt    time.Time   `json:"updatedAt"`
	Root         string      `json:"root"`
	FileCount    int         `json:"fileCount"`
	ChunkCount   int         `json:"chunkCount"`
	FindingCount int         `json:"findingCount"`
}

// FileNode is a node in the file tree.
type FileNode struct {
	Name         string      `json:"name"`
	Path         string      `json:"path"`
	Type         string      `json:"type"` // "dir" | "file"
	Size         int64       `json:"size,omitempty"`
	Children     []*FileNode `json:"children,omitempty"`
	FindingCount int         `json:"findingCount,omitempty"`
	MaxSeverity  Severity    `json:"maxSeverity,omitempty"`
}

// FileContent is a source file returned to the renderer.
type FileContent struct {
	Path     string    `json:"path"`
	Language string    `json:"language"`
	Lines    []string  `json:"lines"`
	Findings []Finding `json:"findings,omitempty"`
}

// Patch is a proposed fix for a finding.
//
// The unified diff is the point of the type: a fix that cannot be reviewed,
// applied and verified is just prose. See F§6.
type Patch struct {
	FindingID string `json:"findingId"`
	// Unified is the unified diff. Empty when the model produced prose
	// instead of a usable diff.
	Unified string `json:"unified"`
	// Valid reports whether the diff parses and applies to the current
	// content of the target file.
	Valid bool `json:"valid"`
	// Error explains why an invalid diff was rejected.
	Error string `json:"error"`
	// Summary is the prose remediation, shown when the diff is unusable.
	Summary string `json:"summary"`
}

// Change is a patch that was applied inside an audit workspace.
type Change struct {
	Path      string    `json:"path"`
	Unified   string    `json:"unified"`
	AppliedAt time.Time `json:"appliedAt"`
	// Backup is the workspace-relative path of the pre-patch copy, which
	// is what makes the change revertible.
	Backup string `json:"backup,omitempty"`
	// Reverted reports that the change was rolled back.
	Reverted bool `json:"reverted,omitempty"`
}

// Rescan is the difference between the findings of a file before and after
// a patch was applied.
//
// It is the evidence that a proposed fix actually fixed something.
type Rescan struct {
	// Resolved lists the ids of findings that are gone after the patch.
	Resolved []string `json:"resolved"`
	// Introduced lists the ids of findings the patch introduced.
	Introduced []string `json:"introduced"`
	// Unchanged counts the findings present both before and after.
	Unchanged int `json:"unchanged"`
}

// ASTNode is one node of a rendered syntax tree.
type ASTNode struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Label     string `json:"label"`
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
	Depth     int    `json:"depth"`
	ParentID  string `json:"parentId,omitempty"`
	Symbol    string `json:"symbol,omitempty"`
}

// ASTEdge is a parent/child relation between AST nodes.
type ASTEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

// AST is a rendered syntax tree.
type AST struct {
	Path  string    `json:"path"`
	Nodes []ASTNode `json:"nodes"`
	Edges []ASTEdge `json:"edges"`
}

// ProviderInfo describes the status of an LLM provider.
type ProviderInfo struct {
	ID     string   `json:"id"`
	Kind   string   `json:"kind"` // "ollama" | "openai"
	URL    string   `json:"url,omitempty"`
	OK     bool     `json:"ok"`
	Models []string `json:"models,omitempty"`
	Error  string   `json:"error,omitempty"`
}

// Config is the effective server configuration.
type Config struct {
	Workspace  string         `json:"workspace"`
	Providers  []ProviderInfo `json:"providers"`
	OllamaURL  string         `json:"ollamaUrl"`
	ChatModel  string         `json:"chatModel"`
	EmbedModel string         `json:"embedModel"`
	ScanModel  string         `json:"scanModel"`
	NumCtx     int            `json:"numCtx"`
}

// Event is a server-pushed event on the WebSocket channel.
type Event struct {
	Type    string      `json:"type"`
	AuditID string      `json:"auditId,omitempty"`
	JobID   string      `json:"jobId,omitempty"`
	Phase   string      `json:"phase,omitempty"`
	Percent float64     `json:"percent,omitempty"`
	Message string      `json:"message,omitempty"`
	Error   string      `json:"error,omitempty"`
	Finding *Finding    `json:"finding,omitempty"`
	Data    interface{} `json:"data,omitempty"`
}

// Event type constants.
const (
	EventAuditProgress = "audit.progress"
	EventAuditDone     = "audit.done"
	EventAuditError    = "audit.error"
	EventScanProgress  = "scan.progress"
	EventFinding       = "finding.created"
	EventAdvisories    = "advisories.ready"
	EventChatToken     = "chat.token"
	EventChatDone      = "chat.done"
	EventChatError     = "chat.error"
)

// RelPath normalizes a path to be relative and slash-separated, so that
// paths stored in findings stay stable and comparable regardless of the
// host OS. A leading slash is trimmed so the result is always relative;
// callers that need to resolve a path against a root should join it
// explicitly rather than relying on this function to preserve absoluteness.
func RelPath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.TrimPrefix(p, "./")
	p = strings.TrimLeft(p, "/")
	p = path.Clean(p)
	if p == "." {
		return ""
	}
	return p
}

// FindingID derives a stable identifier for a finding from its location and
// classification. The same vulnerability in the same file always produces the
// same id, which is what makes deduplication and re-scan diffing possible.
func FindingID(file string, startLine int, cwe, rule string) string {
	h := fnv64(fmt.Sprintf("%s:%d:%s:%s", RelPath(file), startLine, cwe, rule))
	return fmt.Sprintf("%s-%012x", sanitiseID(cweOrRule(cwe, rule)), h)
}

func cweOrRule(cwe, rule string) string {
	if cwe != "" {
		return cwe
	}
	return rule
}

func sanitiseID(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// fnv64 is a small FNV-1a hash used for stable identifier derivation.
func fnv64(s string) uint64 {
	const offset64 = 14695981039346656037
	const prime64 = 1099511628211
	var h uint64 = offset64
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime64
	}
	return h
}
