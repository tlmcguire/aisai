# AISAI MCP Tool Reference

This document is the reference for the AISAI Model Context Protocol server: the transport
it speaks, the methods it implements, the fourteen tools it registers, and the exact
behaviour an agent should expect from each of them. The implementation lives in
[`internal/mcp/`](../internal/mcp/) and the binary in
[`cmd/aisai-mcp/`](../cmd/aisai-mcp/). Tool names are contractual — external agents are
configured against them, so they must not change.

## Transport

The AISAI MCP server speaks **JSON-RPC 2.0 over newline-delimited JSON on stdin/stdout**
(the MCP stdio transport).

- **stdout is the protocol channel and nothing else.** Every log line, warning and
  diagnostic goes to stderr. `cmd/aisai-mcp/main.go` sets `log.SetOutput(os.Stderr)`
  explicitly for this reason: a stray write to stdout corrupts the stream and makes the
  client drop the connection.
- One JSON object per line. Blank lines are skipped.
- A request without an `id` is a notification: it is processed, and no response is
  written.
- The scanner accepts lines up to 16 MiB, because a tool result can be large.

The protocol version reported by `initialize` is **`2024-11-05`**
(`mcp.ProtocolVersion`). The server identifies itself as `aisai`, version
`mcp.ServerVersion`.

## Supported methods

| Method | Behaviour |
|---|---|
| `initialize` | Returns `protocolVersion`, `capabilities` (`tools.listChanged: false`) and `serverInfo` (`name`, `version`). |
| `notifications/initialized` | Accepted and ignored. The bare `initialized` alias is accepted too. |
| `ping` | Returns an empty object. |
| `tools/list` | Returns the registered tools, **sorted by name**, so the list is stable between calls. Each entry carries `name`, `description` and `inputSchema`. |
| `tools/call` | Runs the named tool. Params are `{"name": ..., "arguments": {...}}`. |

Any other method returns JSON-RPC error `-32601` ("unknown method"). A `tools/call` for
an unregistered tool also returns `-32601` ("unknown tool"). Malformed JSON returns
`-32700`; malformed params or a missing tool name return `-32602`.

## Result convention

A successful `tools/call` returns:

```json
{
  "content": [{ "type": "text", "text": "<tool output>" }],
  "isError": false
}
```

A **tool failure** is reported **in band**, not as a protocol error: the response is
still a successful JSON-RPC result, but `isError` is `true` and the text is prefixed
with `Error: `. This lets the calling model read the failure and react to it instead of
the client tearing down the session on a JSON-RPC error.

**Output cap.** Tool output is truncated at **64 KiB**
(`mcp.Server.maxToolOutput`). Anything longer is cut and the suffix
`\n\n[output truncated: N of M bytes dropped. Ask for the rest explicitly.]` is appended, so
the caller knows how much is missing. The cut backs off to the nearest rune boundary, so a
multi-byte UTF-8 rune is never split.

**Finding listing cap.** A listing tool returns at most **200** findings
(`mcp.maxFindingsListed`) and prints `... N more finding(s) not shown` for the rest, so a
large audit cannot flood the model's context.

## The audit id

Most tools act on "the audited package". The tool set resolves that package in two
steps:

1. If the call passes an explicit `audit_id` argument, that id is used.
2. Otherwise the **current audit** is used — the id set by the most recent
   `scan_package` call (`Toolset.SetCurrentAudit`).

If neither yields an id, the tool fails with the error:

```
no audit in progress — call scan_package first
```

If an `audit_id` is given but does not exist, the tool fails with:

```
unknown audit "<id>" — call scan_package first
```

Both are returned in band, so the model sees the instruction and can recover by calling
`scan_package`.

## The fourteen tools

| Tool | Purpose | Required arguments |
|---|---|---|
| `scan_package` | Download and index a package, or copy and index a local folder; returns the audit id and makes it current. | `package` or `path` |
| `get_dependency_graph` | The resolved dependency graph with versions, severity and finding counts. | — |
| `list_files` | The file tree of the audit, with per-file finding counts. | — |
| `get_source` | The text of one file, plus its findings. Can page with `start_line`/`end_line`. | `path` |
| `get_ast` | The syntax tree of one Python file. | `path` |
| `run_sast` | Run the deterministic static rule engine over the whole audit. | — |
| `run_llm_scan` | Run the LLM detect→verify pipeline over one file. | `path` |
| `list_findings` | List findings, most severe first, with filters. | — |
| `get_finding` | The full detail of one finding, as JSON. | `finding_id` |
| `propose_fix` | Produce a unified diff that fixes one finding. | `finding_id` |
| `apply_patch` | Apply a unified diff inside the audit workspace. | `path`, `unified_diff` |
| `export_report` | Export the findings as SARIF 2.1.0 or JSON. | — |
| `search_code` | Hybrid keyword + semantic search over the indexed chunks. | `query` |
| `ask_codebase` | Ask a question about the package, answered from its source. | `question` |

