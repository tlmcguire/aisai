package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/tlmcguire/aisai/internal/model"
)

// This file reads lockfiles: the exact, fully resolved dependency tree a
// project builds with.
//
// A manifest (Cargo.toml, package.json) says what the project asks for, as
// version ranges and only one level deep. A lockfile (Cargo.lock,
// package-lock.json, poetry.lock) records what was actually chosen, down to
// the transitive dependencies and their exact versions. A vulnerability in a
// package three levels down is exactly as exploitable as one the project
// names, and it can only be found from the lockfile, because only the
// lockfile says which version is really in use. The parsers here are
// hand-written readers of just the fields needed (the formats are simple and
// documented) so that no new dependency is added:
//
//   - Cargo.lock: https://doc.rust-lang.org/cargo/guide/cargo-toml-vs-cargo-lock.html
//   - package-lock.json: https://docs.npmjs.com/cli/configuring-npm/package-lock-json
//   - poetry.lock: https://python-poetry.org/docs/basic-usage/#installing-with-poetrylock
//   - Pipfile.lock: https://pipenv.pypa.io/en/latest/pipfile.html
//   - go.mod: https://go.dev/ref/mod#go-mod-file

// lockPkg is one package of a lockfile.
type lockPkg struct {
	Ecosystem string
	Name      string
	Version   string
	// Source says where the package comes from: "registry", "git", "path"
	// or "" when the lockfile does not say. Only registry packages can be
	// looked up in an advisory database by name and version.
	Source string
	// Deps are the keys (see lockKey) of the packages it depends on.
	Deps []string
	// Direct is true for a package the project itself depends on.
	Direct bool
	// workspace marks the project's own crates (Cargo path packages): they
	// are the project, not dependencies of it.
	workspace bool
}

func lockKey(eco, name, version string) string { return eco + "|" + name + "@" + version }

func (p lockPkg) key() string { return lockKey(p.Ecosystem, p.Name, p.Version) }

// maxLockfileBytes is the largest lockfile that is read.
const maxLockfileBytes = 32 << 20

// readLockfiles finds the lockfiles of a project folder and returns the
// packages they list. It looks in the folder and one level down, because a
// project often keeps its front end and its back end in sibling folders.
// declared names the direct dependencies from the manifests, which marks
// them in formats whose lockfile does not.
func readLockfiles(dir string, declared []localDep) []lockPkg {
	var all []lockPkg
	seen := map[string]bool{}
	for _, d := range lockfileDirs(dir) {
		for _, p := range readLockfilesIn(d, declared) {
			k := p.key()
			if seen[k] {
				continue
			}
			seen[k] = true
			all = append(all, p)
		}
	}
	return all
}

// lockfileDirs is the folder itself and its visible subfolders.
func lockfileDirs(dir string) []string {
	out := []string{dir}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || strings.HasPrefix(name, ".") || skippedLockDirs[name] {
			continue
		}
		out = append(out, filepath.Join(dir, name))
	}
	return out
}

var skippedLockDirs = map[string]bool{"node_modules": true, "target": true, "vendor": true, "venv": true, "dist": true, "build": true, "__pycache__": true}

func readLockfilesIn(dir string, declared []localDep) []lockPkg {
	var out []lockPkg
	read := func(name string) (string, bool) {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err != nil || info.IsDir() || info.Size() > maxLockfileBytes {
			return "", false
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", false
		}
		return string(data), true
	}
	if text, ok := read("Cargo.lock"); ok {
		out = append(out, parseCargoLock(text)...)
	}
	if text, ok := read("package-lock.json"); ok {
		out = append(out, parsePackageLock(text, declared)...)
	}
	if text, ok := read("poetry.lock"); ok {
		out = append(out, parsePoetryLock(text, declared)...)
	}
	if text, ok := read("Pipfile.lock"); ok {
		out = append(out, parsePipfileLock(text)...)
	}
	if text, ok := read("go.mod"); ok {
		out = append(out, parseGoModLock(text)...)
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, "requirements*.txt")); len(matches) > 0 {
		sort.Strings(matches)
		for _, m := range matches {
			if text, ok := read(filepath.Base(m)); ok {
				out = append(out, parsePinnedRequirements(text)...)
			}
		}
	}
	return out
}

/* ---------------------------------------------------------------- Cargo */

var (
	tomlKV      = regexp.MustCompile(`^([A-Za-z0-9_-]+)\s*=\s*"([^"]*)"\s*$`)
	cargoDepRef = regexp.MustCompile(`^"([^"\s]+)(?:\s+([^"\s]+))?(?:\s+\([^)]*\))?"`)
)

