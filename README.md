# AISAI

AISAI finds software supply chain vulnerabilities in Python packages. Give it a
package name and version; it resolves the dependency tree, downloads every
source archive safely, parses it into semantic chunks, runs static rules and an
LLM review over the code, and lets you interrogate the result — including
asking the LLM questions about the code itself.

Everything runs locally. The advisory database can be a local snapshot and the
model can be a local Ollama install, so an air-gapped machine can still run a
full audit.

> **This is a rewrite.** The original Python/Dash application is preserved
> under [`legacy/`](legacy/) and its documentation is in
> [`legacy/README-python.md`](legacy/README-python.md). See
> [`NOTES_improvements.md`](NOTES_improvements.md) for the audit that motivated
> the rewrite and [`PLAN_rewrite_electron_go.md`](PLAN_rewrite_electron_go.md)
> for the design.

## Why it was rewritten

The legacy app had real correctness problems, not just rough edges. The audit
found ten of them, including:

- Every LLM scan wiped every prior scan's results, so the findings list only
  ever showed the last file scanned.
- `__init__.py` files from different packages collided in the results
  directory, silently losing findings.
- Bandit ran at `-lll` (HIGH only) in the app but the benchmark accepted MEDIUM
  **and** HIGH — so the app and the benchmark disagreed about the same code.
- `tar.extractall` was called without path-traversal validation in four
  places, on archives downloaded from the internet.
- The Dash server bound `0.0.0.0` with `debug=True`, exposing a Werkzeug
  console with code execution.

The full list, with file and line references, is in
[`NOTES_improvements.md`](NOTES_improvements.md). Findings are referenced
throughout the new code as **F§n**.

## Architecture

```
┌──────────────────────────── Electron ────────────────────────────┐
│  renderer (React + Zustand)                                      │
│    graph · file tree · code view · AST view · findings rail      │
│    diff view · chat panel                                        │
│         ▲ preload (contextBridge, no node access)                │
│  main process                                                    │
│    spawns the Go sidecar, proxies HTTP + WebSocket                │
└──────────────────────────────┬───────────────────────────────────┘
                               │ Unix socket (never the network)
┌──────────────────────────────▼───────────────────────────────────┐
│  Go backend  cmd/aisai-server                                    │
│    internal/audit    lifecycle: resolve → download → index       │
│    internal/pypi     PyPI client + safe tar extraction (fuzzed)  │
│    internal/deps     deps.dev resolver                            │
│    internal/ast      tree-sitter parsing + semantic chunker      │
│    internal/sast     10 AST rules                                │
│    internal/scan     LLM detect → verify pipeline                │
│    internal/index    FTS5 + vectors, RRF fusion                   │
│    internal/chat     chat with prompt-injection quarantine       │
│    internal/findings SQLite store + SARIF export                 │
│    internal/advisory OSV client, online or offline               │
│    internal/patch    unified-diff apply, workspace-confined      │
└──────────────────────────────────────────────────────────────────┘
```

The backend is a library first and a server second: the same packages back
the Electron sidecar, the MCP stdio server, and the benchmark harness. That is
what makes it impossible for the app and the benchmark to disagree about what
a finding is.

## Requirements

| | |
|---|---|
| Go | 1.26+ (tree-sitter bindings use cgo, so a C toolchain is required) |
| Node | 20+ |
| Ollama | optional, for the LLM features |

If Go and Node are not on your `PATH`:

```bash
export PATH="/usr/local/go/bin:$PATH"   # go
export PATH="/usr/local/bin:$PATH"      # node, npm
```

## Quick start

```bash
make install-electron   # install renderer dependencies
make check              # gofmt, go vet, go test
make dev                # run the Electron app in development mode
```

To run the backend on its own:

```bash
make run                # listens on ./.aisai/aisai.sock
```

The server prints exactly one line of JSON on stdout — the address the
Electron main process connects to:

```json
{"address":"/path/to/.aisai/aisai.sock","transport":"unix","type":"listening","version":"0.1.0-dev"}
```

Everything else is logged to stderr.

### Without a model

Every LLM-backed feature can be switched off:

```bash
DISABLE_LLM=1 make run
```

Static analysis, the dependency graph, the file browser and the findings
store all still work.

## Configuration

Configuration is read from a JSON file (`--config`, or `AISAI_CONFIG`), then
overridden by environment variables.

