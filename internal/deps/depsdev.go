// Package deps resolves the dependency graph of a PyPI package.
//
// The primary source is deps.dev, whose resolution matches what the Python
// packaging tools would install. When deps.dev cannot answer, the resolver
// falls back to walking the PyPI `requires_dist` metadata recursively. A
// partial tree is never returned silently: when both sources fail the
// resolver returns an error describing both failures.
package deps

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/tlmcguire/aisai/internal/model"
	"github.com/tlmcguire/aisai/internal/purl"
	"github.com/tlmcguire/aisai/internal/pypi"
)

// DefaultBaseURL is the public deps.dev API endpoint.
const DefaultBaseURL = "https://api.deps.dev"

// DefaultTimeout is the default request timeout of a Resolver.
const DefaultTimeout = 30 * time.Second

// Ecosystem is the ecosystem label of every resolved package.
const Ecosystem = "pypi"

// Resolution sources.
const (
	// SourceDepsDev marks a resolution that came from deps.dev.
	SourceDepsDev = "deps.dev"
	// SourcePyPIMetadata marks a resolution derived from PyPI metadata.
	SourcePyPIMetadata = "pypi-metadata"
)

// maxFallbackDepth bounds the recursion of the PyPI metadata fallback.
const maxFallbackDepth = 25

// maxFallbackPackages bounds the number of packages the fallback collects.
const maxFallbackPackages = 2000

// Options configures a Resolver.
type Options struct {
	// BaseURL is the deps.dev API root. It defaults to "https://api.deps.dev".
	BaseURL string
	// Timeout bounds a single request. It defaults to 30s.
	Timeout time.Duration
	// UserAgent is sent with every request.
	UserAgent string
	// PyPI is the client used for metadata lookups. When nil a default
	// client is created.
	PyPI *pypi.Client
}

// Resolution is the resolved dependency graph of one root package.
type Resolution struct {
	// Packages are the resolved packages, including the root package.
	Packages []model.Package
	// Edges are the dependency relations, from dependent to dependency.
	Edges []model.GraphEdge
	// Conflicts describes packages that appear with more than one version.
	Conflicts []string
	// Source records which resolver produced the graph.
	Source string
}

// Resolver resolves dependency graphs.
type Resolver struct {
	baseURL   string
	userAgent string
	http      *http.Client
	pypi      *pypi.Client
}

// NewResolver returns a resolver configured by opts.
func NewResolver(opts Options) *Resolver {
	base := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if base == "" {
		base = DefaultBaseURL
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	agent := strings.TrimSpace(opts.UserAgent)
	if agent == "" {
		agent = "aisai/0.1 (+https://github.com/tlmcguire/aisai)"
	}
	client := opts.PyPI
	if client == nil {
		client = pypi.NewClient(pypi.ClientOptions{Timeout: timeout, UserAgent: agent})
	}
	return &Resolver{
		baseURL:   base,
		userAgent: agent,
		http:      &http.Client{Timeout: timeout},
		pypi:      client,
	}
}

// Resolve returns the resolved dependency graph for name@version.
//
// The root package is included in the returned packages and is the only
// package marked as a direct dependency.
func (r *Resolver) Resolve(ctx context.Context, name, version string) (*Resolution, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("deps: package name is required")
	}
	if strings.TrimSpace(version) == "" {
		resolved, err := r.pypi.Package(ctx, name, "")
		if err != nil {
			return nil, fmt.Errorf("deps: cannot determine the latest version of %q: %w", name, err)
		}
		version = resolved.Version
	}

	res, primaryErr := r.resolveFromDepsDev(ctx, name, version)
	if primaryErr == nil {
		return res, nil
	}
	log.Printf("deps: deps.dev resolution failed for %s@%s: %v; falling back to PyPI metadata", name, version, primaryErr)

	res, fallbackErr := r.resolveFromPyPIMetadata(ctx, name, version)
	if fallbackErr == nil {
		return res, nil
	}
	return nil, fmt.Errorf("deps: cannot resolve %s@%s: deps.dev: %v; pypi metadata fallback: %w",
		name, version, primaryErr, fallbackErr)
}

// ---------------------------------------------------------------------------
// deps.dev
// ---------------------------------------------------------------------------

