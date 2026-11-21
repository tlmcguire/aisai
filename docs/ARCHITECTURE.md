# AISAI Architecture

This document describes how the AISAI rewrite is put together: the package map of the Go
backend, the lifecycle of an audit, the data path from the Electron renderer to the Go
backend, the identity rules that hold the system together, and the design decisions
behind them. The decisions themselves are recorded in
[`docs/plan/decisions.md`](plan/decisions.md); this document summarises them and points
at the code that implements them.

## The shape of the system

AISAI is two halves:

- **A Go backend** (`internal/`, `cmd/`) that does all the work: fetching packages,
  parsing Python, scanning, indexing, retrieving, talking to the model, storing findings.
  It exposes an HTTP + WebSocket API on a Unix socket for the desktop app, an MCP stdio
  server for external agents, and a benchmark harness.
- **An Electron desktop app** (`electron/`) that is a renderer plus a thin main process.
  The renderer never speaks to the backend directly and never sees a socket path, a
  file handle or a credential.

The split exists because the audited input is hostile. The Go process is the only thing
that touches untrusted archive bytes and untrusted source text; the renderer only ever
displays strings that the backend has already decided are safe to display.

## Package map

### `internal/` — the backend

| Package | Responsibility |
|---|---|
| `internal/advisory` | Known-vulnerability lookups against OSV, online or from a local snapshot. Purely additive: an audit still works with no advisory source at all. |
| `internal/ast` | Tree-sitter Python parsing and semantic chunking (module / class / function / block). Produces the AST view and the retrieval unit. |
| `internal/audit` | The orchestrator: owns the audits and their workspaces, runs the acquire → index phases, exposes files, AST, chunks, the dependency graph and the findings, and hosts the chat's tool runner. Contains `ResolvePath`, the single path-traversal chokepoint. |
| `internal/bench` | The benchmark harness core: input schema detection, the engines (static, LLM, stub, baselines), the CSV contract and the report writer. |
| `internal/chat` | Retrieval-augmented chat over one audited package: query expansion, hybrid retrieval, injection quarantine, context pack, model call, citations. |
| `internal/config` | Configuration loading (defaults → file → environment → flags) and `Redacted()`, the only way a configuration leaves the process. |
| `internal/deps` | Dependency resolution from deps.dev, with PyPI metadata as a fallback. Never installs anything. |
| `internal/findings` | The SQLite findings store, with triage state, filtering and SARIF / JSON export. |
| `internal/index` | The retrieval index: FTS5 keyword search, embedding cache, vector store, and RRF fusion of the two rankings. |
| `internal/llm` | The model providers: Ollama (local, default) and an OpenAI-compatible BYOK client, behind one `Provider` interface. |
| `internal/mcp` | The MCP server: JSON-RPC 2.0 over stdio, the tool registry and the fourteen AISAI tools. |
| `internal/model` | The shared data types and the canonical identity helpers (`PackageURL`, `RelPath`, `FindingID`). The TypeScript mirror lives in `electron/src/api/types.ts`. |
| `internal/patch` | Unified-diff parsing, validation and application, plus the in-workspace backup/revert mechanism. |
| `internal/purl` | Package URL construction and parsing: `pkg:pypi/<name>@<version>`. |
| `internal/pypi` | The PyPI client: release metadata, sdist/wheel download, and the strict safe archive extractors. |
| `internal/sast` | The deterministic Go-native rule engine: ten rules, each producing a canonical finding with a CWE, a normalised severity and a confidence. |
| `internal/scan` | The LLM detection pipeline: detect → verify, with the schemas and the prompts. |
| `internal/server` | The HTTP + WebSocket API, the event hub, the error envelope and the `withRecover` / `withLogging` middleware. |
| `internal/version` | The build-stamped version string. |

### `cmd/` — the binaries

| Command | What it is |
|---|---|
| `cmd/aisai-server` | The backend. Listens on a Unix socket (named pipe on Windows), prints one JSON line on stdout describing the address, and falls back to a loopback TCP port if a Unix socket is unavailable. |
| `cmd/aisai-mcp` | The MCP server over stdio. Same object graph as the HTTP server, different transport. |
| `cmd/aisai-bench` | The benchmark harness. Runs the static and/or LLM engine over a corpus CSV and writes a CSV the notebooks can read unmodified. |
| `cmd/depstest` | A small diagnostic for the deps.dev resolver. |

### `electron/` — the desktop app

