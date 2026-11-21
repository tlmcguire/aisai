# AISAI Backend API Contract

**Version 1.0** — this document is the contract between the Go backend and the Electron
renderer. Any change here is a breaking change for the frontend.

The Go types live in [`internal/model/model.go`](../../internal/model/model.go). The TypeScript
mirror lives in [`electron/src/api/types.ts`](../../electron/src/api/types.ts). A contract test
fails CI if the two drift.

## Transport

- **Unix domain socket** at `$TMPDIR/aisai-<uid>.sock` (macOS/Linux) or a named pipe on Windows.
- Fallback: `127.0.0.1:<ephemeral>` — loopback only, never a public interface.
- The server prints **one line of JSON** to stdout on startup:

```json
{"type":"listening","transport":"unix","address":"/tmp/aisai-501.sock"}
```

  The Electron main process parses it. Nothing else may be written to stdout — logs go to
  stderr.

- **HTTP** carries request/response traffic.
- **WebSocket** at `/ws` carries server-pushed events and streaming chat.

## Conventions

- All timestamps are RFC 3339 UTC strings.
- All severities are one of `critical | high | medium | low | info`.
- All finding `file` fields are **workspace-relative, slash-separated** paths.
- All package identifiers are **PURLs** (`pkg:pypi/<name>@<version>`).
- Errors return `{"error": {"code": "...", "message": "..."}}` with a non-2xx status.
  `code` is a stable machine-readable string; `message` is human-readable and may change.
- All list endpoints return a JSON object (never a bare array) so fields can be added later.

---

## Health & config

### `GET /api/health`

```json
{
  "ok": true,
  "version": "0.1.0",
  "workspace": "/Users/x/Library/Application Support/AISAI",
  "uptimeSeconds": 42,
  "providers": [
    {"id":"ollama","kind":"ollama","url":"http://localhost:11434","ok":true,
     "models":["gemma3n:e4b"],"error":""}
  ]
}
```

### `GET /api/config`

Returns `{"config": { /* model.Config, redacted */ }}`. The API key is never
included.

```json
{"config": {"workspace":"...","providers":[],"ollamaUrl":"http://localhost:11434",
            "chatModel":"...","embedModel":"...","scanModel":"...","numCtx":8192,
            "keepAlive":"3m"}}
```

Returns the effective configuration (`model.Config`). Never includes API keys.

### `GET /api/providers`

```json
{"providers": [ /* model.ProviderInfo[] */ ]}
```

### `POST /api/providers/test`

Body: `{"id":"ollama","url":"...","model":"..."}`. Returns
`{"provider": {"id":"ollama","ok":true,"error":"","models":[...]}}` — a
`model.ProviderInfo` with `ok` and `error` populated. Never returns the key.

---

## Models

The model endpoints are what the model picker in the desktop app is built
on. They list the models installed on the provider, change the ones the
server uses, and release models from memory.

### `GET /api/models`

Returns the installed models and the ones currently selected.

```json
{"models": [{"name":"qwen3.5:4b","size":3300000000,"sizeText":"3.1GB",
             "family":"qwen3","parameterSize":"4.0B",
             "quantizationLevel":"Q4_K_M","modifiedAt":"2026-11-01T10:00:00Z"}],
 "chatModel":"qwen3.5:4b","scanModel":"qwen3.5:4b","embedModel":"embeddinggemma",
 "numCtx":8192,"keepAlive":"3m"}
```

`error` is set when the installed models could not be listed; the selected
models are still reported, because they remain in use.

### `POST /api/models`

Changes the models the server uses. Every field is optional: an empty name
leaves that role alone, so a caller can change one model without having to
read and rewrite the other two.

Body: `{"chatModel":"...","scanModel":"...","embedModel":"...","numCtx":8192,"keepAlive":"3m"}`.

The change takes effect immediately — the provider, the detection pipeline
and the chat resolve the model names on every request — and it is written
back to the configuration file, so it survives a restart. The API key is
never part of that file.

Returns the same shape as `GET /api/models`. When the configuration file
could not be written, the models are still applied and `error` explains
what failed.

### `POST /api/models/unload`

Releases models from the provider's memory.

Body: `{"models":["qwen3.5:4b"]}`, optional. Without a body, the models the
server is currently using are released.

Returns `{"unloaded":["qwen3.5:4b"]}`.

Ollama keeps a model resident until no request has used it for the
keep-alive window; this asks for the memory back immediately.

---

## Packages

### `GET /api/packages?name=<package name>`

Resolves a package name on the package index.

```json
{
  "package": {
    "name": "flask",
    "summary": "A simple framework for building complex web applications.",
    "latest": "3.1.3",
    "versions": ["3.1.3", "3.1.2", "3.0.3"]
  }
}
```

`versions` holds the newest releases, newest first, with the pre-releases
last. At most forty versions are reported.

A name that does not exist is **not** an HTTP failure: the lookup is a
question, not a fetch of a known resource. The answer carries the reason:

```json
{"package": {"name": "nosuchpackage", "error": "pypi: GET /pypi/nosuchpackage/json: not found"}}
```

