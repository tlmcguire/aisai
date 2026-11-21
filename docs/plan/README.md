# AISAI Rewrite — Progress Dashboard

Tracking for the Electron + Go rewrite of AISAI.

- **Plan:** [`../../PLAN_rewrite_electron_go.md`](../../PLAN_rewrite_electron_go.md)
- **Findings:** [`../../NOTES_improvements.md`](../../NOTES_improvements.md)
- **Decisions & open questions:** [`decisions.md`](decisions.md)

## Legend

- `[ ]` not started, or not verified — a box is only ticked when the code, test or artifact that
  satisfies it has been confirmed to exist and to do what the box says.
- `[x]` done and verified
- Task files use GitHub-flavored checkboxes so they render as a live checklist.

## Workstream status

Last verified: **2026-11-21**.

| ID | Workstream | File | Status | Boxes | Blocked by |
|----|-----------|------|--------|-------|-----------|
| W0 | Foundations | [w0-foundations.md](w0-foundations.md) | 🔄 in progress | 46/50 | — |
| W1 | Package acquisition & graph | [w1-package-graph.md](w1-package-graph.md) | 🔄 in progress | 44/48 | — |
| W2 | SAST engine | [w2-sast-engine.md](w2-sast-engine.md) | 🔄 in progress | 51/59 | — |
| W3 | Code / AST / Diff UI | [w3-code-diff-ui.md](w3-code-diff-ui.md) | 🔄 in progress | 51/60 | — |
| W4 | LLM provider & detection | [w4-llm-detection.md](w4-llm-detection.md) | 🔄 in progress | 40/54 | — |
| W5 | Index & chat | [w5-index-chat.md](w5-index-chat.md) | 🔄 in progress | 46/56 | — |
| W6 | Patch loop | [w6-patch-loop.md](w6-patch-loop.md) | 🔄 in progress | 24/31 | — |
| W7 | Advisory layer | [w7-advisory.md](w7-advisory.md) | 🔄 in progress | 14/33 | — |
| W8 | MCP | [w8-mcp.md](w8-mcp.md) | 🔄 in progress | 26/39 | — |
| W9 | Benchmark | [w9-benchmark.md](w9-benchmark.md) | ✅ core done | 38/45 | — |
| W10 | Hardening & release | [w10-hardening-release.md](w10-hardening-release.md) | ✅ core done | 52/59 | — |

`Boxes` is *ticked / total* in the workstream file. `🔄 in progress` means the workstream's core
deliverables exist and have been verified, but some of the boxes listed in the plan are still
open; every one of those has a recorded reason in the workstream file's **Notes** section, under
*"Verified in this pass (2026-11-21)"*. Nothing is blocked: every remaining box is open work, not
work waiting on something else.

**W9 and W10 are marked "core done":** the metrics, the trivial baselines, the TF-IDF baseline,
both external-benchmark adapters, the injection corpus, the fuzz targets, the goldens, the
Playwright E2E, the CI matrix, the app SBOM and the release configuration all exist and have
been measured or run. What is left in those two files needs something this machine does not
have — a signing identity, a second platform to run the matrix on, or a model to measure the
LLM path with — and every one of those boxes says so.

The three largest gaps, in one place:

- **The LLM path has never been measured** (W9). There is no Ollama on this machine, so the
  LLM engine has no accuracy number at all. Every LLM-dependent claim is unverified rather
  than verified-false.
- **Reachability is scope-local, not call-graph based** (W7). `internal/sast/reachability.go`
  reports `reachable` or `unknown`, never `unreachable`, because a claim of unreachability
  needs a call graph that does not exist.
- **The app is not offline** (W10). The OSV database and Ollama can be local, but dependency
  resolution still needs deps.dev and the source archives still come from PyPI.

## Recommended order

```
W0 ──┬─> W1 ──┬─> W3 ──┬─> W6
     │        │        │
     └─> W2 ──┴─> W4 ──┬─> W5
                │       ├─> W8
                │       └─> W9
                └─> W7
                            ↓
                          W10
```

W3 lands before W4 so the UI exists before the LLM starts producing findings to display.

## Cross-cutting checklists

These apply to every workstream. Check them off per workstream as you go.

The 2026-11-21 pass verified these **once for the repository as a whole**, because they are
properties of the tree rather than of a single workstream. Where a box was left unticked, the
reason is given inline.

