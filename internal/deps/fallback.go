package deps

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/tlmcguire/aisai/internal/model"
	"github.com/tlmcguire/aisai/internal/purl"
	"github.com/tlmcguire/aisai/internal/pypi"
)

// pendingPackage is one package whose dependencies still have to be walked.
type pendingPackage struct {
	name    string
	version string
	depth   int
}

// resolveFromPyPIMetadata walks the PyPI `requires_dist` metadata
// recursively.
//
// This is the fallback used when deps.dev cannot answer. It is less precise
// than the deps.dev resolution: version ranges are resolved against the
// published release list and environment markers are evaluated against the
// resolver's marker environment.
func (r *Resolver) resolveFromPyPIMetadata(ctx context.Context, rootName, rootVersion string) (*Resolution, error) {
	builder := newGraphBuilder(Ecosystem, rootName, rootVersion)
	builder.source = SourcePyPIMetadata
	builder.addPackage(rootName, rootVersion, true)

	rootRequires, err := r.pypi.RequiresDist(ctx, purl.NormalizeName(rootName), rootVersion)
	if err != nil {
		return nil, fmt.Errorf("deps: read metadata of %s@%s: %w", rootName, rootVersion, err)
	}
	if err := r.decorateRoot(ctx, builder, rootName, rootVersion); err != nil {
		log.Printf("deps: could not read the metadata of %s@%s: %v", rootName, rootVersion, err)
	}

	markerEnv := pypi.DefaultMarkerEnv()
	releases := map[string][]string{}
	requiresCache := map[string][]string{packageKey(rootName, rootVersion): rootRequires}
	walked := map[string]bool{}
	queue := []pendingPackage{{name: rootName, version: rootVersion, depth: 0}}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current.depth > maxFallbackDepth {
			log.Printf("deps: stopped walking %s@%s at the maximum depth", current.name, current.version)
			continue
		}
		if len(builder.packages) >= maxFallbackPackages {
			log.Printf("deps: stopped the PyPI metadata walk at %d packages", maxFallbackPackages)
			break
		}
		key := packageKey(current.name, current.version)
		if walked[key] {
			continue
		}
		walked[key] = true
		requires, ok := requiresCache[key]
		if !ok {
			fetched, err := r.pypi.RequiresDist(ctx, purl.NormalizeName(current.name), current.version)
			if err != nil {
				log.Printf("deps: could not read the metadata of %s@%s: %v", current.name, current.version, err)
				continue
			}
			requires = fetched
			requiresCache[key] = fetched
		}
		for _, spec := range requires {
			req, err := pypi.ParseRequirement(spec)
			if err != nil {
				log.Printf("deps: skipping unreadable requirement %q of %s@%s: %v", spec, current.name, current.version, err)
				continue
			}
			if !requirementApplies(req, markerEnv) {
				continue
			}
			version, err := r.resolveRequirementVersion(ctx, req, releases)
			if err != nil {
				log.Printf("deps: could not resolve %q of %s@%s: %v", req.Name, current.name, current.version, err)
				version = ""
			}
			builder.addPackage(req.Name, version, false)
			builder.addEdge(model.GraphEdge{
				Source: model.PURL(purl.New(current.name, current.version)),
				Target: model.PURL(purl.New(req.Name, version)),
			})
			queue = append(queue, pendingPackage{name: req.Name, version: version, depth: current.depth + 1})
		}
	}
	return builder.build(), nil
}

// decorateRoot fills the summary, license and home page of the root package.
func (r *Resolver) decorateRoot(ctx context.Context, builder *graphBuilder, name, version string) error {
	info, err := r.pypi.Package(ctx, purl.NormalizeName(name), version)
	if err != nil {
		return err
	}
	for i := range builder.packages {
		if packageKey(builder.packages[i].Name, builder.packages[i].Version) == packageKey(name, version) {
			builder.packages[i].Summary = info.Summary
			builder.packages[i].License = info.License
			builder.packages[i].Homepage = info.Homepage
		}
	}
	return nil
}

// requirementApplies reports whether a requirement applies to the current
// environment.
//
// Requirements guarded by an "extra" marker are never followed: AISAI
// audits the base distribution of a package, not its optional extras.
func requirementApplies(req pypi.Requirement, env pypi.MarkerEnv) bool {
	if strings.TrimSpace(req.Marker) == "" {
		return true
	}
	if strings.Contains(req.Marker, "extra") {
		return false
	}
	ok, err := pypi.EvaluateMarker(req.Marker, env)
	if err != nil {
		// An unreadable marker must not silently drop a dependency, so the
		// requirement is kept and the problem is reported.
		log.Printf("deps: could not evaluate the marker %q of %q: %v", req.Marker, req.Name, err)
		return true
	}
	return ok
}

// resolveRequirementVersion picks the version of a dependency that satisfies
// the requirement's specifier.
func (r *Resolver) resolveRequirementVersion(ctx context.Context, req pypi.Requirement, cache map[string][]string) (string, error) {
	name := purl.NormalizeName(req.Name)
	versions, ok := cache[name]
	if !ok {
		fetched, err := r.pypi.Releases(ctx, name)
		if err != nil {
			return "", err
		}
		cache[name] = fetched
		versions = fetched
	}
	version := pypi.LatestVersion(versions, req.Specifier)
	if version == "" {
		return "", fmt.Errorf("no release of %q satisfies %q", req.Name, req.Specifier)
	}
	return version, nil
}