// parseCargoLock reads Cargo.lock. Its packages without a source are the
// project's own crates: their dependencies are the project's direct
// dependencies, and they are left out of the result.
func parseCargoLock(text string) []lockPkg {
	type raw struct {
		name, version, source string
		deps                  []string // as written: "name", "name version", "name version (source)"
	}
	var pkgs []raw
	var cur *raw
	inDeps := false
	flush := func() {
		if cur != nil {
			pkgs = append(pkgs, *cur)
		}
		cur = nil
		inDeps = false
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "[[package]]":
			flush()
			cur = &raw{}
		case cur == nil:
		case inDeps:
			if strings.HasPrefix(line, "]") {
				inDeps = false
				continue
			}
			if m := cargoDepRef.FindStringSubmatch(line); m != nil {
				ref := m[1]
				if m[2] != "" {
					ref += " " + m[2]
				}
				cur.deps = append(cur.deps, ref)
			}
		case strings.HasPrefix(line, "dependencies = ["):
			rest := strings.TrimPrefix(line, "dependencies = [")
			if strings.Contains(rest, "]") {
				// A one-line list: dependencies = ["a", "b 1.0"]
				for _, part := range regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(rest, -1) {
					fields := strings.Fields(part[1])
					if len(fields) >= 2 {
						cur.deps = append(cur.deps, fields[0]+" "+fields[1])
					} else if len(fields) == 1 {
						cur.deps = append(cur.deps, fields[0])
					}
				}
			} else {
				inDeps = true
			}
		default:
			if m := tomlKV.FindStringSubmatch(line); m != nil {
				switch m[1] {
				case "name":
					cur.name = m[2]
				case "version":
					cur.version = m[2]
				case "source":
					cur.source = m[2]
				}
			}
		}
	}
	flush()

	// Index the versions of each name, so that a reference without a version
	// (written when only one version exists) can be resolved.
	byName := map[string][]raw{}
	for _, p := range pkgs {
		byName[p.name] = append(byName[p.name], p)
	}
	resolve := func(ref string) (raw, bool) {
		fields := strings.Fields(ref)
		cands := byName[fields[0]]
		if len(fields) >= 2 {
			for _, c := range cands {
				if c.version == fields[1] {
					return c, true
				}
			}
			return raw{}, false
		}
		if len(cands) > 0 {
			return cands[0], true
		}
		return raw{}, false
	}

	kindOf := func(source string) string {
		switch {
		case strings.HasPrefix(source, "registry+"), strings.HasPrefix(source, "sparse+"):
			return "registry"
		case strings.HasPrefix(source, "git+"):
			return "git"
		default:
			return "path"
		}
	}

	direct := map[string]bool{}
	var out []lockPkg
	for _, p := range pkgs {
		if p.source == "" { // a crate of the workspace: its dependencies are the project's
			for _, ref := range p.deps {
				if d, ok := resolve(ref); ok && d.source != "" {
					direct[lockKey("cargo", d.name, d.version)] = true
				}
			}
		}
	}
	for _, p := range pkgs {
		if p.source == "" {
			continue
		}
		lp := lockPkg{Ecosystem: "cargo", Name: p.name, Version: p.version, Source: kindOf(p.source)}
		for _, ref := range p.deps {
			if d, ok := resolve(ref); ok && d.source != "" {
				lp.Deps = append(lp.Deps, lockKey("cargo", d.name, d.version))
			}
		}
		lp.Direct = direct[lp.key()]
		out = append(out, lp)
	}
	return out
}

/* ------------------------------------------------------------------ npm */

