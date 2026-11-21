# W8 — MCP

**Goal:** expose AISAI's audit to external agents (server) and consume findings from external MCP
servers (client).

**Status:** 🔄 in progress — the MCP server ships and works; 13 boxes open (see Notes).
**Depends on:** W2, W4
**Blocks:** —

## Tasks

### Server
- [x] `cmd/aisai-mcp/main.go` — stdio transport
- [x] `internal/mcp/server.go` — tool registry and dispatch
- [ ] Also expose an HTTP/SSE transport for non-stdio clients
- [x] Evaluate the current Go MCP SDK (`github.com/modelcontextprotocol/go-sdk`); if immature,
      implement the JSON-RPC stdio transport directly
- [ ] Tool surface:
  - [x] `scan_package(package, version?)`
  - [x] `get_dependency_graph(auditId)`
  - [x] `list_files(auditId, path?)`
  - [x] `get_source(auditId, path)`
  - [x] `get_ast(auditId, path)`
  - [x] `run_sast(auditId)`
  - [x] `run_llm_scan(auditId, path?)`
  - [x] `list_findings(auditId, filters)`
  - [x] `get_finding(auditId, findingId)`
  - [x] `propose_fix(auditId, findingId)`
  - [x] `apply_patch(auditId, path, patch)`
  - [x] `export_report(auditId, format)`
  - [x] `search_code(auditId, query)`
  - [x] `ask_codebase(auditId, question)` — the chat, exposed as a tool
- [x] Structured tool descriptions (the description is what the calling model reads)
- [x] Tool results are truncated to a bounded size so a response cannot blow up the caller's
      context

### Client
- [ ] `internal/mcp/client.go` — connect to external MCP servers (stdio)
- [ ] Merge external findings into the canonical `model.Finding` model
- [ ] Deduplicate by `(file, start_line, CWE)` against local findings
- [ ] Record the contributing tool so the UI can show provenance
- [ ] Configured per audit; disabled by default

### Security
- [x] **F§5** — MCP servers execute tools, and the input is attacker-controlled source
- [x] The audit workspace is the only reachable path from any tool
- [ ] Every tool call is logged with arguments and result size
- [x] `apply_patch` writes only inside the workspace copy
- [x] No tool reaches the network or the user's real filesystem
- [ ] A malicious MCP server cannot be assumed benign: treat its output as untrusted data

### Docs
- [x] `docs/MCP.md` — tool reference, configuration, example client session
- [ ] Document that AISAI can be driven by Claude Code / Codex / Cursor

## Acceptance criteria

- [x] An external MCP client can list the tools and call `scan_package`
- [ ] `ask_codebase` returns a cited answer through MCP
- [ ] A local MCP server (Semgrep or OSV, if available) merges findings correctly
- [x] A tool result larger than the cap is truncated, not fatal
- [ ] Tool calls are visible in the UI activity log

## Notes

- Semgrep ships an MCP server exposing findings, triage, fixes, scans, rules, policies, and
  reports. That is the reference pattern for tool surface design.
- No competitor in the "local Python package auditor" space exposes MCP. Combined with the
  local-Ollama posture, this is a natural differentiator for agent-driven use.
- The MCP server must be runnable **headless** — which is why the Go backend is a library first
  and a server second.

### Verified in this pass (2026-11-21)

**Ticked on the strength of:** `cmd/aisai-mcp/main.go`, `internal/mcp/server.go`,
`internal/mcp/tools.go` (+ `server_test.go`, `tools_test.go`), `docs/MCP.md`.

All fourteen tools are registered in `internal/mcp/tools.go` with structured descriptions, and
the JSON-RPC 2.0 stdio transport is implemented directly in `internal/mcp/server.go`
(`initialize`, `notifications/initialized`, `ping`, `tools/list`, `tools/call`; protocol
`2024-11-05`). `internal/mcp/server_test.go` covers the tool listing, in-band tool errors and the
output truncation; `internal/mcp/tools_test.go` covers the workspace confinement of `apply_patch`
and the severity filter.

**Live check:** `scripts/smoke.sh` drives the server over stdio — `initialize` returns protocol
`2024-11-05` and 14 tools are registered.

**Left unticked (13):**
- *Also expose an HTTP/SSE transport for non-stdio clients* — not implemented; `cmd/aisai-mcp`
  records `--socket` / `--address` but explicitly ignores them ("the MCP transport is stdio").
- *`internal/mcp/client.go` — connect to external MCP servers (stdio)* — the file does not exist.
- *Merge external findings into the canonical `model.Finding` model* — not implemented.
- *Deduplicate by `(file, start_line, CWE)` against local findings* — not implemented.
- *Record the contributing tool so the UI can show provenance* — the `tool` field exists on
  findings, but nothing sets it from an external server.
- *Configured per audit; disabled by default* — there is no external-server configuration at all.
- *Every tool call is logged with arguments and result size* — `internal/mcp/server.go` dispatches
  and truncates, but logs nothing; `cmd/aisai-mcp/main.go` sends its own banner to stderr and no
  per-call line.
- *A malicious MCP server cannot be assumed benign: treat its output as untrusted data* — the box
  describes the **client** side, which does not exist.
- *Document that AISAI can be driven by Claude Code / Codex / Cursor* — `docs/MCP.md` does not
  name any of them; it documents the protocol, the tools and the configuration.
- *`ask_codebase` returns a cited answer through MCP* — the tool is implemented and delegates to
  the chat engine, which extracts citations, but no live model was available in this pass, so the
  end-to-end claim was not verified.
- *A local MCP server (Semgrep or OSV, if available) merges findings correctly* — follows from the
  missing client.
- *Tool calls are visible in the UI activity log* — the Electron app has no MCP activity view.
