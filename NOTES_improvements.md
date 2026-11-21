# AISAI — Improvement Notes

Research + code review only. **Nothing in this document has been implemented.**
Date: 2026-11-21 · Commit reviewed: `4fcd795` "Added per-type vulnerability performance metrics"

Method: read all of `interactiveGraph.py`, `llmScan.py`, `format.py`, `dependencyTree.py`,
`recursiveTree.py`, `astvisualizer.py`, `packageGetter.py`, `setupPackage.py`, both
`requirements.txt`/`Dockerfile`, and the four benchmark notebooks; ran the app locally
(`--local-file` mode, Dash 2.18.2) and drove the real UI in a browser to verify UI findings;
cross-checked competitors and LLM-detection research.

---

## 0. What AISAI is today (grounding)

| Layer | Current implementation |
|---|---|
| Entry point | `interactiveGraph.py` (1,780 lines) — Dash app, port 8080, Flask backend |
| Dependency resolution | `pipdeptree --json-tree` against the **live installed env** |
| Source acquisition | PyPI JSON API → download sdist → `tar.extractall` → rename dir |
| Dependency graph | `dash-cytoscape`, `dagre` layout, node = package name, label = `name\nvVERSION` |
| SAST | `bandit -r -q -f json` with `-lll` (HIGH severity only) |
| LLM | `langchain-ollama` → local Ollama, default `gemma3n:e4b`, `temperature=0` |
| LLM output | JSON schema `{vulnerable, vulnerabilities[]}` via `with_structured_output` |
| AST view | `ast.parse` → cytoscape tree, vulnerable nodes styled red by `line_number` |
| Benchmark | `data1551redacted.csv` (1,551 rows, CVE/CWE/CVSS/cyclomatic metadata), `train/{vulnerable,patched}` (1,673 / 1,379 .py files) |

**The one-line diagnosis:** AISAI does *code-level* vulnerability analysis of a PyPI package and
*visualizes* it, but it does **not** answer the supply-chain question it is named for. There is no
CVE/advisory data anywhere in the pipeline — nothing looks up whether a dependency has a known
vulnerability. The dependency graph is descriptive (topology) but not advisory (risk).

---

## 1. Correctness bugs found (fix these first — several silently lose findings)

These were verified by reading the code, not guessed.

### 1.1 `llmScan.py` wipes the entire results directory on every single scan 🔴
`interactiveGraph.py:1518` runs `subprocess.run([sys.executable, 'llmScan.py', current_ast_file])`.
`llmScan.py:420` executes `clear_results_directory()` at `__main__` before `main()`.
→ **Every LLM scan deletes every prior scan's results.** Scanning file B destroys file A's output.
`interactiveGraph.py:1514` even deletes the target file first "to ensure fresh scans", then the
subprocess nukes the whole directory anyway.

### 1.2 Result filename collisions on basename 🔴
`get_analysis_filename()` uses `os.path.basename` → `__init__.py` → `__init___analysis.json`.
Every package in a dependency tree has an `__init__.py`. Results for different packages/files
silently overwrite each other in the shared `results/` dir.

### 1.3 `vulnerable_files` mixes file paths and package names 🔴
- `run_bandit_analysis` (`:887`) populates `vulnerable_files` with **absolute file paths**
  (`issue['filename']`).
- `initialize_data()` (`:438`) passes that same set as `vulnerable_packages` to `def_elems`, which
  compares it against **package names** (`node_id`).

→ The initial dependency graph never colors a package red from Bandit. The post-analysis callback
recomputes `security_status` separately so it looks like it works, but the file-tree red
highlighting (`:723`, `:747`) and the initial graph coloring are a type confusion.
It also means `vulnerable_files` (file paths) and `package_bandit_results` (package names) need to
be two separate namespaces.

