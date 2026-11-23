# W16 — More ecosystems

**Goal:** accept packages and projects from registries beyond PyPI, npm and crates.io, with the
same pipeline: index, safe acquisition, dependency tree, advisories and static rules.

**Status:** ⬜ not started
**Depends on:** W1, W14, W15
**Blocks:** —

## Current state (verified 2026-11-23)

- Three package sources: PyPI, npm and crates.io (root README, "Package managers").
- Lockfile reading covers Cargo, npm, Poetry, Pipfile and Go's `go.mod` (the last only for the
  tree, see W14).
- No Maven, Gradle, RubyGems, Packagist, NuGet, Hex, Pub or Go-modules-as-a-source.

## Priority order

Rank by how often a user would audit that ecosystem, then by how cheap the registry is to read.
Starting list, to confirm with the user:

1. [ ] Go modules: module proxy (`proxy.golang.org`), `go.sum`, Go vulnerability database.
       Highest fit, because the app is written in Go and W11 call-graph work starts there.
2. [ ] Maven: Maven Central, `pom.xml` and the Gradle lockfile format
3. [ ] RubyGems: `Gemfile.lock`
4. [ ] Packagist (PHP): `composer.lock`
5. [ ] NuGet: `packages.lock.json`
6. [ ] Hex (Elixir) and Pub (Dart) — only if there is demand

## Tasks (repeat for each ecosystem)

### Source and index
- [ ] Registry client with the same safety rules as the existing sources: HTTPS only, size and
      time limits, no code execution during acquisition
- [ ] Verify the artifact against the registry's published checksum or integrity value before
      extraction
- [ ] Safe extraction with the same path and symlink checks used for the other ecosystems

### Dependency tree
- [ ] Lockfile parser (W14 pattern), or manifest resolution when no lockfile exists
- [ ] Historical resolution where the registry has timestamps (W13 pattern)

### Advisories
- [ ] Map the ecosystem to its OSV name and check that OSV covers it; if not, use the ecosystem's
      own feed (Go vuln DB, GHSA)
- [ ] PURL type for the ecosystem (`pkg:golang/`, `pkg:maven/`, `pkg:gem/`, `pkg:composer/`,
      `pkg:nuget/`) with tests that the join key round-trips

### Static analysis
- [ ] Decide which rule families apply (taint for dynamic languages first, pattern rules for the
      rest), and record that choice per ecosystem in `docs/plan/decisions.md`
- [ ] Install-time hooks for the ecosystem, where it has them (W12): `gem` extensions, Maven
      plugins, NuGet `install.ps1`

### Packaging for the app
- [ ] Index search and "why is this here" parsing for the ecosystem's requirement syntax
- [ ] Fixtures for a real package from each ecosystem, kept small, with licence noted

## Acceptance criteria (per ecosystem)

- [ ] A real package pulls, scans and shows its tree, with its advisories, end to end
- [ ] A tampered artifact (wrong checksum) is refused with a clear error
- [ ] Its PURL round-trips through the SBOM export

## Notes

- Each ecosystem is a separate, sizeable job. Ship them one at a time, Go first.
- The scope warning in the root plan (scope creep) applies here: do not add an ecosystem until
  W11 and W12 have landed for the existing three.