Every tool except `scan_package` accepts an optional `audit_id` argument that overrides
the current audit.

---

### `scan_package`

Resolves, downloads and indexes a Python package so its source can be audited, and
returns the audit id.

- `package` (string) — the PyPI package name, e.g. `"requests"`. Required unless `path` is set.
- `path` (string) — an absolute path to a local project folder. The folder is copied and
  the copy is audited. Takes precedence over `package`.
- `version` (string, optional) — the version to audit, e.g. `"2.31.0"`. Defaults to the
  latest release.

The tool never installs the package and never runs its setup code: it only downloads the
source distribution into an isolated workspace and indexes it. The returned id becomes
the **current audit**, so later tools can be called without repeating it.

Returns a short text report:

```
Audit <id> is ready for requests 2.31.0.
audit_id: <id>
files: 412
chunks: 3104
known advisories: 1
  [high] GHSA-xxxx-yyyy-zzzz <summary>

Next: call run_sast to run the static rule engine, then list_findings to triage.
```

The advisory lookup is best-effort: an unreachable advisory source does not fail the
scan.

### `get_dependency_graph`

Returns the resolved dependency graph of the audit: every package with its version,
whether it is a direct dependency, its maximum advisory severity, its finding count and
its advisory count, followed by the dependency edges.

```
Dependency graph of audit <id>: 37 packages, 44 edges.
requests  v2.31.0  [direct]  severity=high  findings=3  advisories=1
...
edges:
  requests -> urllib3
```

### `list_files`

Returns the file tree of the audit as a text tree. Directories are printed as `path/`,
files as `path`, with the finding count and worst severity appended when the file has
findings:

```
pkgs/requests-2.31.0/requests/__init__.py  (3 findings, worst high)
```

The paths printed are workspace-relative and are exactly the paths `get_source`,
`get_ast` and `run_llm_scan` expect.

### `get_source`

Returns the text of one file of the audited package, followed by any findings in that
file.

- `path` (string, **required**) — workspace-relative path, e.g.
  `"pkgs/requests-2.31.0/requests/__init__.py"`.
- `start_line` (integer, optional) — first line to return, 1-based.
- `end_line` (integer, optional) — last line to return, inclusive.
- `audit_id` (string, optional) — the audit to read from; defaults to the current audit.

Output starts with a header line, then the file content, then a `Findings in this file:`
section if there are any. The header states the number of lines, and the range that was
returned when a range was asked for:

```
pkg/requests-2.31.0/requests/__init__.py (python, 42 lines)
pkg/requests-2.31.0/requests/__init__.py (python, lines 1-40 of 42)
```

The range exists because a long file can exceed the output cap; the total line count is
always present so that a caller can tell it is looking at a part of a file rather than the
whole of it. A range that starts past the end of the file is an error, not an empty answer.

Each finding is rendered in the compact one-line form:

```
[high] path/to/file.py:42 CWE-798 (sast, confidence 0.75) message
```

### `get_ast`

Returns the syntax tree of one Python file of the audited package: node types, line
numbers and enclosing scopes, one node per line, indented by depth.

- `path` (string, **required**) — workspace-relative path.

```
Syntax tree of pkgs/requests-2.31.0/requests/__init__.py (87 nodes)
module (line 1)
  function_definition (line 12)
```

### `run_sast`

Runs the static rule engine (`internal/sast`) over every file of the audit and stores
the findings. It is fast and needs no model.

When nothing is found the tool returns:

```
The static rule engine found no issues in audit <id>.
```

Otherwise it returns the standard finding rendering: a count line with a per-severity
breakdown, then one line per finding.

### `run_llm_scan`

Runs the LLM detect→verify pipeline over **one file** of the audited package and stores
the findings it confirms. It is slow — one model round trip per chunk plus one per
candidate finding — and it requires a configured LLM provider.

- `path` (string, **required**) — workspace-relative path of the file to analyse.

If no provider is configured the tool fails with:

```
LLM features are disabled: no LLM provider is configured (unset DISABLE_LLM and configure an Ollama endpoint or AISAI_BYOK_* credentials)
```

### `list_findings`

Lists the findings of the audit, most severe first, with severity, confidence, CWE and
location. Optional filters:

- `severity` (string) — `critical`, `high`, `medium`, `low` or `info`.
- `cwe` (string) — e.g. `"CWE-798"`.
- `state` (string) — `open`, `confirmed`, `suppressed` or `fixed`.