### 1.4 Bandit severity filter in the app ≠ Bandit config in the benchmark 🟠
- App: `bandit ... -lll` (`:864`) **plus** `issue_severity == 'high'` (`:931`) → only HIGH.
- Benchmark: `_bandit_valid_issues` accepts **MEDIUM or HIGH** severity **and** confidence.

→ **The product ships a configuration the benchmark never measures.** Bandit MEDIUM findings
(some of the most common real issues — `try/except/pass`, `assert` in prod code) are invisible in
the app. The benchmark numbers do not describe the shipped tool.

### 1.5 Only one package per container run 🟠
`interactiveGraph.py:514`: `if not args.skip_download and not os.path.exists('/packages'):`
→ Once `/packages` exists, a *different* target package is never downloaded. Its file tree is empty
and the user sees a dependency graph with no sources. Needs per-package existence checks.

### 1.6 `tar.extractall()` without a filter 🟠
`interactiveGraph.py:331`, `setupPackage.py:229`, `packageGetter.py:208`, `dependencyTree.py:76`.
Python 3.12+ warns; 3.14 defaults to `filter='data'`. The container is Python 3.13 (`Dockerfile`).
→ Tar-slip path traversal on attacker-controlled sdists. **In a security tool this should be
`filter='data'` everywhere.** Also `tarfile.open(...).extractall()` on a PyPI sdist is untrusted
input by definition.

### 1.7 `app.run(host='0.0.0.0', debug=True)` 🔴
`interactiveGraph.py:1778`. Werkzeug's interactive debugger on `0.0.0.0` is a **remote code
execution** console. The container publishes port 8080 to the host. Also enables the reloader
(double process, and `initialize()` runs at import time *and* in `main()`).

### 1.8 `pip install` of the target package runs its `setup.py` 🟠
`install_package()` (`:157`) → `pip install <pkg>` → arbitrary code execution at install time for
whatever package is being audited. Should be `--no-deps` into a throwaway venv, or better, resolve
deps with `pipdeptree` against a separate resolution rather than installing into the app env.

### 1.9 `debug=True` + no `suppress_callback_exceptions` 🟠
Dynamic pattern-matching ids (`{'type':'python-file','path':ALL}`) are added by callbacks at
runtime. Dash normally requires `suppress_callback_exceptions=True` for this.

### 1.10 Graph node identity is the package name only 🟠
`def_elems` uses `pkg_data["package_name"]` as the node id and dedupes on it
(`seen_nodes`). Two versions of the same package (dependency conflict) **collapse into one node**
and the conflict is invisible. `pkg_data['installed_version']` is also accessed with direct
indexing → `KeyError` on a malformed tree.

---

## 2. UI/UX findings (verified by running the app)

I ran AISAI in `--local-file` mode on Dash 2.18.2 and drove it in a browser. These are observed
behaviors, not code-reading guesses.

### 2.1 The AST view is the centerpiece and it is currently unusable
Screenshotted the AST modal for a small file:
- The graph renders as a **4px-tall illegible blob** at the bottom of the modal. Node labels
  (`FunctionDef (L1)`, `argname`, `Call`) are unreadable. There is no zoom control, no minimap,
  no focus-on-finding, no way to read the tree.
- **~60% of the modal is empty dead space** above the graph. The graph is bottom-anchored
  (`align: 'UL'` + dagre), the modal body is `80vh`.
- **There is no source code view at all.** The user is asked to judge a vulnerability from a
  structure diagram. There is no code, no line context, no highlighted source line — only a red
  node in a tree.
- The `classes` field is set to the AST node type (`ast_to_cytoscape_elements`, `:818`) but **the
  stylesheet has no class selectors** — only `node` and `edge`. Every AST node is the same teal.
  The styling hook exists and is unused.

→ **This is where the diff view belongs.** The AST modal should become a three-pane file viewer:
code (left) / AST or findings (right) / proposed patch (bottom or diff tab).

