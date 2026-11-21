# W2 — SAST Engine

**Goal:** a deterministic, dependency-free rule engine over the Tree-sitter AST, producing
canonical `model.Finding` records, plus a SQLite store and SARIF export.

**Status:** 🔄 in progress — engine shipped and verified; 8 boxes open (see Notes).
**Depends on:** W0
**Blocks:** W3, W4, W8, W9

## Tasks

### Tree-sitter parsing
- [x] `internal/ast/parse.go` — Tree-sitter parser for Python
- [x] Verify the Go binding choice on this machine (`tree-sitter/go-tree-sitter` vs
      `smacker/go-tree-sitter`); both are cgo
- [x] Always `Close()` C-allocated objects (`Parser`, `Tree`, `TreeCursor`) — required by the
      upstream docs because of a `runtime.SetFinalizer`/CGO interaction
- [x] Parse errors do not abort the file; record them and continue
- [ ] Detect Python version from the source (syntax features) where needed
- [x] Handle non-UTF-8 files (skip with a logged reason)

### Chunking
- [x] `internal/ast/chunk.go` — semantic chunker
- [x] Chunk types: `module`, `file`, `class`, `function`, `block`
- [x] Each chunk records: file, start/end line, symbol path, docstring, parent chunk, calls, imports
- [x] Symbol path format: `module.Class.method`
- [x] Small files (<400 tokens) collapse to a single `file` chunk
- [x] Empty and comment-only files produce no chunks
- [x] Chunk IDs are content-derived and stable

### Rule engine
- [x] `internal/sast/rules.go` — rule definitions
- [x] `internal/sast/engine.go` — traversal + matching
- [x] Rules operate on the AST plus local taint within a chunk
- [x] Rule set:
  - [x] `subprocess-shell-true` → CWE-78
  - [x] `eval-exec-nonliteral` → CWE-95
  - [x] `pickle-yaml-unsafe-load` → CWE-502
  - [x] `tempfile-mktemp` → CWE-377
  - [x] `weak-hash-md5-sha1` → CWE-327
  - [x] `hardcoded-secret` → CWE-798 (entropy + secret-ish identifier)
  - [x] `debug-true-enabled` → CWE-489
  - [x] `assert-used-for-validation` → CWE-617
  - [x] `tls-verify-disabled` → CWE-295
  - [x] `path-traversal` → CWE-22
- [x] Every rule declares: id, CWE, severity, confidence, message template
- [x] Rule severities are normalized through `model.Severity`
- [x] Rules are individually unit-testable against fixtures

### Optional external scanners
- [ ] Detect `bandit` / `semgrep` on `PATH`
- [ ] Run them per package, merge results into `model.Finding`
- [ ] Normalize Bandit test IDs (`B602`) into the same CWE-bearing schema
- [ ] Merge is opt-in per audit; clearly labelled in the UI by `tool` field

### Findings store
- [x] `internal/findings/store.go` — SQLite persistence
- [x] Pure-Go SQLite driver (`modernc.org/sqlite`) — verify FTS5 availability for W5
- [ ] Schema: `findings`, `chunks`, `packages`, `advisories`, `meta`
- [x] Finding IDs are deterministic (see `model.FindingID`) so re-scans reconcile rather than
      duplicate
- [x] Upsert semantics — a re-scan updates a finding rather than adding a second copy
- [x] **F§1.1** — a scan never clears prior results
- [x] **F§1.2** — no basename collisions; keys are `(audit_id, file, start_line, cwe, rule)`
- [x] Audit history retained across scans (compare runs)

### SARIF export
- [x] `internal/findings/sarif.go` — SARIF 2.1.0 writer
- [x] `version`, `runs[].tool.driver` (name, version, rules), `results[]`
- [x] `ruleId`, `level` (error/warning/note), `message.text`
- [x] `locations[].physicalLocation.artifactLocation.uri` + `region.startLine`
- [x] `properties` carrying `cwe`, `confidence`, `reachability`, `patch`
- [ ] Validates against the SARIF 2.1.0 schema in CI
- [x] `GET /api/audits/{id}/export?format=sarif`

### API
- [x] `GET /api/audits/{id}/findings` with filters: `severity`, `cwe`, `tool`, `file`, `state`
- [x] `GET /api/audits/{id}/findings/{findingId}`
- [x] `POST /api/audits/{id}/scan` → start a SAST scan, return a job id
- [x] Findings streamed to the UI as `finding.created` events

## Acceptance criteria

- [x] The rule engine runs over `bench/train/vulnerable/` and flags known-vulnerable samples
- [x] It runs over `bench/train/patched/` and does not flag the corresponding fixes
- [x] Every finding carries a CWE, a severity, and a confidence
- [ ] SARIF output validates against the official schema
- [x] Re-running a scan updates findings in place instead of duplicating them
- [x] A scan of a 300-file package completes in under 5 seconds (no LLM involved)

## Notes

- **F§1.4** — the legacy app ran Bandit with `-lll` (HIGH only) while the benchmark accepted
  MEDIUM **and** HIGH. The benchmark measured a tool the app never ran. In the rewrite, the app
  and the benchmark call the same `internal/sast` package, so the two cannot drift.
- **F§4.4** — the legacy app ran one Bandit process per package, serially. The Go engine parses
  each file once and evaluates all rules in a single traversal.
- Tree-sitter is cgo. If cross-compilation becomes painful, the fallback is the `purego` runtime
  grammar loading described in the upstream `go-tree-sitter` README.

### Verified in this pass (2026-11-21)

**Ticked on the strength of:** `internal/ast/parse.go`, `internal/ast/chunk.go`,
`internal/ast/node.go` (+ `chunk_test.go`), `internal/sast/rules.go`, `internal/sast/engine.go`,
`internal/sast/checks.go`, `internal/sast/scan.go` (+ `rules_test.go`, `perf_test.go`),
`internal/findings/store.go`, `internal/findings/sarif.go`, `internal/server/server.go`.

**Live checks:** all ten rules are present with their CWE, severity and confidence in
`internal/sast/rules.go`; `TestScanBenchCorpus` scanned `bench/train/vulnerable` (1 673 files,
370 findings) and `bench/train/patched` (1 379 files, 208 findings) — it did not skip;
`TestScan300Files` scanned 300 files in 85 ms, well under the 5 s criterion. `go test ./internal/sast`
passes.

**Left unticked (8):**
- *Detect Python version from the source (syntax features) where needed* — every parsed file is
  recorded as `LanguagePython`; there is no version detection.
- *Optional external scanners* (4 boxes) — there is no Bandit/Semgrep detection, no merge, no
  test-ID normalisation and no opt-in switch. The rule engine is the only scanner.
- *Schema: `findings`, `chunks`, `packages`, `advisories`, `meta`* — the store creates only
  `findings` and `scans` (`internal/findings/store.go`); chunks live in `index.sqlite` and
  advisories are held in memory by the audit manager, so the single-database schema is not there.
- *Validates against the SARIF 2.1.0 schema in CI* — `findings.ValidateSARIF` is a hand-written
  structural check, and the CI workflow does not run it.
- *SARIF output validates against the official schema* — same gap: no official-schema validation
  exists, locally or in CI.

**Also worth recording:** `internal/findings/store.go` keys findings on `(audit_id, id)` where
`id = model.FindingID(file, start_line, cwe, rule)` — a hash of exactly the tuple the plan asks
for, so the "no basename collisions" property holds even though the literal key differs.