With no matches it returns `No findings matched in audit <id>.`

### `get_finding`

Returns the full detail of one finding as indented JSON: the offending code, the
data-flow evidence, the reachability, the remediation hint and any patch the scanner
already produced.

- `finding_id` (string, **required**) — the id printed by `list_findings`.

### `propose_fix`

Proposes a remediation patch for one finding: a unified diff that fixes the
vulnerability in the audited source, plus an explanation of why it is safe.

- `finding_id` (string, **required**).

If the finding already carries a patch from the scanner, that patch is returned without
a model round trip. Otherwise the model is asked for one under a 180-second timeout
(`mcp.fixTimeout`); the model must answer with `patch_unified`, `explanation` and
`confidence`.

The returned diff is validated against the current file content before it is returned,
so the tool reports whether it applies cleanly:

```
Proposed fix for <id> (CWE-798) in path/to/file.py:42
<explanation>
The diff applies cleanly to the current file.

<diff>

Pass this diff to apply_patch to apply it.
```

If no provider is configured and the finding carries no patch, the tool fails with the
LLM-disabled message above.

### `apply_patch`

Applies a unified diff to a file inside the audited package's workspace.

- `path` (string, **required**) — workspace-relative path.
- `unified_diff` (string, **required**) — the diff returned by `propose_fix`.

Three things are checked before anything is written: the path is resolved with
`audit.ResolvePath` and refused if it escapes the audit root; the diff is parsed; and
the diff is checked against the current file content. A diff that does not fit cleanly
is refused, so a patch never lands half-applied.

Returns `Applied the patch to <path> in audit <id>.`

### `export_report`

Exports the findings of the audit.

- `format` (string, optional) — `"sarif"` (default) or `"json"`.

Returns the document itself: SARIF 2.1.0 when `sarif` is requested, plain JSON
otherwise. Any other format fails with `format must be sarif or json, got "<value>"`.

### `search_code`

Searches the audited package with hybrid keyword and semantic retrieval over the
indexed chunks.

- `query` (string, **required**) — natural language or keywords, e.g. `"where the auth
  token is validated"`.
- `limit` (integer, optional) — maximum number of hits. Defaults to 10; a value of 0 or
  greater than 50 falls back to 10.

Each hit is printed as `file:start-end symbol (type, score)` followed by a one-line
snippet. When no embeddings are available the header says
`(keyword-only: no embeddings available)`.

### `ask_codebase`

Asks a question about the audited package's source and returns an answer grounded in
the retrieved code, with file and line citations.

- `question` (string, **required**).

The answer is followed by a `Citations:` section listing `file:line (symbol)` for each
citation. Requires a configured LLM provider and a chat engine; otherwise the
LLM-disabled error is returned.

---

## Configuring an agent

The `make mcp` target builds the server and prints the client configuration block:

```
make mcp
```

which prints:

```json
{
  "mcpServers": {
    "aisai": {
      "command": "/path/to/repo/bin/aisai-mcp",
      "args": ["--workspace", "/path/to/repo/.aisai"]
    }
  }
}
```

`bin/aisai-mcp` accepts these flags:

| Flag | Meaning |
|---|---|
| `--workspace` | override the workspace directory (default: the per-user config dir, e.g. `~/Library/Application Support/AISAI`) |
| `--db` | path of the findings database (default `<workspace>/findings.sqlite`) |
| `--index` | path of the retrieval index (default `<workspace>/index.sqlite`) |
| `--config` | path of the config file |
| `--socket` / `--address` | recorded only; the MCP transport is stdio |

A smoke test that sends a single `initialize` request:

```
make mcp-smoke
```

which pipes one JSON-RPC line into the binary.

The MCP server builds the same object graph as the HTTP server: config, findings store,
index, PyPI client, dependency resolver, SAST engine, LLM provider, advisory client,
audit manager, chat engine and finally the tool set. Because it shares the workspace
directory with `aisai-server`, both processes see the same audits, findings and index.

## Relationship to the chat tool set

The MCP tool set (fourteen tools) is **not** the same as the chat model's tool set
(five tools). The chat model, running inside the app, is given a deliberately read-only
allowlist: `get_file`, `get_ast`, `search_symbols`, `list_findings`, `get_finding`
([`internal/audit/tools.go`](../internal/audit/tools.go)). It cannot write, patch or
download. The MCP tool set is the operator-facing surface and includes `apply_patch`
and `scan_package`; the trust decision is that whoever starts the MCP server is
authorised to patch.

## See also

- [`docs/THREAT_MODEL.md`](THREAT_MODEL.md) — T5 covers the MCP server compromise class.
- [`docs/API.md`](API.md) — the HTTP + WebSocket contract used by the desktop app.