### 2.2 The "Files" tab is white-on-white — effectively invisible
The tab bar sets `background: COLORS['gray_light']` but `dcc.Tab` renders a **white** tab body, and
the unselected tab label uses `color: COLORS['text']` (`#ffffff`). Measured in the browser: the
"Files" label renders as near-white on white. It also wraps to a vertical stack because the sidebar
is only 22% wide. **Contrast failure, WCAG AA fail.**

### 2.3 The layout is broken at the widths real users will use
- Header is `width: 80%` but the sidebar is `position: absolute; right: 0; width: 22%` — the
  header background stops mid-page and the sidebar overlaps it.
- Sidebar `height: 92.3vh` vs graph `height: 80vh` — no shared vertical rhythm.
- "Run Bandit Security Analysis" is an **orphan button** rendered *after* the whole layout at
  bottom-left, visually detached from the graph it acts on.
- The right panel's "Package Details" `H3` renders visually larger than the `H1` app title.
- Node labels overflow their node badly: a 100×50px node renders `aisai_sample.py` at ~4× the node
  width. `text-wrap: wrap` does not break long unbroken tokens.

### 2.4 16 React style properties are silently dropped
React rejects hyphenated CSS property names in inline styles. Verified count in `style={...}`
blocks: `margin-right` ×8, `align-items` ×2, `vertical-align` ×2, `margin-top`, `text-align`,
`overflow-y`, `margin-bottom`. The browser console confirms:
`Warning: Unsupported style property margin-top. Did you mean marginTop?`
→ Those styles were never applied. Every one is in a layout-critical spot (header, buttons, sidebar).

### 2.5 The Files tab is a dead end until you select a node
`update_panel_content` returns "Select a package" when `tapNodeData` is empty — *regardless of
which tab is active*. So clicking "Files" first shows a dead message. No hint that a node must be
selected, no empty-state guidance.

### 2.6 No progress feedback on long operations
"Run LLM Security Analysis" spawns a blocking subprocess (model init + health check + retries with
5-second sleeps) and the button never shows a spinner or disables itself. Same for "Run Bandit
Security Analysis" across an entire dependency tree. `output-div` is a 4px-tall strip at the top of
the header — effectively invisible.

### 2.7 No triage model at all
Findings only exist as red nodes and raw `html.Pre` blocks below the graph. No severity, no
confidence, no CWE, no sorting, no filtering, no dedup, no "acknowledge/ignore", no way to say
"this is a false positive".

---

## 3. LLM prompting — what to change

### 3.1 The prompt has no real system message
`llmScan.py:61` builds the whole prompt as **one user-role string** containing literal
`<system>...</system>` and `<user>...</user>` tags, fed through `ChatPromptTemplate.from_template`.
Those are not protocol roles — they're just text inside a user message. Instruction hierarchy is
flattened, and the tags are noise. Use `ChatPromptTemplate.from_messages([("system", ...),
("user", ...)])`.

### 3.2 The schema has no severity, no confidence, no CWE — so findings can't be triaged
Current schema: `{vulnerable, vulnerabilities[{line_number, node_type, code_snippet,
vulnerability_type, description, remediation}]}`.
→ `vulnerability_type` is a free-text string ("hardcoded secret" vs "CWE-798" vs "weak hash").
There is no severity, no confidence, no CWE id, no reachability, no evidence. **This is why the UI
has no triage model.** Fix the schema and the UI capabilities follow.

### 3.3 `line_number` is computed by substring matching, not trusted
`append_line_numbers` (`llmScan.py:31`) discards the model's line numbers and re-finds them by
`if code_snippet in line` — **single-line matching only**. A multi-line snippet never matches, so
the model's (correct) line number survives; a snippet appearing twice picks the first occurrence.
The regex `r'\s*#\s*line\s+\d+\s*$'` strips a `# line N` marker that never exists.
→ The model's line numbers should be kept and validated with a distance check, not overwritten by
exact substring match.

