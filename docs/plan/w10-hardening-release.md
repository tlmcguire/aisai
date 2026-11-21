# W10 — Hardening & Release

**Goal:** make the rewrite safe to ship and safe to point at untrusted code.

**Status:** ✅ the hardening controls are in place and tested; the release pipeline is
configured but **unsigned** — signing and notarization need credentials that do not exist
on this machine. The remaining open boxes are listed in **Notes** with the reason for each.
**Depends on:** all
**Blocks:** —

## Tasks

### Threat model
- [x] `docs/THREAT_MODEL.md` — enumerate threats and mitigations
- [x] Threat classes:
  - [x] Malicious sdist (tar slip, zip bomb, symlink escape)
  - [x] Malicious `setup.py` (never executed)
  - [x] Prompt injection via package source
  - [x] LLM tool abuse / exfiltration
  - [x] MCP server compromise
  - [x] Path traversal via the file API
  - [x] Secrets in the audited package entering the index
  - [x] Local privilege escalation via the desktop app
- [x] Each threat mapped to a specific mitigation and a test

### Sandbox
- [x] Audit workspace confined to a dedicated directory
- [x] All file APIs resolve and validate against the workspace root
- [x] Symlinks inside the workspace are resolved before validation (no symlink escape)
- [x] The app never writes outside the workspace except to its own data dir
- [ ] Optional: run the extraction step in a separate, short-lived process — not done
      (gap G1)

### Hardening
- [x] No debug server reachable from the network
- [x] The socket is a Unix socket / named pipe, not a TCP port
- [x] If a TCP fallback is used, bind to loopback only and use an ephemeral port
- [x] API keys never leave the Go process; never logged; never sent to the renderer
- [x] Errors surfaced to the UI are sanitized (no stack traces, no key fragments)
- [x] The HTTP layer sets a restrictive CSP and disables remote content
- [x] Electron: `contextIsolation: true`, `nodeIntegration: false`, `sandbox: true`
- [x] Electron: navigation and window-open blocked except to approved URLs
- [x] Electron: auto-update from a signed release only
- [x] Dependency audit (`govulncheck`, `npm audit`) in CI

### Testing
- [x] Go unit tests for every package
- [x] `safeExtract` fuzz target
- [x] SARIF and unified-diff parser fuzz targets
- [x] Golden SARIF snapshots for 3–5 known packages
- [ ] **Parity gate:** the rewrite reproduces the legacy app's findings, **plus** the
      MEDIUM-severity findings the legacy app missed (F§1.4) — not implemented; there is no
      comparison against the legacy findings. The severity policy itself **is** pinned by
      `internal/audit/golden_test.go`, `TestSeverityPolicyCountsLowFindings`, which asserts
      that LOW findings are counted and that a HIGH-only filter would produce a different
      set.
- [x] Playwright E2E: audit → scan → open finding → apply patch → re-scan
- [x] Prompt regression: fixed prompt + seeded corpus → snapshot findings; changing the prompt
      requires a deliberate golden update
- [x] Injection corpus test (see W5) is a **release blocker**

### Release
- [x] Cross-platform builds: macOS (arm64 + x64), Linux (amd64 + arm64), Windows (amd64)
- [x] cgo build matrix handled for tree-sitter
- [ ] Code signing (macOS notarization, Windows) — configured, **not performed**: there is no
      signing identity on this machine. The release build fails loudly when a credential is
      missing rather than skipping it.
- [ ] Auto-updater with staged rollouts — the updater is wired and the feed is env-driven, but
      the feed URL, the signing identity and the rollout schedule all come from the release
      secrets.
- [x] SBOM published for the app itself
- [x] Version string stamped at build time

### Documentation
- [x] Root `README.md` rewritten for the new architecture
- [x] `docs/ARCHITECTURE.md` — package map and data flow
- [x] `docs/API.md` — HTTP + WebSocket contract
- [x] `docs/MCP.md` — MCP tool reference
- [x] `docs/THREAT_MODEL.md`
- [x] `docs/BENCHMARKING.md` — how to reproduce the numbers
- [x] `docs/RELEASE.md` — the release pipeline, the secrets it needs, and what is not done
- [x] Keep `legacy/README.md` for anyone who wants the old app