// depsVersionKey identifies one package version in a deps.dev graph.
type depsVersionKey struct {
	System  string `json:"system"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

// depsChild is a dependency node of a deps.dev graph node.
type depsChild struct {
	VersionKey  depsVersionKey `json:"versionKey"`
	Dependency  string         `json:"dependency"`
	Requirement string         `json:"requirement"`
}

// depsEdge is an edge of a deps.dev graph node.
type depsEdge struct {
	From        depsVersionKey `json:"from"`
	To          depsVersionKey `json:"to"`
	FromNode    depsVersionKey `json:"fromNode"`
	ToNode      depsVersionKey `json:"toNode"`
	Dependency  string         `json:"dependency"`
	Requirement string         `json:"requirement"`
}

// depsNode is one node of a deps.dev graph.
//
// The ":dependencies" endpoint returns a flat graph: every node is listed
// once with a "relation" of SELF, DIRECT or INDIRECT, and the edges are a
// separate top-level array that refers to nodes by index. The nested
// "nodes"/"edges" fields are accepted too, because other deps.dev endpoints
// use that shape.
type depsNode struct {
	VersionKey depsVersionKey `json:"versionKey"`
	Relation   string         `json:"relation"`
	Bundled    bool           `json:"bundled"`
	Nodes      []depsChild    `json:"nodes"`
	Edges      []depsEdge     `json:"edges"`
}

// depsFlatEdge is an edge of the flat deps.dev graph. It refers to the
// endpoints by their index in the top-level nodes array.
type depsFlatEdge struct {
	FromNode    int    `json:"fromNode"`
	ToNode      int    `json:"toNode"`
	Requirement string `json:"requirement"`
}

// depsResponse is the deps.dev v3 dependencies response.
type depsResponse struct {
	Nodes []depsNode     `json:"nodes"`
	Edges []depsFlatEdge `json:"edges"`
	Error string         `json:"error"`
}

// resolveFromDepsDev resolves the graph through the deps.dev API.
func (r *Resolver) resolveFromDepsDev(ctx context.Context, name, version string) (*Resolution, error) {
	endpoint := fmt.Sprintf("%s/v3/systems/PYPI/packages/%s/versions/%s:dependencies",
		r.baseURL, url.PathEscape(name), url.PathEscape(version))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("deps: build request for %s: %w", endpoint, err)
	}
	req.Header.Set("User-Agent", r.userAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := r.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("deps: GET %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("deps: GET %s: not found", endpoint)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("deps: GET %s: unexpected status %s", endpoint, resp.Status)
	}
	var payload depsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("deps: decode %s: %w", endpoint, err)
	}
	return resolutionFromDepsResponse(&payload, name, version)
}

// resolutionFromDepsResponse converts a deps.dev graph into a Resolution.
func resolutionFromDepsResponse(payload *depsResponse, name, version string) (*Resolution, error) {
	if len(payload.Nodes) == 0 {
		return nil, fmt.Errorf("deps: deps.dev returned no nodes for %s@%s", name, version)
	}

	// Index every node by its version key and collect all edges.
	nodes := map[string]depsNode{}
	edges := map[string][]model.GraphEdge{}
	for _, node := range payload.Nodes {
		key := versionKeyID(node.VersionKey)
		if key == "" {
			continue
		}
		if _, seen := nodes[key]; !seen {
			nodes[key] = node
		}
		for _, child := range node.Nodes {
			target := versionKeyID(child.VersionKey)
			if target == "" {
				continue
			}
			edges[key] = append(edges[key], model.GraphEdge{
				Source: model.PURL(purl.New(node.VersionKey.Name, node.VersionKey.Version)),
				Target: model.PURL(purl.New(child.VersionKey.Name, child.VersionKey.Version)),
			})
		}
		for _, edge := range node.Edges {
			from := edge.From
			if from.Name == "" {
				from = edge.FromNode
			}
			to := edge.To
			if to.Name == "" {
				to = edge.ToNode
			}
			fromKey := versionKeyID(from)
			toKey := versionKeyID(to)
			if fromKey == "" || toKey == "" {
				continue
			}
			edges[fromKey] = append(edges[fromKey], model.GraphEdge{
				Source: model.PURL(purl.New(from.Name, from.Version)),
				Target: model.PURL(purl.New(to.Name, to.Version)),
			})
		}
	}

	// The ":dependencies" endpoint returns a flat graph whose edges live at
	// the top level and refer to the endpoints by their index in the nodes
	// array. Without this the whole transitive tree is silently dropped.
	for _, flat := range payload.Edges {
		if flat.FromNode < 0 || flat.ToNode < 0 {
			continue
		}
		if flat.FromNode >= len(payload.Nodes) || flat.ToNode >= len(payload.Nodes) {
			continue
		}
		from := payload.Nodes[flat.FromNode].VersionKey
		to := payload.Nodes[flat.ToNode].VersionKey
		fromKey := versionKeyID(from)
		toKey := versionKeyID(to)
		if fromKey == "" || toKey == "" {
			continue
		}
		edges[fromKey] = append(edges[fromKey], model.GraphEdge{
			Source: model.PURL(purl.New(from.Name, from.Version)),
			Target: model.PURL(purl.New(to.Name, to.Version)),
		})
	}

	rootKey := findRootKey(nodes, name, version)
	if rootKey == "" {
		return nil, fmt.Errorf("deps: deps.dev response does not contain %s@%s", name, version)
	}

	builder := newGraphBuilder(name, version)
	builder.addPackage(name, version, true)

	// deps.dev marks the root's immediate dependencies with a relation of
	// DIRECT; everything else is INDIRECT.
	directRel := map[string]bool{}
	for _, node := range payload.Nodes {
		if strings.EqualFold(node.Relation, "DIRECT") {
			directRel[packageKey(node.VersionKey.Name, node.VersionKey.Version)] = true
		}
	}

	visited := map[string]bool{rootKey: true}
	queue := []string{rootKey}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if _, ok := nodes[current]; !ok {
			continue
		}
		for _, edge := range edges[current] {
			builder.addEdge(edge)
			targetName := edge.Target.Name()
			targetVersion := edge.Target.Version()
			targetKey := packageKey(targetName, targetVersion)
			builder.addPackage(targetName, targetVersion, directRel[targetKey])
			if !visited[targetKey] {
				visited[targetKey] = true
				queue = append(queue, targetKey)
			}
		}
	}
	builder.source = SourceDepsDev
	return builder.build(), nil
}

// findRootKey locates the node matching the requested root package.
func findRootKey(nodes map[string]depsNode, name, version string) string {
	want := purl.NormalizeName(name)
	for key, node := range nodes {
		if purl.NormalizeName(node.VersionKey.Name) != want {
			continue
		}
		if version == "" || node.VersionKey.Version == version {
			return key
		}
	}
	// Fall back to a name-only match so a slightly different version
	// spelling still resolves.
	for key, node := range nodes {
		if purl.NormalizeName(node.VersionKey.Name) == want {
			return key
		}
	}
	return ""
}

// versionKeyID builds the identity of a deps.dev version key.
func versionKeyID(key depsVersionKey) string {
	if key.Name == "" {
		return ""
	}
	return packageKey(key.Name, key.Version)
}

// packageKey builds the identity of a package name and version.
func packageKey(name, version string) string {
	return purl.NormalizeName(name) + "@" + version
}

// ---------------------------------------------------------------------------
// Graph assembly
// ---------------------------------------------------------------------------

// graphBuilder accumulates packages, edges and version conflicts.
type graphBuilder struct {
	packages  []model.Package
	seen      map[string]bool
	edges     []model.GraphEdge
	edgeSeen  map[string]bool
	versions  map[string]map[string]bool
	conflicts []string
	source    string
}

// newGraphBuilder returns a builder for a graph rooted at name@version.
func newGraphBuilder(name, version string) *graphBuilder {
	return &graphBuilder{
		seen:     map[string]bool{},
		edgeSeen: map[string]bool{},
		versions: map[string]map[string]bool{},
	}
}

// addPackage records a package, tracking version conflicts per name.
func (b *graphBuilder) addPackage(name, version string, direct bool) {
	key := packageKey(name, version)
	if b.seen[key] {
		return
	}
	b.seen[key] = true
	normalized := purl.NormalizeName(name)
	if b.versions[normalized] == nil {
		b.versions[normalized] = map[string]bool{}
	}
	if !b.versions[normalized][version] {
		b.versions[normalized][version] = true
		if len(b.versions[normalized]) > 1 {
			b.recordConflict(normalized)
		}
	}
	b.packages = append(b.packages, model.Package{
		PURL:        model.PURL(purl.New(name, version)),
		Name:        name,
		Version:     version,
		Ecosystem:   Ecosystem,
		Direct:      direct,
		MaxSeverity: model.SeverityInfo,
	})
}

// recordConflict notes that a package name appears with several versions.
func (b *graphBuilder) recordConflict(name string) {
	versions := make([]string, 0, len(b.versions[name]))
	for v := range b.versions[name] {
		versions = append(versions, v)
	}
	sort.Strings(versions)
	entry := name + ": " + strings.Join(versions, ", ")
	for _, existing := range b.conflicts {
		if existing == entry {
			return
		}
	}
	b.conflicts = append(b.conflicts, entry)
}

// addEdge records a dependency edge, ignoring duplicates.
func (b *graphBuilder) addEdge(edge model.GraphEdge) {
	key := string(edge.Source) + "->" + string(edge.Target)
	if b.edgeSeen[key] {
		return
	}
	b.edgeSeen[key] = true
	b.edges = append(b.edges, edge)
}

// build returns the finished resolution.
func (b *graphBuilder) build() *Resolution {
	return &Resolution{
		Packages:  b.packages,
		Edges:     b.edges,
		Conflicts: b.conflicts,
		Source:    b.source,
	}
}