### 3.4 No `num_ctx` → silent context truncation
Nothing sets `num_ctx`/`num_predict` (`llmScan.py:199`, `:249`). Ollama's default context is
**2,048 tokens**. A ~150-line file plus prompt exceeds it → the tail of the file is silently
dropped → **false negatives with no error**. Either set `num_ctx` explicitly (8192+) or, better,
chunk by function and scan each chunk with its enclosing scope.

### 3.5 Whole-file scanning, no function scoping
`llmScan.py` passes the entire file. Research consistently shows **function-level** analysis with
context is the sweet spot (CORRECT, arXiv:2504.13474: DeepSeek-R1 → 67% accuracy, precision ~0.8).
`recursiveTree.py` already builds an import graph — the building blocks are in the repo.

### 3.6 The prompt asks a binary question; research says that's the worst framing
"TO ERR IS MACHINE" (arXiv:2403.17218): 14 models × 7 prompting methods → **all 50–55% balanced
accuracy** on vulnerable-vs-fixed pairs. SecLLMHolmes: LLMs flag *patched* code as vulnerable and
are non-deterministic. "Can Open LLMs Catch Vulnerabilities?" (ICPEC 2025): >50% FP rate on safe
snippets in every model.
→ Replace "is this vulnerable?" with **structured evidence + per-finding confidence + explicit
abstention**. "Think & Verify" (arXiv:2503.17885) cut ambiguous responses 20.3% → 9.1%.

### 3.7 No grounding, no few-shot, no self-verification
- No CWE/CVE knowledge injected. VulReaD (arXiv:2602.10787) reports **CWE macro-F1 +30%** from
  CWE-taxonomy knowledge-graph grounding.
- Zero examples in the prompt. A 2–3 shot set of *vulnerable + patched* pairs drawn from
  `train/` would directly target the precision problem — and the repo already has 1,214 paired
  vulnerable/patched files sitting on disk.
- No second pass. The strongest pattern in the literature (DeepAudit, VulnHunterX, ReasonVul) is
  **generate → try to disprove → drop what you can't reproduce**.

### 3.8 The model choice is a problem
Default `gemma3n:e4b` — a ~4B model. CASTLE (arXiv:2503.09433) scores: Falcon-3-7B 521,
Mistral-7B 446, **Gemma-2-9B 436**, Llama-3.1-8B 417 vs GPT-4o 954. Reasoning models produce
markedly fewer false positives. RealVuln's headline (LLM F2 53.8 vs Snyk 18.2) uses
Claude Sonnet 4.6 — **not reachable from a local Ollama default**.
→ At minimum: make the model configurable (it partly is, via `OLLAMA_PRIMARY_MODEL`), document
expected accuracy per model size, and consider a **BYOK cloud path alongside Ollama** so the
offline story is preserved but frontier models are reachable (SonarQube Server supports Azure
OpenAI self-hosted; Semgrep supports BYOK).

### 3.9 Prompt strategy summary

| Change | Rationale |
|---|---|
| Real system + user roles | Instruction hierarchy |
| Output: `{findings[{cwe_id, severity, confidence, line_start, line_end, evidence{source,sink,flow}, remediation_patch}], verdict}` | Enables triage, dedup, and a real diff |
| Add `abstain: true` | FP suppression |
| Two-stage: detect → verify/disprove | Drops unverifiable findings |
| 2–3 few-shot pairs from `train/` | Precision |
| CWE taxonomy in the prompt | +30% CWE macro-F1 |
| Function-scoped chunks + enclosing context | Avoids context truncation, better signal |
| `num_ctx=8192+` explicit | Kills silent truncation |
| Ask for a **patch**, not prose remediation | Enables the diff view |
| Separate "is vulnerable" from "is patched" questions | They're different difficulty (arXiv:2503.17885) |

---

## 4. Strategies to identify vulnerabilities more effectively & efficiently

### 4.1 Close the advisory gap (biggest win) — SCA
The dependency graph has **zero vulnerability data**. Additions, in order of value/effort:

| Add | Why | Effort |
|---|---|---|
| **OSV-Scanner `--offline`** or OSV API + local `PyPI/all.zip` | 26,428 PyPI vulns; offline mode sends nothing anywhere — **preserves the air-gap story exactly** | Low |
| **pip-audit** | Python-native, one command, emits CycloneDX/SARIF | Low |
| **deps.dev API** (Google) | Free, unauthenticated, **resolved with packaging-tool-accurate algorithms**, includes licenses + OSV + Scorecard. Can *replace or validate* AISAI's own tree resolution | Low |
| **PyDeps.com / ecosyste.ms** | PyPI metadata + CVE + dependency resolver; ecosyste.ms has a dedicated dependency resolver | Low |

**PURL as the canonical node ID** — it's the join key across OSV, CycloneDX, SPDX, deps.dev, GUAC.
Without it AISAI graphs aren't comparable to anything else.

### 4.2 Broaden SAST beyond Bandit
- **Semgrep CE** (`p/python`, `p/secrets`, `p/flask`, `p/django`) → cross-file taint analysis, YAML
  rules, SARIF, **and an MCP server**. Keep Bandit as a second opinion — the two complement each
  other (Semgrep's own 2021 analysis notes Bandit is often *more* precise on the rules it has).
- **Secret scanning** (gitleaks / trufflehog) — "hardcoded secrets" is in the current prompt's
  vocabulary but Bandit has no real secret detection.
- **Fix the severity filter** (§1.4) so MEDIUM findings actually appear.

### 4.3 Reachability / blast radius — the strongest differentiator
AISAI already has the dependency tree. Socket cuts CVE noise ~90% via reachability; Endor Labs
labels every dependency **Reachable / Unreachable** via call graph. `recursiveTree.py` already
builds an import graph. → **"Is this vulnerable dependency actually reachable from my code?"** is
an axis nobody in the "local Python package auditor" space occupies, and AISAI has the data.

### 4.4 Efficiency (the current pipeline is slow by construction)
- **One subprocess + one full model init per file.** `llmScan.py` is spawned per file; each spawn
  re-runs the health check, the `ping` invoke, and up to 3 retries with 5s sleeps
  (`llmScan.py:196–212`). For a 300-file package that is 300 model initializations.
  → Run the scan **in-process** (or a small worker pool), keep the model warm.
- **Bandit runs once per package directory**, serially, in a Python loop (`:924`). → Batch all
  packages into one `bandit -r` invocation, or run packages in parallel.
- **Results are never cached.** Cache LLM results keyed by `sha256(file) + model + prompt_version`
  so re-scanning a changed dependency tree is near-free.
- **Chunk and scan only what matters.** Skip test files, `venv/`, `docs/`, and generated files;
  prioritize by cyclomatic complexity — the dataset already carries `file_cyclomatic_complexity`.
- **Deduplicate** findings across Bandit + Semgrep + LLM by `(file, line, CWE)` before display.

### 4.5 Make the benchmark publishable
- The benchmark compares **Bandit vs LLM only**. There is **no trivial baseline** (e.g. a regex
  baseline, or TF-IDF + a shallow classifier). Without one, "the LLM is better" is unfalsifiable.
  Note: on CVE→CWE classification, TF-IDF + shallow classifier scored **74%** vs LLM prompting
  **59%** (ACM 10.1145/3733800.3763267).
- Adopt **RealVuln** (arXiv:2604.13764 / realvuln.com): 1,443 Python vulns, 280 curated FP traps,
  73 CWEs, automated scoring, and 16 scanners' baseline outputs already published. Scoring =
  file path + CWE from an acceptable-list + line within ±10, with **recall-weighted F2/F3**.
- Add **OWASP BenchmarkPython** (1,230 cases, per-case TP/FP labels, GPL-3.0) as a neutral,
  reproducible third-party harness.
- **Leakage control**: `train/` is drawn from CVE fix commits; if the model's training cutoff
  postdates those CVEs, results are contaminated. Report on a post-cutoff split.
- **RealVuln's matching semantics** (±10 lines, CWE-set) are much better than AISAI's current
  exact-ish `code_snippet` matching, which is why a snippet-match metric can look great while
  measuring nothing.

---

## 5. MCP support

### 5.1 Expose AISAI as an MCP server (highest-leverage gap)
No competitor in the "local Python auditor" space exposes MCP, but **Semgrep does** — findings,
triage, fixes, scans, rules, policies, reports — and that is the pattern to copy. AISAI's
local-Ollama + container + dependency graph is a natural MCP server: the LLM driving it doesn't
need a cloud account.

Proposed tool surface:

```
scan_package(package, version?)        → resolved deps + advisory summary
get_dependency_graph(package)           → nodes/edges with PURLs + advisory state
list_files(package, path?)              → file tree with finding counts
get_source(package, path)               → file content (line-numbered)
get_ast(package, path)                  → AST elements for the AST view
run_bandit(package)                     → findings (SARIF-shaped)
run_llm_scan(package, path)             → LLM findings
list_findings(package, filters)         → triage list: severity, confidence, CWE, file, line
get_finding(id)                         → evidence + remediation
propose_fix(finding_id)                 → unified diff for the finding
apply_patch(package, path, patch)       → apply a proposed fix
export_report(package, format)          → sarif | cyclonedx | json
```

Transport: **stdio** (fits the container model) with an optional HTTP/SSE mode for the browser UI.

Design notes:
- Keep the MCP server **read-mostly**; `apply_patch` writes into the container's `/packages`
  volume only, never the host.
- Reuse the existing `results/` JSON as the finding store — no new state model needed.
- Findings should be **SARIF-shaped from day one** so they can be consumed by any SARIF-capable
  agent (GitHub code scanning, GitLab, DefectDojo).

### 5.2 Also consume MCP servers
The reverse direction is worth supporting: let AISAI pull findings from MCP servers (Semgrep,
OSV, Trivy) so the UI shows one merged, deduplicated view. MCP client support for stdio servers
is a contained amount of work and makes AISAI the aggregator.

### 5.3 MCP-specific risks
MCP servers execute tools; running one against an untrusted package's source is an attack surface.
Mitigations: the container is already the isolation boundary — keep it; sandbox `apply_patch`;
log every tool call; cap tool output size (an MCP server returning a whole file is a context bomb).

---

## 6. Git-diff-style file viewing with proposed changes

This is the feature that ties the LLM output, the AST view, and the UI together — and it is the
pattern every serious competitor has (Semgrep Autofix → draft PR; Copilot Autofix → inline
suggestion + "Create PR with fix"; SonarQube Remediation Agent → verified PR).

### 6.1 Current state
The LLM returns `remediation` as **prose** ("Use `hmac.compare_digest` instead"). The UI renders
it in a `<p>`. There is **no code fix, no diff, no way to review or apply a change.** The
README's "Package Testing Workflow" (modify source in the container, re-run with `--skip-download`)
is a manual equivalent of this, but with no linkage between the finding and the edit.

### 6.2 Proposed design

**a) LLM returns a patch, not prose.**
Extend the schema:
```json
"remediation": {
  "summary": "string",
  "patch_unified": "string",          // valid unified diff
  "confidence": "low|medium|high"
}
```
Validate the diff server-side (`unidiff` or `git apply --check`); if invalid, fall back to prose.