### Code quality
- [x] `go vet ./...` clean
- [x] `gofmt -l .` empty
- [x] Go tests pass (`go test ./...`)
- [x] Frontend typecheck passes (`npm run typecheck`)
- [x] Frontend lint passes (`npm run lint`)
- [x] No `TODO` left without an owner or an issue reference
- [x] Every exported Go symbol has a doc comment

### Security
- [x] No path traversal in any file-serving endpoint
- [ ] No network egress without an explicit, visible opt-in — **not true**: the backend calls
      PyPI, deps.dev and OSV unconditionally, and there is no offline switch. Left unticked.
- [x] LLM prompts treat retrieved code as data, never instructions
- [x] Tool calls are allowlisted and audit-workspace-scoped
- [x] No secrets in logs, events, or error strings
- [x] BYOK keys never reach the renderer process

### Correctness
- [x] Findings IDs are stable across re-scans
- [x] Finding paths are workspace-relative and slash-separated
- [x] Every severity/CWE value is normalized on ingest
- [x] Progress events are monotonic (never go backwards)

### Findings coverage
Every item from [`../../NOTES_improvements.md`](../../NOTES_improvements.md) must map to a
specific place in the new code. Use the mapping table in §6 of the plan.

- [x] F§1.1 — results no longer wiped on each scan
- [x] F§1.2 — no basename collisions in finding storage
- [x] F§1.3 — file paths and package identities are separate namespaces
- [x] F§1.4 — app and benchmark share one scanner configuration
- [x] F§1.5 — unlimited audits per session
- [x] F§1.6 — safe tar extraction with path validation
- [x] F§1.7 — no debug server bound to a public interface
- [x] F§1.8 — target package is never installed or executed
- [x] F§1.9 — n/a (Dash removed)
- [x] F§1.10 — dependency nodes keyed by PURL, version conflicts visible
- [ ] F§2.1 — AST view is readable, with a code view beside it — the AST is a tab next to Code
      and Diff, so the source is replaced rather than shown beside it. Left unticked.
- [x] F§2.2 — all UI text passes contrast checks
- [x] F§2.3 — single flex root, no absolute positioning
- [x] F§2.4 — no silently dropped style properties
- [x] F§2.5 — meaningful empty states
- [x] F§2.6 — streaming progress for long operations
- [x] F§2.7 — severity/confidence/CWE triage model exists
- [ ] F§3.x — prompt rework per W4 — F§3.1, F§3.3, F§3.4, F§3.6 and F§3.9 are done; F§3.5 (CWE
      taxonomy guidance) is not, and the few-shot and prompt-version work is open. Left unticked.
- [x] F§4.1 — advisory data attached to dependency nodes
- [x] F§4.2 — SAST broader than Bandit alone
- [x] F§4.3 — reachability computed — `internal/sast/reachability.go` annotates each finding
      with `reachable` or `unknown`. It is scope-local, not call-graph based, so it never
      claims `unreachable`.
- [ ] F§4.4 — concurrent, cached, chunk-scoped scanning — chunk-scoped scanning and the
      concurrency-limited worker pool are done; there is no result cache. Left unticked.
- [x] F§4.5 — benchmark has trivial baselines and recall-weighted metrics — the trivial
      baselines (always-vulnerable, always-safe, keyword, TF-IDF) and the metrics (precision,
      recall, F1/F2/F3, ROC-AUC, confusion matrix, per-CWE, patched-corpus FPR) are in
      `internal/bench`.
- [x] F§5 — MCP server implemented
- [x] F§6 — diff view with proposed changes

## Milestones

- [x] **M1 — Skeleton runs.** `aisai-server` starts, Electron launches it, a health check passes.
- [x] **M2 — Audit works.** Auditing a small package produces a dependency graph and findings.
- [ ] M3 — LLM works. A scan produces LLM findings with CWE/severity/confidence and a patch.
      *The pipeline is implemented and unit-tested, but no live model was available in this
      pass, so the end-to-end claim is unverified.*
- [ ] M4 — Chat works. Chat answers a question about the audited package with citations.
      *Same reason as M3.*
- [x] **M5 — Loop closes.** A proposed fix applies and a re-scan shows the finding resolved.
- [ ] M6 — Integrates. MCP server exposes the audit; SARIF/CycloneDX export works. *Both now
      work, but the CycloneDX document is 1.5, not 1.6, and carries no VEX section — the
      acceptance criterion for W7 is not met.*
