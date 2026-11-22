# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

The interface is web technology (TypeScript, CSS) running in an Electron desktop shell for macOS, Windows and Linux. Its design language is shared across the three systems; only the window chrome adapts to each (inset traffic lights on macOS, a controls overlay on Windows and Linux).

## Users

- **Security engineers (primary):** audit third-party Python packages, or a project folder on their own machine, for supply-chain vulnerabilities. They resolve the dependency tree, read the code, triage findings, and draft and apply fixes. They often work for hours at a time.
- **Developers vetting dependencies:** check a package before adding or upgrading it. They want a fast, trustworthy verdict and go less deep.
- **Researchers:** benchmark static rules against LLM detection with the bench harness (`cmd/aisai-bench`, `bench/`).
- **AI agents via MCP:** Claude and other agents run audits, triage and fixes through the MCP server (`cmd/aisai-mcp`), with a human reviewing the results.

## Product Purpose

Given a package and a version, or a local folder, AISAI:

1. resolves the dependency tree;
2. downloads every source archive safely;
3. parses the code into semantic chunks in 14 languages;
4. runs static rules and an LLM review;
5. lets the user question the result, including chatting with the code.

Success means a user can tell what is dangerous in a dependency, show where it is, and leave with a reviewed fix or a recorded triage decision.

## Positioning

- **Local-first and air-gappable:** a full audit can run offline, with a local advisory snapshot and a local model (Ollama, vLLM, llama.cpp or LM Studio). Code leaves the machine only when the user explicitly picks a hosted provider with their own key.
- **One engine everywhere:** the desktop app, the MCP server and the benchmark share one Go backend, so they can never disagree about what a finding is.
- **Evidence over verdicts:** every finding and every chat answer cites the exact file and line. A fix is a diff the user reviews before applying it.
- **Safe with untrusted code:** a package is never installed and its setup code never runs. Archives are extracted into a sandbox with path-traversal checks.

## Operating Context

The core workflow: audit → scan (static, then LLM) → open a finding → read the code and its syntax tree → propose a fix → apply the patch → rescan.

Users work in long sessions, usually in a dim room, reading code for hours rather than glancing at a dashboard. Settings cover:

- the model server;
- the models;
- the downloaded sources (the per-audit sandboxes, which can be deleted once an audit is done);
- the layout;
- MCP client setup.

Agents reach the same workflow through 20 MCP tools.

## Capabilities and Constraints

- **Languages:** Python has the full rule set, with taint and scope tracking. JavaScript, TypeScript, TSX, Go, Rust, Java, C#, Ruby, C, C++, PHP, Scala and shell scripts get heuristic pattern rules that do not follow data flow.
- **Providers:** local servers (Ollama, vLLM, llama.cpp, LM Studio) or hosted services with the user's own key (OpenAI, Anthropic, Gemini, OpenRouter, Groq, Mistral). The provider can be switched without a restart. Keys live in the OS keychain and are never written to the config file.
- **Architecture:** the Go backend (`cmd/aisai-server`) runs as a sidecar of the Electron app. It talks over a Unix socket, never the network. The renderer reaches it only through a narrow preload bridge with an allowlist of API paths.
- **Terminology:**
  - *audit*: one run over a package or folder;
  - *finding*: an issue with a severity, a CWE, a confidence and a triage state (open, confirmed, suppressed, fixed);
  - *sources*: the downloaded code of an audit;
  - *model server* or *provider*: where prompts are sent;
  - *F§n*: a reference to the legacy-app audit in `NOTES_improvements.md`.
- **Legacy:** the original Python/Dash app is preserved under `legacy/`. The rewrite exists because of its correctness bugs, which are listed in `NOTES_improvements.md`.

## Brand Commitments

- The name is **AISAI**. The toolbar shows the wordmark in lowercase.
- The mark is the letter A drawn as a syntax tree: a root node, two branches and one flagged leaf (the finding). Its sources are in `electron/resources/brand/` and `electron/src/lib/logo.ts`.

## Evidence on Hand

- Benchmark data and notebooks are in `bench/` (`bench/baseline_metrics.json`, `bench/data1551redacted.csv`), with the method in `docs/BENCHMARKING.md` and `docs/BENCHMARKING-EXTERNAL.md`.
- Golden test fixtures are in `internal/audit/testdata/golden/`, and the end-to-end fixture package is in `electron/e2e/fixtures/`.
- There are no customers, testimonials, press or published accuracy claims. Future work must not invent them.

## Product Principles

1. **Show the evidence.** Every claim the product makes points to a file and a line the user can open.
2. **The user decides.** Fixes, deletions and provider changes are proposed, shown and confirmed, never applied silently.
3. **Local by default.** Anything that sends code off the machine is an explicit, labelled choice.
4. **Built for long reading.** Optimize for hours of reading code over first-glance impact.
5. **One truth.** The app, the agent tools and the benchmark report the same findings in the same terms.

## Accessibility & Inclusion

- Every text pair meets WCAG 2.1 AA contrast. The measured pairs are recorded in `electron/src/styles/tokens.css`; the legacy app shipped unreadable white-on-white tabs.
- Every control is keyboard reachable: the trees use roving focus and arrow keys, and the pane resizers move with the arrow keys.
- All motion respects reduced-motion settings.