| Variable | Purpose | Default |
|---|---|---|
| `AISAI_WORKSPACE` | Where audits are stored | `./.aisai` |
| `AISAI_CONFIG` | Path to the config file | — |
| `AISAI_SOCKET` | Unix socket path | temp dir |
| `AISAI_OLLAMA_URLS` | Comma-separated Ollama URLs, tried in order | `http://localhost:11434` |
| `AISAI_CHAT_MODEL` | Model for chat and the scan pipeline | `qwen2.5-coder:14b` |
| `AISAI_SCAN_MODEL` | Model for detection | falls back to the chat model |
| `AISAI_EMBED_MODEL` | Model for embeddings | `nomic-embed-text` |
| `AISAI_NUM_CTX` | Context window sent to the model | `8192` |
| `AISAI_SCAN_WORKERS` | Parallel detection workers | `4` |
| `DISABLE_LLM` | Turn off all LLM features | `false` |
| `AISAI_BYOK_BASE_URL` | OpenAI-compatible endpoint (optional) | — |
| `AISAI_BYOK_MODEL` | Model name for the BYOK endpoint | — |
| `AISAI_BYOK_API_KEY` | API key for the BYOK endpoint | — |
| `AISAI_OSV_DB` | Path to a local OSV database directory | — |
| `AISAI_ADVISORY_OFFLINE` | Force the offline advisory path | `false` |

The legacy environment names `OLLAMA_BASE_URL` and `OLLAMA_PRIMARY_MODEL` are
still honoured.

**`AISAI_NUM_CTX` matters.** The legacy app never set `num_ctx`, so Ollama
silently truncated at its 2048-token default and the model was reasoning about
half a file. The new server always sends `num_ctx` explicitly.

**The BYOK API key never leaves the backend.** It is not passed to the
renderer, not written to logs, and not included in error strings. The
config's `Redacted()` helper is used for every config surface.

## Air-gapped operation

1. Download an OSV database snapshot and point `AISAI_OSV_DB` at it.
2. Set `AISAI_ADVISORY_OFFLINE=1`.
3. Point `AISAI_OLLAMA_URLS` at a local Ollama.

With those set, the app performs no outbound network calls. The dependency
resolution uses deps.dev and the package downloads use PyPI, so a fully
offline audit needs a local mirror of both; that is not implemented yet.

## Safety properties

These are the properties the rewrite treats as non-negotiable.

- **The package under audit is never installed.** `pip install` executes the
  target's `setup.py`, which is exactly the code being audited. AISAI
  downloads source archives and resolves dependencies from metadata instead.
- **Archive extraction is validated.** Absolute paths, `..` escapes, and
  symlinks or hardlinks pointing outside the extraction root are all rejected;
  setuid/setgid bits are stripped; device nodes and FIFOs are skipped; total
  size and entry count are capped. `internal/pypi` has a fuzz target.
- **Path traversal has one chokepoint.** Every path that reaches the
  filesystem goes through `audit.ResolvePath`, which resolves symlinks and
  refuses anything outside the audit workspace.
- **The API is never on the network.** The server listens on a Unix socket.
- **Untrusted code is treated as data.** Chunks that look like instructions are
  quarantined before they reach the model, and the model is told to treat the
  code as data.
- **A candidate that cannot be verified is dropped, not downgraded.** The
  detect→verify pipeline exists because models reported false positives on
  safe code in over half of cases (ICPEC 2025).
- **Patches are verified by re-scanning.** Applying a fix re-runs the static
  rules over the patched file and reports what was resolved, introduced and
  left unchanged. Applying a fix only asserts that it worked; the re-scan is
  the evidence.

## Known limitations

- **Dependency versions are not historical.** deps.dev resolves a package's
  dependencies to their *current* versions, so auditing `flask==2.0.0` shows
  `click 8.5.1` rather than the `click 7.1.2` that was actually required at
  the time. The tree is accurate for the root package and approximate for
  transitive dependencies.
- **No reachability analysis yet.** Findings are reported per file and line
  but are not yet traced from an untrusted entry point.
- **Fully offline operation is not complete.** The advisory database and the
  model can both be local, but dependency resolution still uses deps.dev and
  package downloads still use PyPI. A local mirror of both is not
  implemented.
- **No signed releases or auto-updater yet.** The CI pipeline builds and
  tests; it does not publish a signed artifact.
- **OSV is the only advisory source.** No NVD or GitHub Advisory Graph
  enrichment beyond what OSV already carries.
- **The LLM path has not been measured.** There is no Ollama on the machine
  this was built on, so the detection pipeline is covered by unit tests with
  a fake provider rather than by a live benchmark run.

## Layout

```
cmd/aisai-server     backend entry point
cmd/aisai-mcp        MCP stdio server for external agents
cmd/aisai-bench      benchmark harness
internal/            Go packages (the actual implementation)
electron/            Electron app
docs/API.md          the frozen HTTP + WebSocket contract
docs/plan/           workstream tracking and decisions
bench/               benchmark corpus and the untouched notebooks
legacy/              the original Python application
web/                 vendored vis-network, for the offline graph
```

## Export

An audit exports as SARIF 2.1.0 (for CI), CycloneDX 1.5 and SPDX 2.3 (for
SBOM consumers), or raw JSON.