**b) New UI surface: a file viewer with a diff tab.**
Replacing/augmenting the AST modal:

```
┌──────────────────────────────────────────────────────────────┐
│ package/file.py          [ Code ] [ Diff ] [ AST ] [ Findings ]│
├───────────────────────────────┬──────────────────────────────┤
│  12  def verify(token):       │  ← Findings rail (severity,   │
│  13      if token == secret:  │    confidence, CWE, file:line)│
│  14          return True      │                              │
│  ▓▓▓▓ 15  -  if token == secret:  +  hmac.compare_digest(...) │  ← diff view
└───────────────────────────────┴──────────────────────────────┘
```
- **Code pane**: line-numbered source, vulnerable lines highlighted, click a finding → scroll +
  flash the line.
- **Diff pane**: side-by-side or unified, red = removed (vulnerable), green = added (fix).
  Each hunk carries the finding ID, CWE, severity, and confidence.
- **Apply / Reject / Copy**: apply the patch into the container's package dir, then **re-run the
  scan and show the before/after result** — this closes the loop and is the app's strongest
  existing capability (§7.2, differential analysis).
- **Export**: `.patch` file, GitHub-ready PR description, or paste into the AST view.

**c) Reuse what's already here.**
- The dataset already pairs `vulnerable_code` → `fixed_code` per CVE (`data1551redacted.csv`).
  That is **1,551 ready-made (vulnerable, patched) diff pairs** — a goldmine for few-shot
  prompting *and* for validating patch quality.