type npmLockEntry struct {
	Version              string            `json:"version"`
	Link                 bool              `json:"link"`
	Dependencies         map[string]string `json:"dependencies"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
	PeerDependencies     map[string]string `json:"peerDependencies"`
	DevDependencies      map[string]string `json:"devDependencies"`
}

// parsePackageLock reads package-lock.json (lockfileVersion 2 and 3, whose
// "packages" map is keyed by install path, and version 1, whose nested
// "dependencies" map has no install paths).
func parsePackageLock(text string, declared []localDep) []lockPkg {
	var doc struct {
		Packages     map[string]npmLockEntry `json:"packages"`
		Dependencies map[string]struct {
			Version string `json:"version"`
		} `json:"dependencies"`
	}
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		return nil
	}
	if len(doc.Packages) > 0 {
		return npmFromPackages(doc.Packages)
	}
	// Version 1: a flat list with exact versions and no tree.
	direct := map[string]bool{}
	for _, d := range declared {
		if d.Ecosystem == "npm" {
			direct[d.Name] = true
		}
	}
	names := make([]string, 0, len(doc.Dependencies))
	for n := range doc.Dependencies {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []lockPkg
	for _, n := range names {
		v := doc.Dependencies[n].Version
		if v == "" {
			continue
		}
		out = append(out, lockPkg{Ecosystem: "npm", Name: n, Version: v, Source: "registry", Direct: direct[n]})
	}
	return out
}

// npmName is the package name of an install path: the part after the last
// "node_modules/".
func npmName(path string) string {
	const marker = "node_modules/"
	if i := strings.LastIndex(path, marker); i >= 0 {
		return path[i+len(marker):]
	}
	return path
}

func npmFromPackages(packages map[string]npmLockEntry) []lockPkg {
	// resolve finds the package a dependency name means from an install
	// path, the way Node does: look in the package's own node_modules, then
	// in each enclosing one.
	resolve := func(from, dep string) (string, bool) {
		for p := from; ; {
			cand := p + "/node_modules/" + dep
			if p == "" {
				cand = "node_modules/" + dep
			}
			if _, ok := packages[cand]; ok {
				return cand, true
			}
			if p == "" {
				return "", false
			}
			i := strings.LastIndex(p, "/node_modules/")
			if i < 0 {
				p = ""
			} else {
				p = p[:i]
			}
		}
	}
	allDeps := func(e npmLockEntry) []string {
		seen := map[string]bool{}
		var names []string
		for _, m := range []map[string]string{e.Dependencies, e.OptionalDependencies, e.PeerDependencies, e.DevDependencies} {
			for n := range m {
				if !seen[n] {
					seen[n] = true
					names = append(names, n)
				}
			}
		}
		sort.Strings(names)
		return names
	}

	paths := make([]string, 0, len(packages))
	for p := range packages {
		if p != "" {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)

	direct := map[string]bool{}
	if root, ok := packages[""]; ok {
		for _, n := range allDeps(root) {
			if p, ok := resolve("", n); ok {
				direct[p] = true
			}
		}
	}
	keyOf := func(path string) (string, bool) {
		e := packages[path]
		if e.Version == "" || e.Link {
			return "", false
		}
		return lockKey("npm", npmName(path), e.Version), true
	}
	var out []lockPkg
	for _, path := range paths {
		e := packages[path]
		if _, ok := keyOf(path); !ok {
			continue
		}
		lp := lockPkg{Ecosystem: "npm", Name: npmName(path), Version: e.Version, Source: "registry", Direct: direct[path]}
		for _, n := range allDeps(e) {
			if dp, ok := resolve(path, n); ok {
				if dk, ok := keyOf(dp); ok {
					lp.Deps = append(lp.Deps, dk)
				}
			}
		}
		out = append(out, lp)
	}
	return out
}

/* --------------------------------------------------------------- Python */

var pyNormalize = regexp.MustCompile(`[-_.]+`)

func pyName(n string) string { return strings.ToLower(pyNormalize.ReplaceAllString(n, "-")) }

// parsePoetryLock reads poetry.lock: a [[package]] table per package with a
// [package.dependencies] table of what it needs.
func parsePoetryLock(text string, declared []localDep) []lockPkg {
	type raw struct {
		name, version string
		deps          []string
	}
	var pkgs []raw
	var cur *raw
	section := ""
	flush := func() {
		if cur != nil && cur.name != "" {
			pkgs = append(pkgs, *cur)
		}
		cur = nil
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "[[package]]":
			flush()
			cur = &raw{}
			section = "package"
		case strings.HasPrefix(line, "["):
			section = line
		case cur == nil || line == "" || strings.HasPrefix(line, "#"):
		case section == "package":
			if m := tomlKV.FindStringSubmatch(line); m != nil {
				switch m[1] {
				case "name":
					cur.name = m[2]
				case "version":
					cur.version = m[2]
				}
			}
		case section == "[package.dependencies]":
			if i := strings.Index(line, "="); i > 0 {
				cur.deps = append(cur.deps, strings.Trim(strings.TrimSpace(line[:i]), `"`))
			}
		}
	}
	flush()

	byName := map[string]raw{}
	for _, p := range pkgs {
		byName[pyName(p.name)] = p
	}
	direct := map[string]bool{}
	for _, d := range declared {
		if d.Ecosystem == "pypi" {
			direct[pyName(d.Name)] = true
		}
	}
	var out []lockPkg
	for _, p := range pkgs {
		lp := lockPkg{Ecosystem: "pypi", Name: p.name, Version: p.version, Source: "registry", Direct: direct[pyName(p.name)]}
		for _, dn := range p.deps {
			if d, ok := byName[pyName(dn)]; ok {
				lp.Deps = append(lp.Deps, lockKey("pypi", d.name, d.version))
			}
		}
		out = append(out, lp)
	}
	return out
}

