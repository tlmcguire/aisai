# AISAI

**Automated Identification of Software Supply Chain Vulnerability**

AISAI analyzes a PyPI package: it resolves the dependency tree, scans the source for
vulnerabilities, and lets you talk to a local LLM about the code — all offline, all on your
machine.

> ## 🚧 Rewrite in progress
>
> AISAI is being rebuilt with an **Electron frontend** and a **Go backend**.
>
> - **Plan:** [`PLAN_rewrite_electron_go.md`](PLAN_rewrite_electron_go.md)
> - **Progress tracker:** [`docs/plan/README.md`](docs/plan/README.md)
> - **Findings that drove the rewrite:** [`NOTES_improvements.md`](NOTES_improvements.md)
> - **Decisions & open questions:** [`docs/plan/decisions.md`](docs/plan/decisions.md)
>
> The previous Python/Dash implementation is preserved in [`legacy/`](legacy/) and is still
> runnable. Its README is [`legacy/README.md`](legacy/README.md).

## What the rewrite adds

| | Legacy (Python/Dash) | Rewrite (Electron/Go) |
|---|---|---|
| Dependency graph | topology only | topology **+ known advisories** (OSV), keyed by PURL |
| SAST | Bandit, HIGH severity only | Go-native AST rules, CWE + severity + confidence, plus optional Bandit/Semgrep |
| LLM detection | one pass, prose remediation | two-stage detect → **verify**, produces a real **patch** |
| Code view | none — only an AST blob | code + AST + **unified diff** with apply/re-scan |
| Findings | red nodes | triage rail: severity, confidence, CWE, filter, suppression with expiry |
| Chat | none | hybrid retrieval (BM25 + embeddings) with **clickable citations** |
| Agent access | none | **MCP server** (stdio + HTTP) |
| Exports | PNG/JPG/SVG | SARIF 2.1.0, CycloneDX 1.6, SPDX 2.3, PNG/SVG |
| Runtime | Podman container + Ollama | single Go binary + Electron; Ollama local, BYOK optional |

## Repository layout

```
cmd/              Go binaries (aisai-server, aisai-mcp, aisai-bench)
internal/         Go packages: audit, pypi, deps, ast, sast, llm, index, chat, mcp, findings
electron/         Electron app (main / preload / renderer)
web/              Vendored front-end assets (vis-network 9.1.2, tom-select)
bench/            Benchmark corpora + notebooks (data1551redacted.csv, train/)
docs/plan/        Workstream tracking files with checkboxes
legacy/           Previous Python/Dash implementation, frozen
```

## Requirements

- **Go** 1.25+ (cgo enabled — Tree-sitter)
- **Node** 20+ and npm
- **Ollama** (optional but recommended) — a local model for scanning and chat

## Quick start (work in progress)

```bash
make dev     # start the Go backend and the Electron app
make test    # Go tests + frontend typecheck
make bench   # run the benchmark suite
```

See [`docs/plan/README.md`](docs/plan/README.md) for current status and the per-workstream
checklists.

## License

The vendored `web/vis-9.1.2` is MIT/Apache-2.0. See `web/` for license headers.