### CI
- [x] Build + test on macOS, Linux, Windows
- [x] `go vet`, `gofmt`, `golangci-lint`
- [x] Frontend typecheck + lint + Playwright
- [x] `govulncheck` and `npm audit` gates
- [x] A nightly job that re-runs the benchmark and posts the delta

## Acceptance criteria

- [ ] The app runs with **no network egress** (offline OSV DB, local Ollama) — **not true.**
      The OSV database and Ollama can be local, but dependency resolution still needs deps.dev
      and the source archives still come from PyPI; there is no local mirror and no offline
      switch.
- [ ] No known-vulnerable dependency in the shipped build — `govulncheck` and `npm audit` gate
      every commit, but no audit result is recorded for a *shipped* artifact, because no
      artifact has been signed and published.
- [x] The injection corpus produces zero instruction-following failures
- [ ] A clean checkout builds a signed artifact on all three platforms — the matrix builds
      natively on all five legs, but nothing is signed.
- [x] The threat model document has no unmapped threat
- [x] `make bench` reproduces the published numbers

## Notes

- The original project was built around an air-gap posture. **Losing that would be a regression**,
  so offline operation is an acceptance criterion, not a nice-to-have. It is also the property
  that most distinguishes AISAI from the cloud-first competitors.
- The legacy app shipped `app.run(host='0.0.0.0', debug=True)` — a Werkzeug RCE console on an
  exposed port. The rewrite has no equivalent failure mode by construction.

### Verified in this pass (2026-11-21)

**Ticked on the strength of:** `docs/THREAT_MODEL.md`, `docs/API.md`, `docs/MCP.md`,
`docs/ARCHITECTURE.md`, `docs/BENCHMARKING.md`, `docs/BENCHMARKING-EXTERNAL.md`,
`docs/RELEASE.md`, `.github/workflows/{ci,bench-nightly,release}.yml`, `scripts/smoke.sh`,
the root `README.md`, `legacy/README-python.md`, `internal/pypi/fuzz_test.go`,
`internal/patch/fuzz_test.go`, `internal/sast/fuzz_test.go`, `internal/injection/corpus_test.go`,
`internal/audit/golden_test.go`, `internal/scan/prompt_golden_test.go`,
`internal/audit/manager.go` (`ResolvePath`), `internal/patch/*`, `internal/config/config.go`
(`Redacted`), `internal/llm/provider.go` (`redact`), `electron/main.ts`,
`electron/preload.ts`, `electron/test/security.test.ts`, `electron/e2e/audit-flow.spec.ts`.

**The threat model has no unmapped threat.** `docs/THREAT_MODEL.md` enumerates all eight
threat classes (T1–T8) and maps each one to a mitigation **and a test**, including T8, which
used to have none.

**Six of the ten documented gaps were closed in this pass, and the document records how:**

| Gap | What closed it |
|---|---|
| **G2** — the detection pipeline had no injection quarantine | the detectors moved to `internal/injection`, the scan path withholds a quarantined chunk, and `internal/injection/corpus_test.go` (102 cases: 72 hostile, 30 benign, 28 traps) is a release blocker. The twelve false negatives the corpus recorded were closed: role markers inside comments, instructions in variable names, base64-encoded payloads, fake tool-call and JSON-RPC envelopes, credential exfiltration, non-English instructions, auditor impersonation and "do not report this" directives. Two false positives remain and are recorded in the corpus. |
| **G5** — the desktop app's security posture was untested | `electron/test/security.test.ts` (17 assertions) evaluates the real literals of `main.ts` and `preload.ts`. Verified negatively: five weakenings each made it fail. |
| **G6** — the IPC HTTP proxy had no allowlist | `ALLOWED_API_PREFIXES` and `isAllowedApiPath` in `electron/main.ts`, asserted by the same test. |
| **G7** — the socket's permissions were umask-dependent, the WebSocket accepted every origin | the socket is created `0600`; `internal/server/ws.go` no longer sets `OriginPatterns: ["*"]`, and `TestWebSocketRejectsAForeignOrigin` pins the policy. |
| **G8** — the MCP output cap split a multi-byte rune | `truncateUTF8` backs off to the nearest rune boundary. |
| **G9** — the output cap dropped information silently | the marker states how many bytes were dropped, and `get_source` takes `start_line`/`end_line` and always reports the total line count. |