- `train/vulnerable` + `train/patched` are paired by id (1,214 shared ids) — same idea, more data.
- `format.py` (comment stripping) and `setupPackage.py` (package modification workflow) are the
  existing pieces of the "modify and rescan" loop.

**d) Where the diff surfaces in the graph.**
The dependency graph should also support "show the fix path": clicking a vulnerable package →
the chain of packages/versions involved → the file → the diff. Today there is no way to see
*which* dependency introduced a pattern.

---

## 7. Competitors — what to adopt vs. differentiate

### 7.1 Would make AISAI redundant today

| Competitor | Why it's a threat | Source |
|---|---|---|
| **DeepAudit** (`lintsinghua/DeepAudit`, AGPL-3.0, ~6.6k★) | **Closest competitor by far.** Multi-agent (Orchestrator→Recon→Analysis→Verification→Report), **Ollama-local is a headline feature** (same air-gap positioning), RAG over CWE/CVE, Tree-sitter chunking, **sandboxed PoC verification that drops unreproducible findings**, wraps Semgrep + Bandit + Gitleaks + TruffleHog + npm audit + safety, FastAPI + React + Postgres + Docker. → **AISAI's scanning half is a strict subset.** | github.com/lintsinghua/DeepAudit |
| **pip-audit / osv-scanner / Trivy** | One command, zero setup, vastly better maintained — and they answer the question AISAI doesn't. | pypi.org/project/pip-audit, google.github.io/osv-scanner |
| **PyDeps.com / deps.dev** | Dependency exploration for 880k PyPI packages **with CVE annotation**, free, no container. | pydeps.com, deps.dev |
| **Semgrep CE** | Replaces Bandit with cross-file taint, SARIF, CI, **and MCP**. | semgrep.dev |
| **RealVuln** | Already publishes what AISAI's benchmark is trying to measure, with a scoring harness. | realvuln.com, arXiv:2604.13764 |

### 7.2 What AISAI can own that nobody does well together
1. **Local-first, air-gapped, one-container PyPI package forensics.** DeepAudit is a
   Postgres/Redis/multi-container platform with 11 cloud providers and **no dependency graph**;
   AISAI is one Podman container + a browser.
2. **AST as the LLM's grounding mechanism.** DeepAudit uses Tree-sitter chunking; AISAI already
   renders the AST and highlights vulnerable lines *in it*. Feeding the AST + the exact sink line
   is a more structured prompt than "here's the file." Already half-built.
3. **Reachability over the dependency graph** — the one axis where AISAI's data advantage is real.
4. **Benchmark-first credibility** — publish against RealVuln + OWASP BenchmarkPython with
   reproducible scripts and become *the* reference implementation for local LLM auditing of Python.
