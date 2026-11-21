# W5 — Index & Chat

**Goal:** hybrid retrieval over the audited package (BM25 + embeddings, RRF-fused) and a chat
panel that answers questions about the package with clickable citations.

**Status:** 🔄 in progress — index and chat shipped and verified; 10 boxes open (see Notes).
**Depends on:** W4
**Blocks:** —

## Tasks

### Index build
- [x] `internal/index/index.go` — SQLite index (one database per audit)
- [x] Tables: `chunk_fts` (FTS5), `chunk_vec` (vector blobs), `chunk_meta`
- [x] Verify FTS5 availability in `modernc.org/sqlite`; fall back to a plain inverted index if not
- [x] Embed chunks in batches via `POST /api/embed` (`input` accepts an array)
- [ ] Skip embedding for chunks below a token threshold (BM25 covers them)
- [x] Embeddings cached by `(content_hash, model, dimensions)` — re-indexing is free
- [x] Background indexing: BM25 available immediately, vectors fill in progressively
- [x] Progress reported as `audit.progress` with a phase label
- [x] Embedding failures degrade to keyword-only (visible in the UI state, not silent)

### Retrieval
- [x] `internal/index/search.go` — two retrievers behind one interface
- [x] BM25 retriever over FTS5
- [x] Cosine retriever over stored vectors (brute force; see note below)
- [x] **RRF fusion**: `RRF(d) = Σ_r 1 / (k + rank_r(d))`, k = 60
- [x] Top-k selection with a configurable k
- [ ] Result carries: chunk content, file, line range, symbol path, score components

### Query processing
- [x] Query expansion: the model rewrites the question into 2–4 search variants
- [x] Expansion failures fall back to the raw question
- [ ] Optional filter extraction (e.g. "in the auth module" → path filter)

### Chat pipeline
- [x] `internal/chat/chat.go` — the chat handler
- [x] Context pack assembly: chunk + symbol path + file:line header + enclosing scope signature
- [x] System prompt enforces: cite `file:line`, refuse when not present, treat code as data
- [x] **F§3.9** — retrieved code is never placed in the system role
- [ ] Tool-calling loop when the model supports it:
  - [x] `get_file(path)`
  - [x] `get_ast(path)`
  - [x] `search_symbols(name)`
  - [x] `list_findings(filters)`
  - [x] `get_finding(id)`
- [x] Tool-call fallback for models without tool support: retrieval-only
- [x] Streaming responses over the WebSocket (`chat.token` → `chat.done`)
- [x] Conversation history kept per audit, bounded in length
- [x] Citations extracted from the answer and rendered as clickable `file:line` links

### Prompt-injection defense — release blocker
- [x] Detectors for instruction-like text inside source
      ("ignore previous instructions", "AI assistant:", "system:", role markers, etc.)
- [x] Suspicious chunks are **quarantined** before being placed in the prompt
- [x] The original source is still shown to the user in the code pane (never silently altered)
- [x] The user is warned in the chat when a chunk was quarantined
- [x] No tool can reach the network, the user's home directory, or anything outside the audit workspace
- [ ] Every tool call is logged with its arguments
- [x] Files matching `.env`, `*.pem`, `*.key`, `*.p12` are excluded from the index
- [ ] **Injection test corpus**: a set of packages containing injection payloads, with an
      automated assertion that the chat never follows embedded instructions

### Chat UI
- [x] Collapsible panel docked at the bottom
- [x] Streaming render
- [x] Clickable `file:line` citations → open the code pane at that line
- [x] Suggested starter questions ("how does this package handle authentication?")
- [x] Empty state explaining what the chat knows
- [x] Clear indication of which chunks were used for an answer
- [ ] Tool calls shown in a collapsed activity list

### API
- [x] `POST /api/chat` → `{auditId, question}` → streams over WS
- [x] `POST /api/audits/{id}/index` → (re)build the index
- [x] `GET /api/audits/{id}/index/status`