// parsePipfileLock reads Pipfile.lock: a flat list of exact versions in
// "default" and "develop".
func parsePipfileLock(text string) []lockPkg {
	var doc struct {
		Default map[string]struct {
			Version string `json:"version"`
		} `json:"default"`
		Develop map[string]struct {
			Version string `json:"version"`
		} `json:"develop"`
	}
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		return nil
	}
	var out []lockPkg
	add := func(m map[string]struct {
		Version string `json:"version"`
	}) {
		names := make([]string, 0, len(m))
		for n := range m {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			v := strings.TrimPrefix(m[n].Version, "==")
			if v == "" || strings.ContainsAny(v, "<>~!*") {
				continue
			}
			// The lockfile does not say which packages the project asked for.
			out = append(out, lockPkg{Ecosystem: "pypi", Name: n, Version: v, Source: "registry"})
		}
	}
	add(doc.Default)
	add(doc.Develop)
	return out
}

var pinnedReq = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._-]*)\s*(?:\[[^\]]*\])?\s*==\s*([A-Za-z0-9][A-Za-z0-9.!+_-]*)`)

// parsePinnedRequirements reads the exactly pinned lines of a requirements
// file ("name==1.2.3"). A range cannot be looked up, so it is not listed.
func parsePinnedRequirements(text string) []lockPkg {
	var out []lockPkg
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") {
			continue
		}
		if m := pinnedReq.FindStringSubmatch(line); m != nil {
			out = append(out, lockPkg{Ecosystem: "pypi", Name: m[1], Version: m[2], Source: "registry", Direct: true})
		}
	}
	return out
}

/* ------------------------------------------------------------------- Go */

var goModLine = regexp.MustCompile(`^(?:require\s+)?([A-Za-z0-9._~/-]+\.[A-Za-z0-9._~/-]+)\s+(v[^\s/]+)(\s*//\s*indirect)?`)

// parseGoModLock reads the require lines of go.mod. Since Go 1.17 go.mod lists
// every module in the build, marking the ones the project does not import
// itself with "// indirect", so it is a complete list with exact versions.
// It records no tree, so the indirect modules have no parents.
func parseGoModLock(text string) []lockPkg {
	var out []lockPkg
	inRequire := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "require ("):
			inRequire = true
			continue
		case line == ")":
			inRequire = false
			continue
		}
		if !inRequire && !strings.HasPrefix(line, "require ") {
			continue
		}
		if m := goModLine.FindStringSubmatch(line); m != nil {
			out = append(out, lockPkg{Ecosystem: "go", Name: m[1], Version: m[2], Source: "registry", Direct: m[3] == ""})
		}
	}
	return out
}

/* ---------------------------------------------------------------- Graph */

// lockedGraph builds the dependency graph of a project from its lockfiles:
// every locked package with its exact version, an edge from the project to
// each direct dependency, and the edges between packages the lockfile
// records. It returns false when there is no lockfile.
func lockedGraph(root model.Package, locked []lockPkg) ([]model.Package, []model.GraphEdge, bool) {
	if len(locked) == 0 {
		return nil, nil, false
	}
	pkgs := []model.Package{root}
	var edges []model.GraphEdge
	purls := map[string]model.PURL{}
	for _, l := range locked {
		purls[l.key()] = model.PackageURLFor(l.Ecosystem, l.Name, l.Version)
	}
	added := map[model.PURL]bool{root.PURL: true}
	for _, l := range locked {
		p := purls[l.key()]
		if added[p] {
			continue
		}
		added[p] = true
		pkgs = append(pkgs, model.Package{PURL: p, Name: l.Name, Version: l.Version, Ecosystem: l.Ecosystem, Direct: l.Direct, Source: l.Source})
		if l.Direct {
			edges = append(edges, model.GraphEdge{Source: root.PURL, Target: p})
		}
	}
	hasParent := map[model.PURL]bool{}
	for _, l := range locked {
		from := purls[l.key()]
		for _, dk := range l.Deps {
			if to, ok := purls[dk]; ok && to != from {
				edges = append(edges, model.GraphEdge{Source: from, Target: to})
				hasParent[to] = true
			}
		}
	}
	// A package that nothing else in the lockfile needs sits at the top of
	// the tree, so the project is what pulls it in. This is how a lockfile
	// that records no tree (go.mod, Pipfile.lock) still gets a connected graph.
	for _, l := range locked {
		p := purls[l.key()]
		if !l.Direct && !hasParent[p] {
			edges = append(edges, model.GraphEdge{Source: root.PURL, Target: p})
		}
	}
	return pkgs, edges, true
}