```bash
curl --unix-socket "$SOCKET" "http://localhost/api/audits/$AID/export?format=cyclonedx" -o bom.cdx.json
```

The CycloneDX document carries the dependency tree as components, the
advisories as vulnerabilities and the findings as component properties. SPDX
has no vulnerability vocabulary, so there the advisories and findings become
package comments.

## The MCP server

AISAI can be driven by an external agent — Claude Code, Codex, Cursor. The
MCP server exposes the full audit as tools: `scan_package`,
`get_dependency_graph`, `list_files`, `get_source`, `get_ast`, `run_sast`,
`run_llm_scan`, `list_findings`, `get_finding`, `propose_fix`, `apply_patch`,
`export_report`, `search_code` and `ask_codebase`.

```bash
make build-server
make mcp    # prints the configuration to paste into your agent
```

The agent can then audit a package and read the findings without touching
the workspace itself.

## Benchmark

`bench/data1551redacted.csv` holds 1,551 paired vulnerable/fixed snippets.
The Go harness runs the same `internal/sast` and `internal/scan` code the app
uses and writes a CSV in the exact schema the existing notebooks
(`bench/notebooks/*.ipynb`) already read — the notebooks are unmodified and
still work.

```bash
make bench          # full run
make bench-quick    # a small slice, no LLM calls
make bench-baseline # trivial baselines for comparison
make bench-tfidf    # the TF-IDF + naive Bayes baseline on a held-out split
make bench-metrics  # the static engine plus the full metric set as JSON
make bench-external # RealVuln and OWASP BenchmarkPython
```

### Where the numbers stand

On the full 1,551-pair corpus the static rule engine scores **1595/3102
correct (51.4%)** — against a **50.0%** null baseline, because the corpus is
balanced between vulnerable and patched code. That is an honest and rather
unflattering number, and it is the point of running the benchmark: the AST
rules on their own barely beat chance on this corpus.

The full metric set says the same thing more precisely:

| metric | static engine | null baseline |
|---|---|---|
| accuracy | 51.4% | 50.0% |
| precision | 58.2% | — |
| recall | **10.1%** | — |
| F1 / F2 / F3 | 17.2% / 12.1% / 11.0% | — |
| false-positive rate on the patched corpus | 7.2% | — |

The engine finds 156 of 1,551 vulnerabilities and wrongly flags 112 of 1,551
already-patched snippets. **The TF-IDF + naive Bayes baseline scores 51.3% on
a held-out split** — 1.3 points above chance, and no better than the shipped
rule engine. The 74% that ACM 10.1145/3733800.3763267 reports for TF-IDF was
measured on CVE *description text*, not on source code; on source code the
signal is far weaker. That is the honest finding, and it is reported rather
than hidden.

Against the external corpora, with the same static engine:

| corpus | precision | recall | F1 |
|---|---|---|---|
| RealVuln v3.1.1 (1,899 vulns, 283 FP traps) | 49.0% | 7.7% | 13.4% |
| OWASP BenchmarkPython (1,230 cases) | 24.2% | 8.8% | 13.0% |

The rules are tuned for the CWE classes they cover, and this corpus is
dominated by Django web patterns (CSRF, XSS, improper input validation) that
the ten rules do not target. A useful next step is not more rules but
measuring which CWE families the engine actually wins on — the per-CWE
breakdown of the metrics output does exactly that.

**The LLM path is unmeasured.** This machine has no Ollama, so there is no
LLM accuracy number at all and none is claimed. The detection pipeline is
covered by unit tests with a fake provider (verified finding → `vulnerable`,
abstention → `safe`, dropped candidate → `safe`, detection or verification
error → `unknown`), and the error path was exercised against an unreachable
Ollama.

The harness writes the exact CSV schema the existing notebooks already read,
including the `_scan_time_*_seconds` aliases their timing block needs. The
notebooks are unmodified. They were **not executed** here — pandas is not
installed on this machine — so the schema compatibility is verified against
the column names the notebooks read, not by running them.

## Development

```bash
make check          # gofmt + vet + tests
make test-electron  # renderer tests
make typecheck      # renderer typecheck
make lint           # renderer lint
make fmt            # format Go sources
make tidy           # tidy go.mod
make smoke          # end-to-end test against the real PyPI/deps.dev/OSV
```

`make smoke` starts the backend on a Unix socket, audits a real package, and
checks every stage: the health endpoint, the dependency graph, the file tree,
the code view, the AST view, the static scan, the advisory lookup, and the
apply → re-scan → revert patch loop. It needs network access but no LLM.

## License

MIT. The vendored `web/vis-9.1.2` is MIT/Apache-2.0. See
[`legacy/README-python.md`](legacy/README-python.md) for the original project's
licensing notes.