This is what the package picker of the desktop app calls: it turns the name
the user typed into the project it refers to and fills the version dropdown
with the versions that actually exist.

---

## Audits

### `POST /api/audits`

Body:

```json
{"package": "flask", "version": "2.0.0"}
```

`version` is optional; omitted means "latest". Returns `{"audit": { /* model.Audit */ }}` with
status `pending`. The audit then progresses asynchronously — subscribe to `audit.progress` on
the WebSocket.

### `GET /api/audits`

```json
{"audits": [ /* model.Audit[] */ ]}
```

Newest first.

### `GET /api/audits/{id}`

```json
{"audit": { /* model.Audit */ }}
```

### `GET /api/audits/{id}/graph`

```json
{"graph": {"nodes": [ /* model.GraphNode[] */ ], "edges": [ /* model.GraphEdge[] */ ]}}
```

### `GET /api/audits/{id}/files`

```json
{"tree": { /* model.FileNode */ }}
```

### `GET /api/audits/{id}/file?path=<workspace-relative path>`

```json
{"file": { /* model.FileContent */ }}
```

`404` if the path does not exist. `400` if the path escapes the audit workspace.

### `GET /api/audits/{id}/ast?path=<path>`

```json
{"ast": { /* model.AST */ }}
```

`model.AST` carries the `language` the file was parsed with. AISAI parses
Python, JavaScript, TypeScript, TSX, Go, Rust, Java, C#, Ruby and C; a file
in another language has no syntax tree.

### `GET /api/audits/{id}/chunks?path=<path>`

```json
{"chunks": [ /* model.Chunk[] */ ]}
```

Every chunk carries the `language` it was chunked from.

### `DELETE /api/audits/{id}/workspace`

Deletes the downloaded sources of the audit.

```json
{"audit": { /* model.Audit */ }, "freed": 7561490, "freedText": "7.2 MB"}
```

Only the sandbox of downloaded code is removed. The findings, the graph and
the report of the audit are kept, so the result of the inspection survives
the deletion. `model.Audit` reports `sandboxBytes` and `sourcesDeleted`.

The downloaded packages live in `pkgs/` inside the workspace of the audit,
are never executed, and are stripped of the executable and setuid bits they
carried in the archive.

---

## Findings

### `GET /api/audits/{id}/findings`

Query parameters (all optional): `severity` (repeatable), `cwe` (repeatable), `tool`, `file`,
`state`, `package`, `limit`, `offset`.

```json
{"findings": [ /* model.Finding[] */ ], "total": 42}
```

### `GET /api/audits/{id}/findings/{findingId}`

```json
{"finding": { /* model.Finding */ }}
```

### `POST /api/audits/{id}/findings/{findingId}/state`

Body: `{"state": "suppressed", "suppressedUntil": "2026-12-01T00:00:00Z"}`

`state` is one of `open | confirmed | suppressed | fixed`. `suppressedUntil` is required when
`state` is `suppressed`.

Returns `{"finding": { /* model.Finding */ }}` with the updated finding.

### `POST /api/audits/{id}/scan`

Body: `{"engine": "sast"}` or `{"engine": "llm", "path": "optional/file.py"}`.

Returns `{"jobId": "..."}` immediately. Progress arrives as `scan.progress` events. Findings
arrive as `finding.created` events.

---

## Patches

### `POST /api/audits/{id}/propose-fix`

Body: `{"findingId": "..."}`

```json
{"patch": {
  "findingId": "...",
  "unified": "--- a/x.py\n+++ b/x.py\n@@ ...",
  "valid": true,
  "error": "",
  "summary": "Use hmac.compare_digest for constant-time comparison"
}}
```

`valid: false` means the model produced something unusable — `error` explains why and the
prose remediation is used instead.

### `POST /api/audits/{id}/apply-patch`

Body: `{"path": "pkg/mod.py", "unified": "<diff>"}`

```json
{"applied": true, "rescan": {"resolved": ["f-1"], "introduced": [], "unchanged": 7}}
```

The patch is applied, then the patched file is re-scanned and the difference is
reported as a `model.Rescan`:

| Field | Meaning |
|-------|---------|
| `resolved` | ids of findings that were present before the patch and are gone after it |
| `introduced` | ids of findings the patch introduced |
| `unchanged` | number of findings present both before and after |

Findings are matched by rule and CWE rather than by id, because a finding's
id includes its line number and a patch moves lines around. A finding that is
gone is marked `fixed` in the store, not merely hidden.

If the patch applies but the re-scan fails, the response is still
`{"applied": true, "rescan": null, "error": "..."}` — the change was made, and
saying otherwise would be a lie.

Only writes inside the audit workspace. The pre-patch contents are backed up
inside the workspace, so a change can always be undone.

### `POST /api/audits/{id}/revert-patch`

Body: `{"path": "pkg/mod.py"}`. Restores the pre-patch contents of a file and
re-scans it.

```json
{"reverted": true, "rescan": {"resolved": [], "introduced": ["f-2"], "unchanged": 7}}
```

Returns `not_found` when there is no un-reverted change for that file.