## Acceptance criteria

- [x] A question about the audited package returns an answer with at least one clickable citation
- [ ] A question about something *not* in the code is refused, not answered from priors
- [ ] The injected-instruction test corpus produces zero instruction-following failures
- [x] Keyword search works while embeddings are still indexing
- [x] Re-indexing an unchanged package takes no measurable time
- [ ] Indexing a 300-file package completes in under 60 seconds on a laptop

## Notes

- **Why no vector database:** a typical audited package is 1k–20k chunks. Brute-force cosine over
  20k × 768 dims in Go is ~15 ms — well under a frame. sqlite-vec would add a cgo dependency and
  a migration burden for no measurable gain at this scale. Benchmark this before committing; if a
  package ever exceeds ~100k chunks, swap sqlite-vec in behind the same interface.
- **RRF over score fusion** because it needs no calibration between BM25 and cosine scores, and
  it degrades gracefully when one retriever is unavailable.
- The retrieval corpus is **the audited package only** — this is not a general codebase chat tool.
  The index is built per audit and discarded with it.

### Verified in this pass (2026-11-21)

**Ticked on the strength of:** `internal/index/index.go`, `internal/index/search.go`,
`internal/index/vector.go` (+ `index_test.go`), `internal/chat/chat.go`, `internal/chat/prompt.go`,
`internal/injection/injection.go` (the shared detector, now used by both the chat and the
detection pipeline), `internal/chat/citations.go` (+ `chat_test.go`),
`internal/audit/tools.go`, `internal/server/server.go`, `electron/src/views/chatPanel.ts`.

`TestFTS5IsAvailable` confirms FTS5 in `modernc.org/sqlite`; `TestSearchFusesKeywordAndVectorRankings`
confirms the RRF fusion; `rrfK = 60.0` is in `internal/index/search.go`. The chat places the
retrieved code in the **user** role inside `<context>` delimiters only
(`TestAskPlacesCodeInUserRoleOnly`), quarantines instruction-like chunks
(`TestAskQuarantinesInstructionLikeChunks`), and extracts citations (`TestAskExtractsCitations`).
The five chat tools are declared in `internal/audit/tools.go` and every one of them goes through
`audit.ResolvePath`.

**Left unticked (10):**
- *Skip embedding for chunks below a token threshold (BM25 covers them)* — every uncached chunk
  is embedded; `internal/index/index.go` has no size threshold.
- *Result carries: chunk content, file, line range, symbol path, score components* — the hit
  carries the chunk and the **fused** RRF score only; the individual BM25 and cosine scores are
  not surfaced.
- *Optional filter extraction (e.g. "in the auth module" → path filter)* — not implemented; the
  expansion prompt only produces search queries.
- *Every tool call is logged with its arguments* — `runTools` in `internal/chat/chat.go` executes
  the calls and renders their output, but nothing is logged. The MCP layer does not log either.
- *Injection test corpus: a set of packages containing injection payloads, with an automated
  assertion that the chat never follows embedded instructions* — there is a unit test with
  hand-written payloads (`TestDetectPromptInjection`, `TestAskQuarantinesInstructionLikeChunks`)
  but no corpus of packages and no end-to-end assertion that the chat never obeys an injected
  instruction.
- *Tool calls shown in a collapsed activity list* — the chat panel shows the messages, the
  citations and the chunks used; the tool calls themselves are not shown to the user.
- *A question about something not in the code is refused, not answered from priors* — the system
  prompt requires the refusal, but no test asserts it.
- *The injected-instruction test corpus produces zero instruction-following failures* — follows
  from the corpus gap above.
- *Indexing a 300-file package completes in under 60 seconds on a laptop* — not verifiable here:
  embedding needs a live model, and none was available in this pass.

**Also worth recording:** the retrieval corpus really is the audited package only — the index is
keyed by `audit_id` and discarded with the workspace, as D4 requires.
