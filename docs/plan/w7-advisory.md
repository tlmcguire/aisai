# W7 — Advisory Layer

**Goal:** attach real vulnerability data to dependency nodes — turning the graph from descriptive
(topology) into advisory (risk).

**Status:** 🔄 in progress — OSV layer shipped and verified; 19 boxes open (see Notes).
**Depends on:** W1
**Blocks:** —

## Tasks

### OSV
- [x] `internal/advisory/osv.go` — OSV.dev API client
- [x] Query by PURL: `POST https://api.osv.dev/v1/query` with `{package:{name,ecosystem}, version}`
- [ ] Batch queries where the API supports it; rate-limit politely
- [ ] Offline mode: download `https://osv-vulnerabilities.storage.googleapis.com/PyPI/all.zip`
      into the workspace and query it locally
- [x] Offline mode sends **nothing** anywhere — preserving the air-gap property
- [ ] Offline DB refresh command (`aisai advisory refresh`)
- [x] Normalize OSV records into `model.Advisory` (id, summary, severity, CWE, fixed versions)

### PyPI advisories
- [ ] `internal/advisory/pypi.go` — `GET https://pypi.org/pypi/{name}/{version}/json` → `vulnerabilities`
- [ ] Use as a secondary source when OSV has no record
- [ ] Deduplicate against OSV by advisory ID

### deps.dev enrichment
- [ ] Fetch project metadata: license, description, homepage, OpenSSF Scorecard
- [ ] Surface "malicious package" advisories (`MAL-` prefixed OSV IDs) prominently

### Severity mapping
- [x] Map each source's severity onto `model.Severity`
- [x] Prefer CVSS base score when available; fall back to the source's label
- [ ] Record the source-specific severity alongside the normalized one

### Reachability
- [ ] `internal/audit/reachability.go` — compute reachability of a vulnerable dependency from the
      audited package
- [ ] Use the import graph (ported from the legacy `recursiveTree.py` logic)
- [ ] Classify each dependency: `reachable` / `unreachable` / `unknown`
- [ ] **F§4.3** — this is the strongest differentiator: nobody in the "local Python package
      auditor" space does reachability over a dependency graph

### Graph integration
- [x] Nodes carry advisory count and max severity
- [ ] The UI distinguishes "known CVE" from "code smells found by our scanner" — these are
      different kinds of risk and must not be conflated
- [ ] Filter: "only show reachable vulnerable dependencies"

### SBOM export
- [ ] CycloneDX 1.6 JSON writer, with a VEX section
- [x] SPDX 2.3 writer
- [x] `GET /api/audits/{id}/export?format=cyclonedx|spdx`

### API
- [x] `POST /api/audits/{id}/advisories` → fetch/refresh advisories for the audit
- [x] `GET /api/audits/{id}/advisories`
- [x] `GET /api/advisories/status` → OSV DB freshness, online/offline mode

## Acceptance criteria

- [x] Auditing a package with known CVEs (e.g. an old Django) shows those advisories on the
      correct nodes
- [ ] Offline mode completes with the network disabled
- [ ] CycloneDX output validates against the 1.6 schema
- [x] Node severity reflects the highest of advisory and scanner findings
- [ ] Reachability labels are computed for every dependency

## Notes

- **This is the missing half of the product.** The tool is named for software supply chain
  vulnerability identification and currently has zero advisory data. Everything else in AISAI
  analyzes code; this work answers "is this dependency known-vulnerable?"
- The offline OSV DB is a natural fit for the original project's air-gap design — PyPI has
  **26,428** listed vulnerabilities.
- deps.dev (Google) computes dependency graphs with the same algorithms as the packaging tools,
  so it is the accurate resolution source, and it also carries licenses, OSV advisories, and
  Scorecard signals.
- **PURL is the join key.** Every advisory record must be keyed by PURL or it cannot be attached
  to a graph node or exported in an SBOM.

### Verified in this pass (2026-11-21)

**Ticked on the strength of:** `internal/advisory/osv.go` (online + offline query, CVSS v3.1 base
score, severity normalisation, version-range filtering, fixed-version extraction),
`internal/advisory/status.go`, `internal/audit/advisory.go` (+ `advisory_test.go`),
`internal/server/server.go`, `internal/sbom/sbom.go` (SPDX 2.3 writer).

**Live check:** the `flask==2.0.0` audit fetched two OSV advisories online
(`CVE-2023-30861` high, `CVE-2026-27205` low), each with CWE ids and fixed versions; the graph
node `pkg:pypi/flask@2.0.0` carried `vulnCount: 4` and `maxSeverity: "high"`; and
`GET /api/advisories/status` reported `mode: online`.

**Left unticked (19):**
- *Batch queries where the API supports it; rate-limit politely* — the audit layer queries OSV
  once per package, sequentially, with no batching and no rate limiting.
- *Offline mode: download `all.zip` into the workspace and query it locally* — the *query* side
  exists (`queryOffline` reads `<db>/PyPI/<package>.json`), but nothing downloads the database,
  so an offline database has to be assembled by hand.
- *Offline DB refresh command (`aisai advisory refresh`)* — no such command; `cmd/` contains
  `aisai-server`, `aisai-mcp`, `aisai-bench` and `depstest`.
- *`internal/advisory/pypi.go` — PyPI `vulnerabilities`* — the file does not exist.
- *Use [PyPI advisories] as a secondary source* — not implemented.
- *Deduplicate against OSV by advisory ID* — there is only one source, so nothing is deduplicated.
- *Fetch project metadata: license, description, homepage, OpenSSF Scorecard* — license,
  summary and homepage are filled in by the **PyPI metadata fallback** path
  (`decorateRoot` in `internal/deps/fallback.go`), not by deps.dev; OpenSSF Scorecard is absent.
- *Surface "malicious package" advisories (`MAL-` prefixed OSV IDs) prominently* — no `MAL-`
  handling anywhere in `internal/`.
- *Record the source-specific severity alongside the normalized one* — `model.Advisory` has one
  `severity` field; the raw label is not kept.
- *`internal/audit/reachability.go` — compute reachability* — the file does not exist.
  `model.Finding.Reachability` is a field that nothing writes.
- *Use the import graph (ported from the legacy `recursiveTree.py` logic)* — not implemented.
- *Classify each dependency: `reachable` / `unreachable` / `unknown`* — not implemented.
- *F§4.3 — reachability is the strongest differentiator* — not implemented.
- *The UI distinguishes "known CVE" from "code smells found by our scanner"* — the graph tooltip
  reports the two counts separately, but the node colour is the **max of both**, so a known CVE
  and a scanner finding are still conflated in the one thing the user sees first.
- *Filter: "only show reachable vulnerable dependencies"* — no reachability, so no such filter.
- *CycloneDX 1.6 JSON writer, with a VEX section* — `internal/sbom/sbom.go` writes **CycloneDX
  1.5** (`"specVersion": "1.5"`) and has a `vulnerabilities` array but no VEX section.
- *Offline mode completes with the network disabled* — the offline query path exists but there is
  no database downloader, and dependency resolution still needs deps.dev and the downloads still
  need PyPI, so a full offline audit is not possible.
- *CycloneDX output validates against the 1.6 schema* — the document is 1.5 and no schema
  validation runs locally or in CI.
- *Reachability labels are computed for every dependency* — not implemented.

**Also worth recording:** `GET /api/audits/{id}/export?format=cyclonedx|spdx` now works — the
SBOM writer and the server wiring landed during this pass — and `scripts/smoke.sh` checks both
documents. `docs/API.md` already advertised these formats; before this pass the server answered
`"format must be sarif or json"`.
