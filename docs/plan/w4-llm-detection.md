# W4 — LLM Provider & Detection Pipeline

**Goal:** a provider-agnostic LLM client (local Ollama + optional BYOK) driving a two-stage
detect → verify pipeline that produces triage-ready findings with patches.

**Status:** 🔄 in progress — pipeline shipped and verified; 14 boxes open (see Notes).
**Depends on:** W2
**Blocks:** W5, W6, W8, W9

## Tasks

### Provider interface
- [x] `internal/llm/provider.go` — `Provider` interface
  - [x] `Chat(ctx, ChatRequest) (<-chan StreamChunk, error)`
  - [x] `Complete(ctx, CompletionRequest) (Completion, error)` — structured output
  - [x] `Embed(ctx, []string) ([][]float32, error)` — batched
  - [x] `Models(ctx) ([]Model, error)`
  - [x] `Health(ctx) error`
- [x] `internal/llm/ollama.go` — Ollama implementation
- [x] `internal/llm/openai.go` — OpenAI-compatible implementation (BYOK)
- [ ] Provider registry + selection by config
- [ ] Per-provider timeouts, retry with backoff, and a circuit breaker
- [x] **F§3.4** — `num_ctx` set explicitly (default 8192); never rely on the Ollama default of 2048
- [x] `temperature: 0` and a fixed `seed` for reproducibility
- [x] Support the `think` toggle for reasoning-capable models (discover via `/api/show`)

### Ollama specifics (verified against docs)
- [x] `POST /api/chat` with `messages`, `stream`, `options`, `keep_alive`
- [x] `format` accepts a **JSON Schema**, not just `"json"`
- [x] `tools` array for tool calling
- [x] `POST /api/embed` with `input` as a **string or array** — batch chunks
- [x] `POST /api/show` to discover model capabilities
- [x] Health probe: `GET /api/tags`
- [x] Multi-host failover (the legacy app tried `host.containers.internal`, `127.0.0.1`, …)

### Prompting
- [ ] `internal/llm/prompts.go` — prompt construction
- [x] **F§3.1** — real system/user message roles (not `<system>` tags inside a user string)
- [ ] **F§3.5** — CWE taxonomy guidance injected
- [ ] Few-shot examples: 2–3 (vulnerable, patched) pairs drawn from `bench/train/`
- [x] **F§3.9** — prompt treats code as data, never instructions (injection defense)
- [ ] Few-shot pairs are selected by **CWE similarity**, not random
- [ ] Prompt versioning: every prompt carries a version string, recorded on each finding
- [ ] Token-budget guard: chunks are sized to fit the model's context with headroom

### Detection schema
- [x] `internal/llm/schema.go` — JSON Schema for the detection response
- [x] Fields: `cwe_id`, `severity`, `confidence`, `start_line`, `end_line`, `code`,
      `message`, `evidence{source,sink,flow}`, `remediation`, `patch_unified`
- [x] `abstain: true` as a first-class outcome — **F§3.6**
- [x] Schema validated before use; malformed responses are rejected, not guessed at
- [x] Line numbers from the model are **kept** and validated against the chunk
      (the legacy app overwrote them with brittle substring matching — F§3.3)