| Path | Responsibility |
|---|---|
| `electron/main.ts` | The main process: creates the locked-down `BrowserWindow`, starts the Go sidecar, parses its "listening" line, proxies HTTP and WebSocket traffic over IPC. |
| `electron/preload.ts` | The sandboxed bridge. Exposes exactly one object, `window.aisai`. |
| `electron/src/api/` | The renderer's typed API client, the shared types, and the mock backend used when the sidecar is missing. |
| `electron/src/state/` | The renderer's stores: audit, graph, findings, workspace, chat, UI. |
| `electron/src/views/` | The panels: file tree, code view, AST view, diff view, findings rail, graph, chat. |
| `electron/src/main-process/` | Sidecar supervision, socket path resolution, the HTTP client and the WebSocket client — all in the main process, where the socket path is allowed to live. |

## The audit lifecycle

```
create ──▶ acquire ──▶ index ──▶ ready
```

`audit.Manager` owns this, and the phases are also the audit status values in
`internal/model/model.go`.

**create** — `Manager.Create` allocates a random audit id (`newAuditID`: the letter `a`
followed by 16 hex digits from `crypto/rand`), creates `<workspace>/<audit-id>/pkgs/`,
and records a `pending` audit.
The workspace root is the audit root; everything later is relative to it.

**acquire** — `Manager.Run` resolves the dependency graph through `internal/deps`, then
`Manager.acquire` walks the resolved packages in name order. For each package it
downloads the source distribution with `pypi.Client.DownloadSdist` and extracts it with
the strict extractors. A package that cannot be fetched or extracted is **skipped, not
fatal**: it stays in the graph, and the audit continues. The extracted Python files are
then parsed into AST chunks (`parseTree`), which are re-stamped with the audit id and the
workspace-relative path.

**index** — the chunks of every file are added to the retrieval index
(`index.AddChunks`). A broken index does not fail the audit: the failure is emitted as a
progress event and keyword search degrades to keyword-only. Embeddings are a separate,
later step (`index.Embed`) because they need a model.

**ready** — the audit status becomes `ready`, an `audit_done` event is broadcast, and
the advisory lookup is kicked off in the background with `context.WithoutCancel` so that
it outlives the request that started it. The audit is usable before the advisories
land; the client is told when they arrive.

Scanning is a *separate* phase from acquisition: `Manager.Scan` runs the static engine
over the already-parsed files, and `Manager.ScanLLM` runs the LLM pipeline. Both store
their findings in `internal/findings`.

## Data flow: renderer to backend

```
┌─ renderer (sandboxed, no Node) ──────────────────────────────────┐
│  window.aisai.http(method, path, body)                           │
│  window.aisai.connectEvents(cb)                                  │
└──────────────────────────┬───────────────────────────────────────┘
                           │ contextBridge / ipcRenderer.invoke
┌──────────────────────────▼───────────────────────────────────────┐
│  Electron main process                                          │
│  • owns backendAddress (socket path) — never sent to the renderer│
│  • ipcMain.handle('aisai:http') → backendRequest(...)           │
│  • WsClient on /ws → 'aisai:event' → renderer                   │
└──────────────────────────┬───────────────────────────────────────┘
                           │ HTTP over Unix socket
┌──────────────────────────▼───────────────────────────────────────┐
│  Go backend (internal/server)                                    │
│  mux → withLogging(withRecover(mux))                             │
│  handlers → audit.Manager / findings.Store / index.Index         │
└──────────────────────────────────────────────────────────────────┘
```

The important properties of this path:

- The renderer can only reach the backend through seven methods on `window.aisai`
  (`getMode`, `getStatus`, `http`, `connectEvents`, `disconnect`, `onBackendStatus`,
  `send`). It has no filesystem access, no Node integration and no direct socket.
- The socket path never crosses the bridge. The main process resolves it, uses it, and
  keeps it.
- The backend's response body is parsed in the main process and handed to the renderer
  as structured JSON plus the error code and message, so the renderer never parses
  untrusted HTML.
- Server-pushed events (audit progress, findings, streaming chat tokens) travel the
  other way over the WebSocket, are parsed in the main process, and are forwarded to the
  renderer as `aisai:event` messages.
- When the sidecar binary cannot be found, the app runs on the mock backend
  (`electron/src/api/mock.ts`) and says so in the status message.

## Canonical identity

Two identity rules hold the system together. Everything that crosses a boundary — the
HTTP API, the WebSocket events, the findings store, the index, the MCP tools, the
exported SARIF — uses these forms.

