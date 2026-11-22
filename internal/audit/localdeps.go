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

// localDep is one dependency a project declares in a manifest.
type localDep struct {
	Ecosystem string
	Name      string
	Version   string
}

var (
	pyReqPattern  = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._-]*)\s*(?:\[[^\]]*\])?\s*([<>=!~][^;#]*)?`)
	tomlStrPair   = regexp.MustCompile(`^"?([A-Za-z0-9_.-]+)"?\s*=\s*(?:"([^"]*)"|\{[^}]*version\s*=\s*"([^"]*)")`)
	goRequireLine = regexp.MustCompile(`^(?:require\s+)?([A-Za-z0-9._~/-]+\.[A-Za-z0-9._~/-]+)\s+(v[^\s]+)`)
)

// readLocalDeps lists the dependencies a project folder declares, read from the
// manifests at its top level: requirements*.txt, pyproject.toml, package.json,
// go.mod and Cargo.toml.
//
// Only what the manifests say is listed. Nothing is resolved, so the graph of
// a folder is the project and its direct dependencies, with the version range
// as declared. The second result names the manifests that were found.
func readLocalDeps(dir string) ([]localDep, []string) {
	var deps []localDep
	var found []string
	read := func(name string) (string, bool) {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || len(data) > 2<<20 {
			return "", false
		}
		found = append(found, name)
		return string(data), true
	}

	if matches, _ := filepath.Glob(filepath.Join(dir, "requirements*.txt")); len(matches) > 0 {
		sort.Strings(matches)
		for _, m := range matches {
			if text, ok := read(filepath.Base(m)); ok {
				deps = append(deps, parseRequirements(text)...)
			}
		}
	}
	if text, ok := read("pyproject.toml"); ok {
		deps = append(deps, parsePyproject(text)...)
	}
	if text, ok := read("package.json"); ok {
		deps = append(deps, parsePackageJSON(text)...)
	}
	if text, ok := read("go.mod"); ok {
		deps = append(deps, parseGoMod(text)...)
	}
	if text, ok := read("Cargo.toml"); ok {
		deps = append(deps, parseCargo(text)...)
	}
	return deps, found
}

func parseRequirements(text string) []localDep {
	var out []localDep
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") {
			continue
		}
		if m := pyReqPattern.FindStringSubmatch(line); m != nil {
			out = append(out, localDep{"pypi", m[1], strings.TrimSpace(m[2])})
		}
	}
	return out
}

// parsePyproject reads the quoted entries of `dependencies = [ ... ]` in the
// [project] table.
func parsePyproject(text string) []localDep {
	var out []localDep
	inProject, inList := false, false
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "["):
			inProject = line == "[project]"
			inList = false
			continue
		case !inProject:
			continue
		}
		if strings.HasPrefix(line, "dependencies") && strings.Contains(line, "[") {
			inList = true
			line = line[strings.Index(line, "[")+1:]
		}
		if !inList {
			continue
		}
		for _, part := range regexp.MustCompile(`"([^"]+)"|'([^']+)'`).FindAllStringSubmatch(line, -1) {
			spec := part[1] + part[2]
			if m := pyReqPattern.FindStringSubmatch(spec); m != nil {
				out = append(out, localDep{"pypi", m[1], strings.TrimSpace(m[2])})
			}
		}
		if strings.Contains(line, "]") {
			inList = false
		}
	}
	return out
}

func parsePackageJSON(text string) []localDep {
	var pkg struct {
		Dependencies         map[string]string `json:"dependencies"`
		DevDependencies      map[string]string `json:"devDependencies"`
		OptionalDependencies map[string]string `json:"optionalDependencies"`
	}
	if json.Unmarshal([]byte(text), &pkg) != nil {
		return nil
	}
	var out []localDep
	for _, group := range []map[string]string{pkg.Dependencies, pkg.OptionalDependencies, pkg.DevDependencies} {
		names := make([]string, 0, len(group))
		for n := range group {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			out = append(out, localDep{"npm", n, group[n]})
		}
	}
	return out
}

func parseGoMod(text string) []localDep {
	var out []localDep
	inBlock := false
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(strings.SplitN(raw, "//", 2)[0])
		switch {
		case strings.HasPrefix(line, "require ("):
			inBlock = true
			continue
		case line == ")":
			inBlock = false
			continue
		}
		if !inBlock && !strings.HasPrefix(line, "require ") {
			continue
		}
		if m := goRequireLine.FindStringSubmatch(line); m != nil {
			out = append(out, localDep{"golang", m[1], m[2]})
		}
	}
	return out
}

// parseCargo reads the [dependencies] and [build-dependencies] tables.
func parseCargo(text string) []localDep {
	var out []localDep
	in := false
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "[") {
			in = line == "[dependencies]" || line == "[build-dependencies]"
			continue
		}
		if !in {
			continue
		}
		if m := tomlStrPair.FindStringSubmatch(line); m != nil {
			out = append(out, localDep{"cargo", m[1], m[2] + m[3]})
		}
	}
	return out
}

// localGraph turns the declared dependencies of a folder into packages and
// edges below its root package.
func localGraph(root model.Package, deps []localDep) ([]model.Package, []model.GraphEdge) {
	pkgs := []model.Package{root}
	var edges []model.GraphEdge
	seen := map[model.PURL]bool{root.PURL: true}
	for _, d := range deps {
		version := d.Version
		if version == "" {
			version = "*"
		}
		p := model.PackageURLFor(d.Ecosystem, d.Name, version)
		if seen[p] {
			continue
		}
		seen[p] = true
		pkgs = append(pkgs, model.Package{PURL: p, Name: d.Name, Version: version, Ecosystem: d.Ecosystem, Direct: true})
		edges = append(edges, model.GraphEdge{Source: root.PURL, Target: p})
	}
	return pkgs, edges
}