5. **Package forensics: download → mutate → rescan.** The README already documents modifying a
   package's source in the container and re-running with `--skip-download`. No competitor documents
   this; with §6 (diff + apply + rescan) it becomes a genuine product loop.

### 7.3 Adoption backlog (value/effort)

| # | Adopt | Effort | Payoff |
|---|---|---|---|
| 1 | SARIF output for Bandit + LLM findings | Low | Unlocks GitHub code scanning, GitLab, DefectDojo. **Cheapest credibility win.** |
| 2 | OSV-Scanner `--offline` / OSV API | Low | Turns the graph from descriptive → **advisory** |
| 3 | PURL as canonical node ID | Low | Makes graphs comparable across tools |
| 4 | deps.dev API | Low | Packaging-accurate resolution + licenses + OSV + Scorecard |
| 5 | Fix Bandit severity filter (§1.4) | Low | Benchmark now measures the shipped product |
| 6 | LLM prompt/schema rework (§3) | Low–Med | Severity/confidence/CWE → triage UI; +accuracy |
| 7 | RealVuln + OWASP BenchmarkPython | Med | Publishable, third-party-reproducible numbers |
| 8 | MCP server (§5.1) | Med | **Biggest 2026 gap**; the agentic-use unlock |
| 9 | Semgrep CE alongside Bandit | Med | Cross-file taint, secrets, SARIF, MCP |
| 10 | Diff view + patch apply + rescan (§6) | Med | Closes the product loop; matches every competitor |
| 11 | CycloneDX/SPDX SBOM export | Med | Feeds GUAC, Dependency-Track, Trivy |
| 12 | Reachability analysis | Med–High | **The differentiator** |
| 13 | BYOK cloud-model path alongside Ollama | Low | Frontier-model accuracy while keeping the offline story |
| 14 | PyPI attestations / Sigstore view | Med | Free per-package trust signal |

---

## 8. Prioritized roadmap (suggested)

**Phase 0 — correctness (small PRs, high value)**
Fix §1.1, §1.2, §1.3, §1.4, §1.7, §1.6. These are silent-data-loss and RCE-class issues.

**Phase 1 — UI/UX**
Fix the 16 dropped React styles; fix the Files-tab contrast; repair the layout (single flex
container instead of absolute positioning); move the Bandit button into the graph toolbar; add
loading states; replace the AST modal with a Code/Diff/AST/Findings pane; style AST node classes.

**Phase 2 — findings model + diff**
New schema with CWE/severity/confidence/patch; findings list with triage; unified-diff view;
apply/reject; rescan-and-compare.

**Phase 3 — advisory layer**
OSV-Scanner offline, deps.dev, PURL ids, SARIF export, CycloneDX export.

**Phase 4 — MCP**
MCP server over the tools in §5.1; then MCP client aggregation.

**Phase 5 — research credibility**
RealVuln + OWASP BenchmarkPython; trivial baseline; recall-weighted metrics; leakage-controlled
splits; publish results.

---

## 9. Open questions for the owner

1. **Is the "supply chain" framing the intent?** The name promises CVE/advisory analysis; the
   implementation is code-level SAST on a downloaded package. If advisory data is in scope, Phase 3
   is not optional — it *is* the product.
2. **Is the local-Ollama-only constraint firm?** RealVuln's headline number uses Claude Sonnet 4.6,
   which a local 4B model cannot reach (CASTLE: Gemma-2-9B 436 vs GPT-4o 954). A BYOK path
   preserves the offline story while making the accuracy claim defensible.
3. **Is the target audience researchers (benchmark rigor) or practitioners (find a bug in a dep
   you're about to install)?** The two imply different priorities: the former wants the benchmark
   harness and reproducibility; the latter wants fast triage, diffs, and CI integration.
4. **Should AISAI stay a single-target local tool, or become a persistent service?** The current
   module-level globals (`elements`, `vulnerable_files`, `package`, `current_ast_file`) mean
   multi-user is impossible — one user switching packages corrupts another's state.
