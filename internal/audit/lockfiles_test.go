package audit

import (
	"sort"
	"strings"
	"testing"

	"github.com/tlmcguire/aisai/internal/model"
)

func lockNames(ps []lockPkg) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Name+"@"+p.Version)
	}
	sort.Strings(out)
	return out
}

func findLock(ps []lockPkg, name string) *lockPkg {
	for i := range ps {
		if ps[i].Name == name {
			return &ps[i]
		}
	}
	return nil
}

func TestParseCargoLock(t *testing.T) {
	text := `
version = 4

[[package]]
name = "myapp"
version = "0.1.0"
dependencies = [
 "serde",
 "tokio 1.0.0",
]

[[package]]
name = "serde"
version = "1.0.200"
source = "registry+https://github.com/rust-lang/crates.io-index"
checksum = "abc"
dependencies = [
 "serde_derive",
]

[[package]]
name = "serde_derive"
version = "1.0.200"
source = "registry+https://github.com/rust-lang/crates.io-index"

[[package]]
name = "tokio"
version = "1.0.0"
source = "registry+https://github.com/rust-lang/crates.io-index"
dependencies = ["mio 0.8.0 (registry+https://github.com/rust-lang/crates.io-index)"]

[[package]]
name = "mio"
version = "0.8.0"
source = "registry+https://github.com/rust-lang/crates.io-index"

[[package]]
name = "mio"
version = "0.7.0"
source = "registry+https://github.com/rust-lang/crates.io-index"

[[package]]
name = "fromgit"
version = "0.1.0"
source = "git+https://github.com/x/y?rev=abc#abc"
`
	got := parseCargoLock(text)
	if strings.Join(lockNames(got), ",") != "fromgit@0.1.0,mio@0.7.0,mio@0.8.0,serde@1.0.200,serde_derive@1.0.200,tokio@1.0.0" {
		t.Fatalf("packages = %v", lockNames(got))
	}
	if p := findLock(got, "myapp"); p != nil {
		t.Error("the project's own crate must not be a dependency")
	}
	serde := findLock(got, "serde")
	if !serde.Direct || serde.Source != "registry" {
		t.Errorf("serde: %+v", serde)
	}
	if d := findLock(got, "serde_derive"); d.Direct {
		t.Error("serde_derive is transitive")
	}
	if len(serde.Deps) != 1 || serde.Deps[0] != lockKey("cargo", "serde_derive", "1.0.200") {
		t.Errorf("serde deps = %v", serde.Deps)
	}
	// A reference with a version picks that version among several.
	tokio := findLock(got, "tokio")
	if len(tokio.Deps) != 1 || tokio.Deps[0] != lockKey("cargo", "mio", "0.8.0") {
		t.Errorf("tokio deps = %v", tokio.Deps)
	}
	if g := findLock(got, "fromgit"); g.Source != "git" {
		t.Errorf("fromgit source = %q", g.Source)
	}
}

func TestParsePackageLockResolvesNestedModules(t *testing.T) {
	text := `{
  "lockfileVersion": 3,
  "packages": {
    "": {"name": "app", "dependencies": {"express": "^4", "qs": "^6"}},
    "node_modules/express": {"version": "4.18.0", "dependencies": {"qs": "6.5.0", "accepts": "^1"}},
    "node_modules/express/node_modules/qs": {"version": "6.5.0"},
    "node_modules/qs": {"version": "6.11.0"},
    "node_modules/accepts": {"version": "1.3.8"},
    "node_modules/linked": {"link": true, "resolved": "../linked"}
  }
}`
	got := parsePackageLock(text, nil)
	if strings.Join(lockNames(got), ",") != "accepts@1.3.8,express@4.18.0,qs@6.11.0,qs@6.5.0" {
		t.Fatalf("packages = %v", lockNames(got))
	}
	express := findLock(got, "express")
	if !express.Direct {
		t.Error("express is a direct dependency")
	}
	// express needs qs: the nested copy (6.5.0) wins over the top-level one.
	hasNested := false
	for _, d := range express.Deps {
		if d == lockKey("npm", "qs", "6.5.0") {
			hasNested = true
		}
		if d == lockKey("npm", "qs", "6.11.0") {
			t.Error("express resolved to the wrong qs")
		}
	}
	if !hasNested {
		t.Errorf("express deps = %v", express.Deps)
	}
	if a := findLock(got, "accepts"); a.Direct {
		t.Error("accepts is transitive")
	}
}

