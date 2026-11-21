# W0 — Foundations

**Goal:** a Go backend binary and an Electron shell that start together and talk over a Unix
socket, with a typed contract between them.

**Status:** 🔄 in progress — core shipped and verified; 4 boxes open (see Notes).
**Depends on:** —
**Blocks:** everything

## Tasks

### Repository restructure
- [x] Move Python app to `legacy/` (`git mv`, history preserved)
- [x] Move Python `requirements.txt` → `legacy/requirements-python.txt`
- [x] Move Python `Dockerfile` → `legacy/Dockerfile.python`
- [x] Move `PODMAN_MIGRATION.md` → `legacy/`
- [x] Move `graphs/` → `legacy/graphs/`
- [x] Move notebooks → `bench/notebooks/`
- [x] Move `data1551redacted.csv` → `bench/`
- [x] Move `train/` → `bench/train/`
- [x] Move `lib/vis-9.1.2`, `lib/tom-select`, `lib/bindings` → `web/`
- [x] Update `.gitignore` for the new layout
- [x] Move old `README.md` → `legacy/README-python.md`
- [x] Write a new root `README.md` for the rewrite

### Go module
- [x] `go.mod` with module path and Go version (`github.com/tlmcguire/aisai`, Go 1.25)
- [x] `internal/model/model.go` — canonical types (PURL, Package, Finding, Chunk, Audit, AST, events)
- [x] `internal/model/model_test.go` — PURL round-trip, severity ranking, stable finding IDs, path normalization
- [x] `internal/config/config.go` — load config from file + env + defaults
- [x] `internal/version/version.go` — build-stamped version string
- [x] Verify `go build ./...` passes
- [x] Verify `go vet ./...` passes
- [x] Verify `gofmt -l .` is empty
- [x] Verify `go test ./...` passes

### Server skeleton
- [x] `cmd/aisai-server/main.go` — flag parsing, config load, signal handling
- [x] `internal/server/server.go` — HTTP mux + router
- [x] `internal/server/events.go` — WebSocket hub, client subscription, broadcast
- [x] Unix socket listener (`/tmp/aisai-<uid>.sock` on macOS/Linux, named pipe on Windows)
- [x] `127.0.0.1:<ephemeral>` fallback when the socket path is unavailable
- [x] Print the listen address to stdout as one JSON line (Electron parses it)
- [x] `GET /api/health` → `{ok, version, providers, workspace}`
- [x] `GET /api/config` → effective config
- [x] Graceful shutdown on SIGINT/SIGTERM (drain WS clients, close listeners)

### Electron shell
- [x] `electron/package.json` with `electron`, `vite`, `typescript`, `zustand`
- [x] `electron/tsconfig.json` — strict mode
- [x] `electron/main.ts` — window creation, spawn sidecar via `utilityProcess.fork`
- [x] `electron/main.ts` — parse the sidecar's JSON address line, connect
- [x] `electron/preload.ts` — `contextBridge` exposing a narrow `window.aisai` API
- [x] `contextIsolation: true`, `nodeIntegration: false`, `sandbox: true`
- [x] `electron/renderer/index.html` — app shell
- [x] Renderer boots, shows a health status chip, verifies it reached the Go backend

### Contracts
- [x] `docs/API.md` — full HTTP + WebSocket contract
- [x] `electron/src/api/types.ts` — TypeScript mirror of `internal/model`
- [ ] Contract test that fails if Go and TS types drift

### Toolchain
- [x] Go 1.25.4 available at `/usr/local/go/bin/go`
- [x] Node 26.4.0 / npm 12.2.2 available at `/usr/local/bin/node`
- [x] Add a `Makefile` (`make build`, `make dev`, `make test`, `make lint`)
- [ ] Add `.tool-versions` or document required versions

## Acceptance criteria

- [ ] `make dev` starts the Go backend and the Electron app with one command
- [ ] The Electron window shows the backend version and Ollama reachability
- [x] `curl` against the socket returns a valid `/api/health` payload
- [x] Killing the Go backend produces a clear error in the UI, not a blank screen
- [x] No debug server is reachable from the network (verify with `lsof -i`)

## Notes

- **Transport:** Unix socket, with an ephemeral TCP fallback. Keeps the API off the network and
  removes port collisions. The Go binary must also be runnable standalone for MCP/CI.
- **Why `utilityProcess` over `child_process.spawn`:** `utilityProcess` (Electron ≥22) gives the
  child a Node-less, Electron-managed process with better lifecycle integration and no
  `ELECTRON_RUN_AS_NODE` leakage.
- **Keys:** the BYOK API key lives in the Go process only. The renderer asks for a scan; the Go
  side injects the key at request time. Never expose it over the socket.

### Verified in this pass (2026-11-21)

**Ticked on the strength of:** `internal/config/config.go`, `internal/version/version.go`,
`cmd/aisai-server/main.go`, `internal/server/server.go`, `internal/server/events.go`,
`internal/server/ws.go`, `electron/main.ts`, `electron/preload.ts`, `electron/index.html`,
`electron/src/api/types.ts`, `electron/src/views/toolbar.ts`, `Makefile`, `docs/API.md`.

**Live checks:** the server was started on a Unix socket; it printed exactly one JSON line
(`{"address":"/tmp/aisai-verify/aisai.sock","transport":"unix","type":"listening","version":"0.1.0-dev"}`),
answered `GET /api/health` with `{"ok":true,"version":"0.1.0-dev",...}` and `GET /api/config`
with the redacted configuration, showed **no** TCP listener under `lsof -i`, and shut down
cleanly on SIGTERM (`received terminated, shutting down`).

**Left unticked (4):**
- *Contract test that fails if Go and TS types drift* — `electron/src/api/types.ts` says
  "A contract test fails CI if this file and model.go drift", but no such test exists; the
  renderer suite is only `electron/test/units.test.ts` and `electron/test/mock-api.test.ts`.
- *`.tool-versions` or document required versions* — the file does not exist.
- *`make dev` starts the Go backend and the Electron app with one command* — the target runs
  only `cd electron && npm run dev`; the Go binary has to be built first (`make build`, or
  `make dev-backend` for the backend alone).
- *The Electron window shows the backend version and Ollama reachability* — Ollama
  reachability is shown by the provider chip (`electron/src/views/toolbar.ts`), but the
  backend version is never rendered: `AppController.fetchHealth()` in `electron/src/app.ts`
  is defined and never called.

**Also worth recording:** the toolchain moved on since this file was written — `go.mod`
declares `go 1.26.0` and the installed toolchain is `go1.26.0 darwin/arm64`, not Go 1.25.4.
The toolchain boxes stay ticked because a working toolchain is available at the documented
path; the version number in the text is stale.
