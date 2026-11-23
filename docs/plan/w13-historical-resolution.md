# W13 — Historical dependency resolution

**Goal:** audit a package as it was at a chosen date or release, so the dependency tree matches
what that release actually installed, and so advisories are judged against the versions that
were really in use.

**Status:** ⬜ not started
**Depends on:** W1, W14 (for lockfile-derived trees)
**Blocks:** W17 (re-checking old audits)

## Current state (verified 2026-11-23)

- The root `README.md` (Known limitations) states the problem: `flask==2.0.0` resolves `click` to
  its current version, `8.5.1`, not the `7.1.2` it used at the time.
- deps.dev resolves dependencies to current versions. No cutoff-date input exists.
- Projects with a lockfile already pin real versions, so the gap is mainly for downloaded packages.

## Design decision to make first

- [ ] Choose the input: a release date, the root's own release time, or both. Record in
      `docs/plan/decisions.md`
- [ ] Choose the resolution strategy per ecosystem (see tasks), since the registries differ

## Tasks

### Timestamps from registries
- [ ] PyPI: read `upload_time_iso_8601` for every file of every release from the JSON API; cache
      the result per package
- [ ] npm: read the `time` object from the packument (`registry.npmjs.org/<name>`)
- [ ] crates.io: read `created_at` from `/api/v1/crates/<name>/versions`
- [ ] Handle yanked and deleted versions: a version removed after the cutoff must still resolve
      for the historical tree, and the report must say it was yanked

### Resolution at a cutoff
- [ ] Python: resolve with the cutoff applied to every candidate release (equivalent of
      `uv pip compile --exclude-newer`). Decide whether to shell out to `uv` (optional tool,
      like Bandit and Semgrep) or to port the resolution logic
- [ ] npm: for each range, pick the highest version published before the cutoff (the way
      `npm` resolution behaves for a fresh install at that date). Respect `dependencies` vs
      `peerDependencies` and `optionalDependencies`
- [ ] crates.io: pick the highest non-yanked version before the cutoff that satisfies the
      requirement, and record the resolved `Cargo.lock`-equivalent tree
- [ ] Record each resolution decision (requirement, candidates considered, chosen version) so the
      tree is explainable

### Fallback and honesty
- [ ] When a registry lacks timestamps, fall back to the current deps.dev tree and mark every
      node as `current, not historical`
- [ ] Show the resolution date and strategy in the graph header and in reports
- [ ] Mark each node with its resolution source: `lockfile`, `historical`, `current`

### Advisory timing
- [ ] Advisories are judged against the historical version. Add a second view: "advisories
      published after the cutoff" (known later, still present then)
- [ ] Record each advisory's published and modified dates from OSV

### Tests
- [ ] Fixture: `flask==2.0.0` resolved at its release date gives `click 7.x`, not `8.x`
- [ ] Fixture: an npm range that gained a new matching version after the cutoff resolves to the
      older version
- [ ] Fixture: a yanked crates.io version present before the cutoff is still resolved, and is
      reported as yanked

## Acceptance criteria

- [ ] Auditing `flask==2.0.0` with the cutoff at its release date shows the historical `click`
      version and marks the node `historical`
- [ ] With the registry unreachable, the app falls back to `current, not historical` and says so
- [ ] Every historical node can be traced to its resolution decision

## Notes

- Historical resolution is an approximation of what a fresh install did at the time. Build
  backends, platform markers and optional extras can change the result, and the report should say
  which of those were ignored.
- This work makes "audit as of date X" possible. W17 uses it to re-check old audits when new
  advisories appear.
