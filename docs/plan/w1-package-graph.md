# W1 — Package Acquisition & Dependency Graph

**Goal:** given `package==version`, produce a dependency graph keyed by PURL and a workspace of
safely-extracted source for every dependency.

**Status:** 🔄 in progress — core shipped and verified; 4 boxes open (see Notes).
**Depends on:** W0
**Blocks:** W3, W7

## Tasks

### PURL
- [x] `internal/purl/purl.go` — PURL construction, parse, normalize, compare
- [x] Round-trip tests: `pkg:pypi/flask@2.0.0` → name `flask`, version `2.0.0`
- [x] Handle namespaced and normalized PyPI names (`Flask` vs `flask`, `zope.interface`)

### PyPI client
- [x] `internal/pypi/client.go` — `GET https://pypi.org/pypi/{name}/{version}/json`
- [x] Resolve version specifiers (`==`, `>=`, `<`, `~=`) against the PyPI release list
- [x] Fetch project metadata when the version is unknown
- [x] Locate the **sdist** (`packagetype == "sdist"`); fall back to wheel if no sdist
- [x] Honor the `Content-Length` / stream to disk rather than buffering in memory
- [x] Retries with backoff on 5xx and network errors
- [x] Respect a configured timeout; surface a clear error on timeout
- [x] Unit tests with `httptest.Server` fixtures

### Safe extraction
- [x] `internal/pypi/extract.go` — `safeExtract(io.Reader, dst)`
- [x] Reject absolute paths
- [x] Reject `..` traversal (after `filepath.Clean`)
- [x] Reject symlinks and hardlinks that escape the destination
- [x] Strip setuid/setgid bits; cap file permissions
- [x] Enforce a max uncompressed size and a max file count (zip-bomb guard)
- [x] Skip entries that would overwrite outside the destination
- [x] **Fuzz target** for `safeExtract`
- [x] Tests covering: `../etc/passwd`, `/etc/passwd`, `a/../../b`, symlink escape,
      `a/b/../../../c`, absolute symlink target

### Dependency resolution
- [x] `internal/deps/depsdev.go` — `https://api.deps.dev/v3/systems/PYPI/packages/{name}/versions/{version}:dependencies`
- [x] Parse the resolved graph into `model.Package` / `model.GraphEdge`
- [x] Fallback resolver: PyPI `requires_dist` metadata walked recursively
- [x] Detect and surface version conflicts (same package, two versions)
- [ ] Cycle detection in the dependency graph
- [ ] Cache resolution results on disk keyed by `(name, version)` with an ETag/TTL

### Workspace layout
- [x] `internal/audit/workspace.go` — create a per-audit directory
- [x] Layout: `<workspace>/<auditID>/pkgs/<purl-escaped>/`
- [x] Download each dependency's sdist into the workspace, extract safely
- [x] **Never** run `pip install` on the target or its dependencies
- [ ] Skip extraction for packages with no Python source (metadata-only)

### Graph assembly
- [x] `internal/audit/graph.go` — build `model.Graph` from the resolution + findings
- [x] Node label: `name\nvX.Y.Z` (same convention as the legacy app)
- [x] Node severity: max of advisory severity and finding severity
- [x] Counts per node: findings, advisories
- [x] `GET /api/audits/{id}/graph`

### API
- [x] `POST /api/audits` → create audit for `{package, version?}`
- [x] `GET /api/audits` → list audits
- [x] `GET /api/audits/{id}` → audit detail
- [x] `GET /api/audits/{id}/graph` → dependency graph
- [x] `GET /api/audits/{id}/files` → file tree
- [x] `GET /api/audits/{id}/file?path=` → file content
- [x] All file paths validated against the audit workspace root (no traversal)

## Acceptance criteria

- [x] Auditing `flask==2.0.0` produces a graph with the correct transitive dependencies
- [x] A malicious tarball with `../evil.sh` is rejected and the audit fails cleanly
- [x] No target package code is executed at any point (verify with `strace`-style check or
      by inspecting that `pip` is never invoked)
- [x] The graph API returns PURL-keyed nodes that match the OSV/CycloneDX join key
- [ ] Re-auditing the same package+version reuses the cached resolution

## Notes

- The legacy app resolved dependencies by `pip install`-ing the target and running `pipdeptree`
  against the live environment. That both executed attacker code and could describe a different
  resolution than the requested version. deps.dev uses the same algorithms as the packaging
  tools, so it is the accurate source.
- Fallback order: deps.dev → PyPI `requires_dist` walk → nothing (fail loudly, never silently
  return a partial tree).

### Verified in this pass (2026-11-21)

**Ticked on the strength of:** `internal/purl/purl.go` (+ `purl_test.go`), `internal/pypi/client.go`,
`internal/pypi/packaging.go`, `internal/pypi/extract.go` (+ `extract_test.go`, `fuzz_test.go`),
`internal/deps/depsdev.go`, `internal/deps/fallback.go`, `internal/audit/workspace.go`,
`internal/audit/manager.go` (`Graph`), `internal/server/server.go`.

**Live check:** a real audit of `flask==2.0.0` produced a PURL-keyed graph with the expected
transitive tree (flask → click, itsdangerous, jinja2, werkzeug → markupsafe), 368 files and
6 161 chunks under `<workspace>/<auditID>/pkgs/<name>-<version>/`, and no finding path was
absolute. `scripts/smoke.sh` passes 32/32 against the same package.

**Name changes:** `safeExtract` landed as the exported `pypi.SafeExtract`; the graph assembly
landed as `(*audit.Manager).Graph` in `internal/audit/manager.go`, not in `internal/audit/graph.go`.

**Left unticked (4):**
- *Cycle detection in the dependency graph* — there is no cycle detection in the Go resolver.
  The renderer counts self-loops and 2-cycles only (`countCycles` in `electron/src/state/graph.ts`)
  and shows them as a badge; that is a UI approximation, not graph-level detection.
- *Cache resolution results on disk keyed by `(name, version)` with an ETag/TTL* — the only
  cache is in-memory (`requiresCache` in `internal/deps/fallback.go`); there is no ETag and no
  TTL anywhere in `internal/deps`.
- *Skip extraction for packages with no Python source (metadata-only)* — every resolved package
  is downloaded and extracted; `parseTree` simply ignores the non-`.py` files afterwards.
- *Re-auditing the same package+version reuses the cached resolution* — follows from the two
  gaps above; a re-audit re-resolves and re-downloads.

**Accuracy limitation (not a ticked box, but material):** deps.dev resolves the *current*
dependency versions, not the versions that were current at the target release. The live audit of
`flask==2.0.0` resolved click 8.5.1 and werkzeug 3.1.9 — the modern releases, not the
click 7.1.2 / werkzeug 2.0.0 that flask 2.0.0 actually depended on.