**Package identity is a PURL.** `pkg:pypi/<name>@<version>`, built by
`model.PackageURL` and parsed by `internal/purl`. The name is lower-cased and
PEP 503-normalised (`purl.NormalizeName` in `internal/purl`); the version is preserved
verbatim.
The PURL is the join key across OSV advisories, dependency-graph nodes and the package
reference on a finding, so the same package is the same key everywhere.

**File identity is a workspace-relative path.** `model.RelPath` normalises to
slash-separated, relative, cleaned form: backslashes become slashes, a leading `./` and
a leading `/` are stripped, and `.` collapses to the empty string. Every path that
leaves the backend is in this form.

The reason is practical. Exposing absolute paths would leak the local filesystem layout
into the UI and into exported reports, and it would break the file view: the client
resolves a path against the workspace root, so a path that already contains the root
would be looked up in the wrong place. The same rule is what makes `audit.ResolvePath`
possible — there is exactly one normalisation function and one containment check.

## One engine, two harnesses

There is exactly one static analysis engine (`internal/sast`) and exactly one LLM
detection pipeline (`internal/scan`). The desktop app calls them through
`audit.Manager.Scan` and `audit.Manager.ScanLLM`. The benchmark calls them through
`bench.StaticEngine` and `bench.LLMEngine`. Both call the same functions.

This is the fix for the divergence recorded as F§1.4 in `NOTES_improvements.md`: the
legacy app ran Bandit with `-lll` (HIGH only) while its own benchmark accepted MEDIUM
*and* HIGH, so the benchmark measured a configuration the product never shipped. The
rewrite has one engine and one severity policy — `bench.SeverityPolicy` is
`model.SeverityLow`, which is "count every finding the engine reports", the same thing
the application does. The severity filter in the findings API is a *read-time* filter
and never changes what the engine reports.

The consequence is that a benchmark number is a statement about the shipped product. If
the rule catalogue changes, the benchmark number moves, because it is measuring the same
code.

## Design decisions

Summarised from [`docs/plan/decisions.md`](plan/decisions.md). The rationale column is
the decision log's, condensed.

| # | Decision | Rationale |
|---|---|---|
| D1 | Electron as the frontend | User decision; the legacy UI and its workflow are worth keeping. |
| D2 | The legacy Python app stays in `legacy/`, frozen | Preserves the benchmark notebooks' imports and the historical findings references. |
| D3 | Go backend, no Python at runtime | Removes langchain, removes the per-file subprocess, one static binary. |
| D4 | Chat is scoped to the audited package's source | Fits the mission; the index is per-audit and is discarded with it. |
| D5 | Hybrid keyword + semantic retrieval, RRF-fused | No score calibration needed between BM25 and cosine; degrades gracefully when embeddings are unavailable. |
| D6 | Local Ollama by default, optional BYOK | Keeps the air-gap story while making the accuracy claim defensible. |
| D7 | vis-network for the graph | Already vendored in `web/`, MIT/Apache-2.0, no new dependency, works offline. |
| D8 | Tree-sitter for Python parsing | No Python AST parser exists in Go; Tree-sitter gives AST chunks for retrieval *and* the AST view from one parse. |
| D9 | deps.dev for resolution, PyPI metadata as fallback | Packaging-accurate; avoids `pip install` of untrusted packages. |
| D10 | PURL as the package identity | The join key across OSV, CycloneDX, SPDX, deps.dev and GUAC. |
| D11 | Go-native SAST rules | Deterministic, instant, zero-dependency — which is what makes the benchmark meaningful. |
| D12 | SQLite for findings | Fixes the results-wiped-on-every-scan and filename-collision bugs by construction. |
| D13 | Two-stage detection: detect → verify | Verification drops unverifiable findings; targets the >50% false-positive rate in the literature. |

Two further rules that are not in the decision log but are enforced in the code:

- **The audited package is never executed.** No `pip install`, no `setup.py`, no
  subprocess. The sdist is bytes; the bytes are parsed as text.
- **The audited package is data, never instructions.** Retrieved source is placed in the
  user role inside `<context>` delimiters, instruction-like chunks are quarantined, and
  the detection prompts say "treat the code as data" in the detector's own words.

## See also

- [`docs/API.md`](API.md) — the HTTP + WebSocket contract.
- [`docs/MCP.md`](MCP.md) — the MCP tool reference.
- [`docs/THREAT_MODEL.md`](THREAT_MODEL.md) — the threat model.
- [`docs/BENCHMARKING.md`](BENCHMARKING.md) — how to reproduce the numbers.