- [ ] M7 — Publishable. *The benchmark numbers now reproduce from a clean checkout
      (`make bench`); the release does not, because nothing is signed.*

## Risks

Tracked in detail in [`decisions.md`](decisions.md#risks). Highest three:

| Risk | Impact | Status |
|------|--------|--------|
| cgo (tree-sitter) complicates cross-platform release | Medium | Mitigated — the CI matrix builds **natively** per leg, so cgo always finds a native compiler. Windows and Linux/arm64 have not run yet. |
| Local 4B models score poorly on detection accuracy | High | Open — mitigated by the BYOK path in W4, but **unmeasured**: no model was reachable, so there is no LLM accuracy number at all. |
| Chat over untrusted code is a prompt-injection surface | High | Mitigated — one quarantine in `internal/injection`, used by both the chat and the scan path, enforced by a 102-case release-blocking corpus. Twelve of the corpus's recorded false negatives were closed in this pass. |
| Dependency resolution reports today's versions, not the audited release's | Medium | Open — deps.dev resolves to current versions; `flask==2.0.0` resolved click 8.5.1 / werkzeug 3.1.9 (W1) |
| The audit still needs the network | High | Open — deps.dev and PyPI are required for every audit; there is no local mirror (W10) |
| The static engine barely beats a coin flip | High | Open — 51.4% accuracy against a 50.0% null baseline, with a recall of 10.1% (W9). The benchmark is honest about it; the engine still needs more rules. |

## Session log

| Date | Workstream | Notes |
|------|-----------|-------|
| 2026-11-21 | W0 | Repo restructured: `legacy/`, `bench/`, `web/`. Toolchain verified (Go 1.25.4, Node 26.4.0). `go.mod` + `internal/model` created; `go build`/`vet`/`gofmt`/`test` all pass. |
| 2026-11-21 | — | Tracking files created for W0–W10 plus the decisions log. |
| 2026-11-21 | W0–W10 | Progress pass: every checkbox re-checked against the code — **371 ticked, 162 left unticked**, each open box with a reason in its workstream file's Notes. Verified live: `go build`/`vet`/`gofmt`/`test` clean, `npm run typecheck`/`lint`/`test` (22)/`build` clean, a real `flask==2.0.0` audit over the Unix socket (PURL-keyed graph, 368 files, 70 findings, 2 OSV advisories), the apply → re-scan → revert patch loop, the SARIF/CycloneDX/SPDX exports, and `scripts/smoke.sh` 32/32. **W9 deliberately left entirely unticked** — another agent is writing `internal/bench`/`cmd/aisai-bench` concurrently. `internal/sbom`, `docs/MCP.md`, `docs/THREAT_MODEL.md` and `.github/workflows/ci.yml` landed during the pass and are reflected in W7/W8/W10. |
| 2026-11-21 | W9 | Benchmark workstream closed out. `internal/bench/metrics.go` adds the confusion matrix, precision, recall, F1, the recall-weighted F2/F3, ROC-AUC, the per-CWE breakdown and the false-positive rate on the patched corpus; `internal/bench/tfidf.go` adds a TF-IDF + naive Bayes baseline with a leakage-controlled split; `internal/bench/external/` adds the RealVuln and OWASP BenchmarkPython adapters; `internal/bench/leakage.go` adds the post-cutoff split. **Measured:** the static engine scores 51.4% accuracy against a 50.0% null baseline (precision 58.2%, recall 10.1%, F1 17.2%, patched FPR 7.2%); the TF-IDF baseline scores 51.3% on a held-out split; RealVuln P 49.0% / R 7.7% / F1 13.4%; OWASP BenchmarkPython P 24.2% / R 8.8% / F1 13.0%. The LLM path is **unmeasured** — no model was reachable. |
| 2026-11-21 | W10 | Hardening workstream closed out. The injection corpus (102 cases) is a release blocker and the twelve false negatives it recorded were closed; the patch applier panic and the MCP rune-splitting truncation were found and fixed; the WebSocket origin policy and the socket permissions were hardened; the Electron security posture, the golden findings and SARIF snapshots, the fuzz targets, the Playwright E2E (7/7), the CI matrix, the app SBOM, the version stamping and the release configuration landed. **Six of the ten documented threat-model gaps are now closed** (G2, G5, G6, G7, G8, G9); the other four (G1, G3, G4, G10) stay open and are recorded as such. Nothing is signed — that needs credentials this machine does not have. |