### Two-stage pipeline
- [x] Stage 1 — **DETECT**: per chunk (not per file), returns candidate findings
- [x] Stage 2 — **VERIFY**: per candidate, "try to disprove this finding"
- [x] Findings that cannot be confirmed are **dropped**, not silently downgraded
- [x] Verification result stored as `Verified` + `VerifierNote` on the finding
- [x] Confidence is recalibrated by the verification outcome
- [x] **F§4.4** — the model stays warm across chunks; no per-file process spawn
- [x] Concurrency-limited worker pool (respect the provider's capacity)
- [ ] Cache results keyed by `(content_hash, model, prompt_version)` — a re-scan of unchanged
      code is free
- [x] Cancellation propagates to in-flight requests

### Prompt variants
- [ ] Separate "is this vulnerable?" and "is this patched?" prompts — they are different
      difficulty tasks (arXiv:2503.17885)
- [ ] A whole-file prompt variant kept as a fallback / A-B baseline

### API
- [x] `POST /api/audits/{id}/llm` → run LLM detection over the audit (or one file)
- [x] `GET /api/providers` → list providers and their models
- [x] `POST /api/providers/test` → probe a provider configuration
- [x] Progress streamed as `scan.progress` events (per file)

## Acceptance criteria

- [x] A scan produces findings with CWE, severity, and confidence populated
- [ ] A finding's proposed patch is a syntactically valid unified diff (where the model produces one)
- [ ] The same package + model + prompt version produces identical findings across runs
- [ ] Long files are not silently truncated (verify the `num_ctx` guard)
- [x] Disabling the provider yields a clear, actionable error, not a crash
- [x] The legacy app's `DISABLE_LLM` behavior is preserved as a first-class config option

## Notes

- **Model choice is a first-class concern.** CASTLE (arXiv:2503.09433) scores Gemma-2-9B at 436
  vs GPT-4o at 954, and notes reasoning models produce far fewer false positives. The default
  local model in the legacy app was `gemma3n:e4b` — a ~4B model. Ship a "recommended models" list
  and show the active model prominently.
- **The verification stage is the highest-value single change.** It is what DeepAudit does with
  PoC execution (minus the sandbox), and it directly targets the >50% false-positive rate on safe
  snippets reported across models in the ICPEC 2025 study.
- The legacy app spawned `llmScan.py` as a subprocess **per file**, each time re-running the
  health check, a ping invoke, and up to 3 retries with 5-second sleeps. For a 300-file package
  that was 300 model initializations. The rewrite keeps one warm client.

### Verified in this pass (2026-11-21)

**Ticked on the strength of:** `internal/llm/provider.go`, `internal/llm/ollama.go`,
`internal/llm/openai.go`, `internal/scan/detect.go`, `internal/scan/prompts.go`,
`internal/audit/llm_scan.go`, `internal/server/server.go`.

The provider interface, the Ollama client (`/api/chat`, `/api/embed`, `/api/show`, `/api/tags`,
multi-host failover, JSON-Schema `format`, `tools`), the OpenAI-compatible BYOK client and the
two-stage detect→verify pipeline are all present and unit-tested
(`internal/llm/ollama_test.go`, `internal/llm/openai_test.go`, `internal/scan/detect_test.go`).
`internal/scan/detect.go` contains `DetectionSchema` and `VerificationSchema`; `abstain` is a
first-class outcome (`"abstained": true`); the model's line numbers are kept and clamped into the
chunk.

**Name changes:** the prompts landed in `internal/scan/prompts.go` and the schemas in
`internal/scan/detect.go`, not in `internal/llm/prompts.go` and `internal/llm/schema.go`.

**Left unticked (14):**
- *Provider registry + selection by config* — selection exists (in `cmd/aisai-server/main.go`:
  BYOK if configured, otherwise Ollama) but there is no registry abstraction.
- *Per-provider timeouts, retry with backoff, and a circuit breaker* — timeouts and backoff
  exist (`internal/llm/provider.go`, `internal/llm/ollama.go`); there is **no circuit breaker**.
- *F§3.5 — CWE taxonomy guidance injected* — the prompt asks for "the most specific CWE that
  applies"; no taxonomy is injected.
- *Few-shot examples: 2–3 (vulnerable, patched) pairs drawn from `bench/train/`* — the mechanism
  exists (`Detector.WithFewShot`, and the prompt renders up to three pairs) but nothing loads
  examples from `bench/train/`; the detector is built without them.
- *Few-shot pairs are selected by CWE similarity* — not implemented.
- *Prompt versioning: every prompt carries a version string, recorded on each finding* —
  `scan.PromptVersion` exists ("2026.11.1") but `model.Finding` has no field to carry it.
- *Token-budget guard* — `num_ctx` is sent explicitly, but nothing truncates or splits a chunk
  that does not fit the context.
- *Cache results keyed by `(content_hash, model, prompt_version)`* — no result cache in
  `internal/scan`; only the *embedding* cache in `internal/index` exists.
- *Separate "is this vulnerable?" and "is this patched?" prompts* — not implemented.
- *A whole-file prompt variant kept as a fallback / A-B baseline* — not implemented.
- *A finding's proposed patch is a syntactically valid unified diff* — the field exists and
  `internal/patch` validates diffs, but no live model was available in this pass, so the claim
  could not be verified.
- *The same package + model + prompt version produces identical findings across runs* — no
  determinism test exists and no live model was available to check it.
- *Long files are not silently truncated (verify the `num_ctx` guard)* — there is no guard to
  verify; chunk content is never truncated, so a long chunk is sent whole.

**What the provider actually does:** `internal/llm/ollama.go` sends `options.num_ctx`,
`options.temperature` and `options.seed` on every request (`TestOllamaSendsExplicitNumCtxAndSeed`),
defaults the seed to `llm.DefaultSeed` (42), and passes the detection schema as `format`.