### `GET /api/audits/{id}/changes`

```json
{"changes": [{"path":"pkg/mod.py","unified":"...","appliedAt":"...","backup":".aisai-backups/...","reverted":false}]}
```

### `GET /api/audits/{id}/changes.patch`

Returns every un-reverted change as a single unified diff, with
`Content-Type: text/x-patch`. The body can be saved as a `.patch` file and
applied with `git apply`.

---

## Advisories

### `POST /api/audits/{id}/advisories`

Body: `{"offline": false}`. Fetches advisories for the audit's packages and
records them against the packages, so the dependency graph reports
`vulnCount` and `maxSeverity` per node.

Returns `{"advisories": [ /* model.Advisory[] */ ], "mode": "online"}`.

The `offline` field is accepted for compatibility. The effective mode is fixed
when the advisory client is constructed (`AISAI_OSV_DB` / `AISAI_ADVISORY_OFFLINE`),
because switching modes mid-session would make the results ambiguous.

An advisory lookup runs automatically in the background once an audit becomes
ready; this endpoint forces a refresh.

### `GET /api/audits/{id}/advisories`

Returns the advisories already fetched for the audit, without querying the
source.

```json
{"advisories": [ /* model.Advisory[] */ ], "mode": "online"}
```

### `GET /api/advisories/status`

```json
{"mode":"online","osv":{"available":true,"updatedAt":"2026-11-20T00:00:00Z","count":26428}}
```

`mode` is `online`, `offline`, or `unavailable` when no advisory source is
configured. `updatedAt` and `count` are zero in online mode.

---

## Export

### `GET /api/audits/{id}/export?format=sarif|cyclonedx|spdx|json`

Returns the file with the correct `Content-Type` and a `Content-Disposition`
filename.

| Format | Content type | Filename | Contents |
|--------|-------------|----------|----------|
| `sarif` | `application/sarif+json` | `aisai-<id>.sarif` | the findings as SARIF 2.1.0 |
| `cyclonedx` | `application/vnd.cyclonedx+json` | `aisai-<id>.cdx.json` | a CycloneDX 1.5 BOM: the dependency tree as components, the advisories as vulnerabilities, the findings as component properties |
| `spdx` | `application/spdx+json` | `aisai-<id>.spdx.json` | an SPDX 2.3 document: the dependency tree as packages, the advisories and findings as package comments |
| `json` | `application/json` | `aisai-<id>.json` | the raw findings |

An unknown format is a `bad_request`.

---

## Index & chat

### `POST /api/audits/{id}/index`

Builds (or rebuilds) the retrieval index. Returns `{"jobId": "..."}`.

### `GET /api/audits/{id}/index/status`

```json
{"ready": true, "phase": "complete", "progress": 1.0, "chunks": 1234, "embedded": 1234,
 "keywordOnly": false, "embedModel": "embeddinggemma"}
```

`keywordOnly: true` means embeddings are unavailable or still building — keyword search still
works.

### `POST /api/chat`

Body: `{"auditId": "...", "question": "...", "history": [ /* prior turns */ ]}`

Returns `{"jobId": "..."}`. The answer streams over the WebSocket as `chat.token` events
followed by `chat.done`.

---

## WebSocket

Connect to `/ws`. Server → client messages are `model.Event`:

```json
{"type":"audit.progress","auditId":"a1","phase":"acquiring","percent":0.35,
 "message":"Downloading flask==2.0.0"}
```

### Event types

| Type | When | Payload |
|------|------|---------|
| `audit.progress` | Audit phase change | `auditId`, `phase`, `percent`, `message` |
| `audit.done` | Audit finished successfully | `auditId` |
| `audit.error` | Audit failed | `auditId`, `error` |
| `scan.progress` | Scan progress | `auditId`, `phase`, `percent`, `message` |
| `finding.created` | A finding was produced | `auditId`, `finding` |
| `advisories.ready` | Advisory lookup finished for an audit | `auditId`, `data` = `{count, mode}` |
| `chat.token` | Streaming chat output | `jobId`, `data` = `{delta: "..."}` |
| `chat.done` | Chat turn complete | `jobId`, `data` = `{citations: [...], chunksUsed: [...]}` |
| `chat.error` | Chat turn failed | `jobId`, `error` |

Client → server messages:

```json
{"type":"subscribe","auditId":"a1"}
{"type":"unsubscribe","auditId":"a1"}
{"type":"ping"}
```

The server replies to `ping` with `{"type":"pong"}`.

---

## Error codes

| Code | Status | Meaning |
|------|--------|---------|
| `bad_request` | 400 | Malformed request or invalid query parameter |
| `not_found` | 404 | Unknown audit, finding, or file |
| `path_escape` | 400 | Path resolved outside the audit workspace |
| `conflict` | 409 | The resource is in a state that forbids the operation |
| `upstream_unavailable` | 502 | PyPI, deps.dev, OSV, or the LLM provider failed |
| `disabled` | 503 | The feature is disabled by configuration (e.g. `DISABLE_LLM`) |
| `internal` | 500 | Unexpected error — details are logged, not returned |