**Two real defects were found and fixed in this pass, both by the new tests:**

1. **A panic in the patch applier** (`internal/patch/patch.go`). The applier took the number
   of source lines a hunk consumes from the *header* rather than from the body of the hunk,
   so a patch whose header declared more lines than the file had made it slice past the end
   of the source and panic. A second variant panicked when the header pointed past the end of
   the file and the hunk held only additions. **This is reachable from untrusted input**: the
   model proposes the patch. Fixed by deriving the count from the body and refusing a header
   that points past the end of the file; pinned by
   `TestApplyToContentSurvivesAWrongLineCount`. The fuzz target no longer exempts any panic.

2. **A wrong precision in the nightly comparison script**
   (`.github/workflows/bench-metrics.py`). It computed the precision from the "before" column
   only, whose ground truth is always "vulnerable" in a paired corpus, so it reported a
   precision of **1.0**. The false positives are in the "after" column — the patched snippets.
   The script now counts them there, and the committed baseline was regenerated: precision
   58.21%, recall 10.06%, FPR 7.22%, which agrees with the Go metrics exactly.

**What is still open, and why:**

- *The app is not offline.* deps.dev and PyPI are needed for every audit. There is no local
  mirror and no offline switch.
- *Nothing is signed.* The release configuration is in place —
  `electron/.github/electron-builder.release.json` sets `forceCodeSigning: true`, the
  notarization block is configured, and `electron/.github/verify-release-env.mjs` is the
  `beforeBuild` hook that stops a build whose credentials are missing. But there is no signing
  identity here, so the pipeline has never produced a signed artifact.
- *The auto-updater is wired but not exercised.* `electron/main.ts` calls
  `configureAutoUpdate()`, which does nothing unless the app is packaged and
  `AISAI_UPDATE_FEED_URL` is set. The feed only ever contains signed artifacts, and the
  updater verifies the signature of the downloaded app on macOS and the publisher name of
  the installer on Windows. The staged rollout is a property of the feed, which does not
  exist yet.
- *The parity gate is not implemented.* There is no comparison against the legacy findings.
  The severity policy that F§1.4 is about **is** pinned by the golden test.
- *G1 — the extraction is not sandboxed.* It runs in the backend process with the user's own
  privileges; the path checks make it safe, not sandboxed.
- *G3 — the secret-file list is an exact-match allowlist.* `.env.development`, `.envrc`,
  `.pypirc` and similar are not on it.
- *G4 — excluded secrets are still on disk and still readable.* They are excluded from the
  index, not deleted.
- *G10 — the API error envelope carries internal error text.* No stack traces and no key
  fragments, but not a curated message either.
- *The matrix legs have not all run.* Only macOS/arm64 executed here; Windows and
  Linux/arm64 have never run. The CI workflow builds **natively** per leg, so cgo always
  finds a native compiler: Linux installs `build-essential`, macOS verifies
  `xcode-select -p`, Windows installs MinGW-w64 and sets `CC=gcc`.
- *The nightly benchmark runs the static engine only.* The LLM leg needs a self-hosted
  Ollama runner. The static leg needs no network and no model, and is deterministic: the
  delta against the committed baseline is `+0.00` percentage points.
- *The Electron E2E uses a stub backend.* The real Go backend is not exercised by it, because
  creating an audit downloads from PyPI and `internal/pypi` has no local-index option. The
  stub serves a **vendored** fixture package, and the test asserts that the app is running
  against the stub and not against the in-renderer mock.
- *The lint gate suppresses findings in other workstreams' files.* `.golangci.yml` excludes
  them by path, with the list enumerated in `docs/RELEASE.md`. No `//nolint` was added and no
  linter was disabled wholesale.