func TestParsePoetryPipfileGoAndPinned(t *testing.T) {
	poetry := parsePoetryLock(`
[[package]]
name = "Flask"
version = "3.0.0"

[package.dependencies]
Werkzeug = ">=3.0"
click = ">=8.1"

[[package]]
name = "werkzeug"
version = "3.0.1"

[[package]]
name = "click"
version = "8.1.7"
`, []localDep{{Ecosystem: "pypi", Name: "flask"}})
	if !findLock(poetry, "Flask").Direct || findLock(poetry, "werkzeug").Direct {
		t.Error("only flask is declared")
	}
	if len(findLock(poetry, "Flask").Deps) != 2 {
		t.Errorf("flask deps = %v", findLock(poetry, "Flask").Deps)
	}

	pipfile := parsePipfileLock(`{"default": {"requests": {"version": "==2.31.0"}, "weird": {"version": "*"}}, "develop": {"pytest": {"version": "==8.0.0"}}}`)
	if strings.Join(lockNames(pipfile), ",") != "pytest@8.0.0,requests@2.31.0" {
		t.Errorf("pipfile = %v", lockNames(pipfile))
	}

	gomod := parseGoModLock("module x\n\ngo 1.22\n\nrequire (\n\tgithub.com/a/b v1.2.3\n\tgolang.org/x/text v0.3.0 // indirect\n)\n\nrequire github.com/c/d v0.1.0\n")
	if strings.Join(lockNames(gomod), ",") != "github.com/a/b@v1.2.3,github.com/c/d@v0.1.0,golang.org/x/text@v0.3.0" {
		t.Errorf("go.mod = %v", lockNames(gomod))
	}
	if findLock(gomod, "golang.org/x/text").Direct || !findLock(gomod, "github.com/a/b").Direct {
		t.Error("indirect modules are marked wrongly")
	}

	pinned := parsePinnedRequirements("# c\nflask==3.0.0\nrequests>=2\nnumpy[extra]==1.26.4\n-r other.txt\n")
	if strings.Join(lockNames(pinned), ",") != "flask@3.0.0,numpy@1.26.4" {
		t.Errorf("pinned = %v", lockNames(pinned))
	}
}

func TestLockedGraphConnectsTheTree(t *testing.T) {
	root := model.Package{PURL: "pkg:generic/app", Name: "app", Ecosystem: "local", Direct: true}
	locked := []lockPkg{
		{Ecosystem: "cargo", Name: "a", Version: "1", Direct: true, Deps: []string{lockKey("cargo", "b", "1")}},
		{Ecosystem: "cargo", Name: "b", Version: "1", Deps: []string{lockKey("cargo", "c", "1")}},
		{Ecosystem: "cargo", Name: "c", Version: "1"},
		{Ecosystem: "go", Name: "orphan", Version: "v1"}, // nothing needs it: the project does
	}
	pkgs, edges, ok := lockedGraph(root, locked)
	if !ok || len(pkgs) != 5 {
		t.Fatalf("ok=%v pkgs=%d", ok, len(pkgs))
	}
	names := map[model.PURL]string{}
	for _, p := range pkgs {
		names[p.PURL] = p.Name
	}
	c := model.PackageURLFor("cargo", "c", "1")
	chain := chainTo(edges, names, root.PURL, c)
	if strings.Join(chain, ">") != "a>b>c" {
		t.Errorf("chain to c = %v", chain)
	}
	orphan := model.PackageURLFor("go", "orphan", "v1")
	if got := chainTo(edges, names, root.PURL, orphan); strings.Join(got, ">") != "orphan" {
		t.Errorf("chain to orphan = %v", got)
	}
	if got := chainTo(edges, names, root.PURL, "pkg:cargo/unknown@1"); got != nil {
		t.Errorf("an unknown package has no chain, got %v", got)
	}
	// Counting: c is vulnerable, so a and b each have one below them.
	counts := transitiveVulnCounts(edges, []model.PURL{c})
	if counts[model.PackageURLFor("cargo", "a", "1")] != 1 || counts[model.PackageURLFor("cargo", "b", "1")] != 1 || counts[c] != 0 {
		t.Errorf("counts = %v", counts)
	}
}

func TestLookupable(t *testing.T) {
	cases := []struct {
		p    model.Package
		want bool
	}{
		{model.Package{Ecosystem: "cargo", Version: "1.2.3", Source: "registry"}, true},
		{model.Package{Ecosystem: "go", Version: "v1.2.3", Source: "registry"}, true},
		{model.Package{Ecosystem: "cargo", Version: "0.1.0", Source: "git"}, false},
		{model.Package{Ecosystem: "npm", Version: "^4.0.0", Source: "registry"}, false},
		{model.Package{Ecosystem: "pypi", Version: ">=2", Source: "registry"}, false},
		{model.Package{Ecosystem: "pypi", Version: "*"}, false},
		{model.Package{Ecosystem: "local", Version: "local"}, false},
	}
	for _, c := range cases {
		if got := lookupable(c.p); got != c.want {
			t.Errorf("lookupable(%+v) = %v, want %v", c.p, got, c.want)
		}
	}
}
