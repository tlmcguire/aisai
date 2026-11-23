# W11 — Call-graph reachability

**Goal:** tell the user whether a vulnerable dependency is actually *called* by the audited
project, and if so, from where. Most advisories name a function or module that the project never
touches; reachability turns a flat CVE list into a triage list.

**Status:** ⬜ not started
**Depends on:** W7 (advisory data), W14 (complete dependency tree)
**Blocks:** W7 "Filter: only reachable vulnerable dependencies"

## Current state (verified 2026-11-23)

- `internal/sast/reachability.go` labels a finding `reachable` only when request-derived data
  reaches the sink in the same function scope. It never says `unreachable`, and it builds no
  call graph.
- The root `README.md` (Known limitations) still says "No reachability analysis yet". That line
  is out of date for the scope-local check and must be corrected when this work lands.
- W7 has an older, unticked "Reachability" section that describes an import-graph approach.
  This workstream supersedes it.

## Design decision to make first

Reachability needs a rule for "vulnerable symbol". Options, in order of effort:

1. **Import-level:** the project imports the vulnerable package at all. Cheap, but almost every
   imported package is "reachable", so it tells the user little.
2. **Symbol-level:** the project references a function, class or module named in the advisory.
   Needs advisory data that names symbols (OSV `affected[].ecosystem_specific` for some
   ecosystems, GHSA sometimes, Go's vulnerability database always).
3. **Call-graph-level:** an entry point of the project reaches that symbol through its calls.
   Needs a per-language call graph of the dependency's source, which is the expensive part.

- [ ] Record the decision in `docs/plan/decisions.md` (D-number, rationale, which level ships first)

## Tasks

### Vulnerable-symbol data
- [ ] Survey which advisory sources (OSV, GHSA, Go vuln DB) name affected functions or modules,
      per ecosystem, and record the coverage percentage on a sample of 200 real advisories
- [ ] Add `Symbols []string` to `model.Advisory`, filled from whichever source provides them
- [ ] Store the source of each symbol list (so the UI can say "from OSV" or "from GHSA")

### Reachability levels
- [ ] Implement level 1 (import-level) for PyPI, npm and crates.io; classify as `imported` /
      `not-imported` / `unknown`
- [ ] Implement level 2 (symbol-level) where symbol data exists; classify as `referenced` /
      `not-referenced` / `unknown`
- [ ] Implement level 3 (call-graph) for at least one language first (Go, since `govulncheck`
      shows the approach works; then Python, then JavaScript/TypeScript)
- [ ] Never report `unreachable` unless the analysis is complete for that package. Otherwise
      report `unknown` and say why (dynamic import, reflection, unresolved package)

### Call-graph construction (level 3)
- [ ] Entry points: public functions of the audited package, CLI and console-script entries,
      `main` packages, exported npm `main`/`exports`, Rust `pub` items of library crates
- [ ] Build an intra-project call graph from tree-sitter ASTs (reuse the parsers in `internal/ast`)
- [ ] Resolve imports to dependency packages using the graph from W14
- [ ] Map call sites to vulnerable symbols; record the shortest call path as a list of
      `file:line` steps
- [ ] Cap work per audit (time and memory) and report partial results honestly

### Surfacing
- [ ] Advisory list shows the reachability label next to each advisory
- [ ] Graph filter: "only reachable vulnerable dependencies" (the W7 box)
- [ ] Finding detail shows the call path when one exists
- [ ] Reports (PDF, HTML, SARIF, CycloneDX VEX) carry the label
- [ ] CycloneDX VEX maps `not-referenced` and `unreachable` to the right `analysis.state`, and
      `unknown` to `in_triage`

### Documentation
- [ ] Update root `README.md` "Known limitations" to describe what reachability does and does not
      prove
- [ ] Update `docs/plan/README.md` "three largest gaps" paragraph
- [ ] Mark the superseded W7 "Reachability" section as moved to this file

## Acceptance criteria

- [ ] On a fixture project that imports a vulnerable package but calls only a safe function,
      level 2 reports `not-referenced` and level 3 reports `unreachable`, and both have tests
- [ ] On a fixture where the vulnerable function is called from an entry point, the call path
      names the entry point and every intermediate call
- [ ] A dynamic call (`getattr`, `eval`, reflection) into the package yields `unknown`, never
      `unreachable`
- [ ] Running the analysis on a large project finishes within the configured budget and says so
      when it stops early

## Notes

- This is the biggest single gain in triage quality. It is also the most expensive item in the
  plan, so the level-1 and level-2 steps should ship before level 3.
- A claim of `unreachable` is a strong claim. Treat any false `unreachable` as a bug worse than
  a missed finding, and test for it directly.
