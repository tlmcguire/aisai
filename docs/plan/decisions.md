# Decisions & Open Questions

Decision log for the AISAI rewrite. Update this file whenever a decision is made or reversed.

## Locked decisions

| # | Decision | Choice | Rationale | Date |
|---|----------|--------|-----------|------|
| D1 | Frontend framework | **Electron** (non-negotiable) | User decision | 2026-11-21 |
| D2 | Legacy Python app | Moved to `legacy/` (kept, not deleted) | User decision; preserves the benchmark notebooks' imports and the findings references | 2026-11-21 |
| D3 | Backend language | **Go**, no Python at runtime | Removes langchain, removes the per-file subprocess, single static binary | 2026-11-21 |
| D4 | Chat scope | **The audited package's source only** | Fits AISAI's mission; index is per-audit and discarded with it | 2026-11-21 |
| D5 | Retrieval | **Hybrid keyword + semantic**, RRF-fused | No score calibration needed between BM25 and cosine; degrades gracefully | 2026-11-21 |
| D6 | LLM access | **Local Ollama + optional BYOK** | Keeps the air-gap story while making the accuracy claim defensible | 2026-11-21 |
| D7 | Visualization | **vis-network** (already vendored in `web/`) | MIT/Apache-2.0, already in the repo, avoids adopting a new graph library | 2026-11-21 |
| D8 | Python parsing | **Tree-sitter** (cgo) | No Python AST parser exists in Go; Tree-sitter gives AST chunks for retrieval *and* the AST view | 2026-11-21 |
| D9 | Dependency resolution | **deps.dev**, PyPI metadata as fallback | Packaging-accurate; avoids `pip install` of untrusted packages | 2026-11-21 |
| D10 | Package identity | **PURL** (`pkg:pypi/name@version`) | Join key across OSV, CycloneDX, SPDX, deps.dev, GUAC | 2026-11-21 |
| D11 | SAST | **Go-native rules**, Bandit/Semgrep optional externals | Deterministic, instant, zero-dependency; keeps the richer tools available | 2026-11-21 |
| D12 | Findings store | **SQLite** | Fixes the results-wiped-on-every-scan and filename-collision bugs by construction | 2026-11-21 |
| D13 | Detection | **Two-stage: detect → verify** | Verification drops unverifiable findings; targets the >50% FP rate in the literature | 2026-11-21 |

## Open questions

| # | Question | Why it matters | Options | Status |
|---|----------|----------------|---------|--------|
| Q1 | Should the legacy Python app stay in the repo at all? | The plan assumes `legacy/` is frozen and kept. Deleting it would break the notebook imports and my findings references. | Keep frozen (current) / keep but strip notebooks / delete | **Assumed: keep frozen** — revisit if the repo gets unwieldy |
| Q2 | Is the OSV database bundled or downloaded on demand? | Offline-first argues for bundling; the PyPI-only scope argues for on-demand. Affects the release artifact size and the first-run experience. | Bundle (~tens of MB) / Download on demand / Both (bundle default, refreshable) | Open |
| Q3 | Should BYOK providers ship in v1? | Roughly doubles the LLM client surface, but without it the accuracy story stays tied to small local models. | Ship in v1 / Defer to v2 | Open |
| Q4 | Should the chat be exposed through MCP? | Cheap to add once W5 exists, but it commits the retrieval interface early. | Yes (v1) / Later | Open |
| Q5 | Which Electron editor for the code pane — CodeMirror 6 or Monaco? | Monaco is heavier but has better Python support and a built-in diff mode; CodeMirror is smaller and faster to load. Affects bundle size and the diff view quality. | CodeMirror 6 / Monaco / Neither — build a minimal viewer | Open |
| Q6 | Should the Electron app ship the Go binary per-platform, or download it? | Bundling is simpler and offline-safe; downloading enables independent Go releases. | Bundle per-platform / Download + verify signature / Both | Open |
| Q7 | Windows named-pipe transport, or just loopback TCP everywhere? | Unix sockets are cleaner but Windows needs named pipes; loopback TCP is simpler but exposes a port. | Named pipe + Unix socket / Loopback TCP only | Open |

## Risks

| # | Risk | Impact | Likelihood | Mitigation | Status |
|---|------|--------|------------|------------|--------|
| R1 | cgo (Tree-sitter) complicates cross-platform release | Medium | Medium | Per-platform CI matrix; evaluate the `purego` runtime grammar loading path from the upstream `go-tree-sitter` README | Open |
| R2 | Local 4B models score poorly on detection accuracy | High | High | BYOK path (D6); publish a recommended-models list; show the active model in the UI; report per-model accuracy in the benchmark | Open |
| R3 | Chat over untrusted code is a prompt-injection surface | High | Medium | Quarantine detection, data/instruction separation, tool allowlist, no network-capable tools, injection test corpus as a release blocker | Open |
| R4 | Retrieval underperforms naive whole-file prompting | Medium | Medium | Keep a whole-file prompt variant as a fallback; A/B on `bench/train` before committing to chunk-only | Open |
| R5 | Electron + Go = two toolchains, two security update streams | Medium | High | Single updater; pinned Go toolchain; publish an SBOM for both halves | Open |
| R6 | Scope creep — chat + scan + graph + MCP + SBOM + benchmark | High | High | Strict W-ordering; the parity gate (W10) must pass before new features | Open |
| R7 | OWASP BenchmarkPython is GPL-3.0 | Low | Low | Keep corpora out of the shipped binary; fetch at benchmark time; keep adapters in a separate package | Mitigated |
| R8 | The legacy app's Python is the reference for "correct" behavior | Medium | Medium | Freeze it in `legacy/` and use the parity gate as an explicit, documented gate rather than an accident | Open |
| R9 | `modernc.org/sqlite` may not ship FTS5 | Low | Medium | Verify early in W2; fall back to a hand-rolled inverted index behind the same interface | Open |

## Notes

- **The parity gate is a decision, not an accident.** The rewrite will legitimately find *more*
  findings than the legacy app (it restores MEDIUM severity, adds CWE/severity/confidence, and
  adds advisories). The gate should be "reproduces every legacy finding **and** adds the ones
  the legacy app was blind to" — not "produces the same set".
- **Do not delete `bench/`.** `data1551redacted.csv` (1,551 paired rows) and `train/` (5,831 files)
  are the raw material for both the benchmark and the few-shot prompts, and they are hard to
  regenerate.
- **`web/vis-9.1.2` is already MIT/Apache-2.0 licensed and already in the repo.** Do not replace
  it with a larger dependency without a reason — the vendored copy also guarantees the graph
  works offline.
