# W3 — Code / AST / Diff UI

**Goal:** replace the legacy AST modal (an illegible blob with no source view) with a real file
viewer: code, syntax tree, and proposed diffs, beside a triage rail.

**Status:** 🔄 in progress — app shipped and verified; 9 boxes open (see Notes).
**Depends on:** W1, W2
**Blocks:** W6

## Tasks

### Renderer foundation
- [x] `electron/renderer/` — Vite + TypeScript, strict mode
- [x] `electron/src/state/` — zustand store; one store per concern (audit, graph, findings, chat)
- [x] `electron/src/api/client.ts` — typed client over the socket
- [x] Design tokens: colors, spacing, type scale — **all contrast-checked** (F§2.2)
- [x] No inline style strings that a CSS-in-JS layer could silently drop (F§2.4)

### Layout
- [x] Single flex root; **no absolute positioning** (F§2.3)
- [x] Toolbar: package selector, Scan, Scan LLM, provider status chip, settings
- [x] Three-column shell: dependency graph | main pane | findings rail
- [x] Collapsible chat panel docked at the bottom
- [x] Resizable panes (drag handles), persisted per user
- [x] Works from 1280×720 up to 4K; usable at 1024 wide
- [ ] Keyboard shortcuts: `⌘K` command palette, `⌘F` filter findings, `g f` focus file tree

### Dependency graph (vis-network)
- [x] Reuse the vendored `web/vis-9.1.2/vis-network.min.js` (MIT/Apache-2.0)
- [x] Reuse `web/bindings/utils.js` for neighbourhood highlighting
- [x] Node = PURL; label = `name\nvX.Y.Z`
- [x] Color by max severity; distinct shape for direct dependencies
- [ ] Click node → load that package's files + findings
- [x] Search box, filter by severity
- [x] **Path finder**: "why is X in my tree?" — highlight the dependency path from root to X
- [x] Cycle detection surfaced as a badge
- [x] Export PNG / SVG (replaces the legacy cytoscape export buttons)

### File tree
- [x] `GET /api/audits/{id}/files` → tree
- [x] Directories containing findings are marked (the legacy app did this — keep it)
- [x] Collapsible, remembered state
- [x] Per-file finding count badge
- [x] Click → open in the code pane

### Code pane
- [ ] Line-numbered source, syntax highlighted (CodeMirror 6 or Monaco — evaluate size)
- [x] Findings highlighted inline, severity-colored gutter marker
- [x] Click a finding → scroll to and flash the line
- [ ] **F§2.1** — source is visible beside the AST, not replaced by it
- [x] Large files: virtualized rendering, no editor freeze

### AST pane
- [x] AST nodes rendered as a collapsible tree, not a force graph blob
- [x] **F§2.1** — node types visually distinct (the legacy code set `classes` but never styled them)
- [x] Click a node → jump to the corresponding source line
- [x] Depth limit control; "expand all" / "collapse all"
- [x] Findings on a node highlighted
- [ ] Option to keep the force-graph view for the export workflow

### Findings rail
- [x] Sortable by severity, confidence, CWE, file, tool
- [ ] Filter chips: severity, CWE, tool, package, state
- [x] Search box
- [x] **F§2.7** — triage actions: confirm, suppress (with expiry), mark fixed
- [x] Suppression persists and shows an expiry date
- [x] Suppressed findings hidden by default, toggleable

### Diff view
- [x] Side-by-side and unified modes
- [x] Red = removed (vulnerable), green = added (fix)
- [x] Per-hunk header showing finding id, CWE, severity, confidence
- [x] Apply / Reject / Copy buttons (Apply wired up in W6)
- [x] Export as `.patch`
- [x] **F§6** — diff view is the centerpiece of the proposed-changes workflow

### States and feedback
- [x] **F§2.5** — meaningful empty states everywhere (no "Select a package" dead ends)
- [x] **F§2.6** — streaming progress: phase, percent, current file, ETA, cancel
- [x] Buttons disable and show a spinner while their job runs
- [x] Errors are readable, not raw stack traces

## Acceptance criteria

- [ ] Clicking a package node loads its file tree and findings
- [x] Clicking a finding opens the file at the right line with the line highlighted
- [ ] The AST tree is readable at 100% zoom without horizontal scrolling
- [x] A proposed fix renders as a valid unified diff with correct colors
- [x] No text fails WCAG AA contrast in the dark theme
- [x] The layout does not break at 1024px width
- [ ] The app is usable with keyboard only for the main flows

## Notes

- The legacy Dash app had **16 React style properties silently dropped** because they used
  hyphenated CSS names (`margin-right`, `align-items`, …). React ignores those. The rewrite uses
  a typed style system where such a mistake is a compile error.
- The legacy Files tab was white-on-white and effectively invisible. Design tokens must be
  contrast-checked, and the check should be automated (a lint rule or a snapshot test).
- The legacy AST modal was ~60% dead space with the graph bottom-anchored and unreadable. A
  tree view is better than a force graph for ASTs — force graphs are for *relationships*, and
  the AST already is a tree.

### Verified in this pass (2026-11-21)

**Ticked on the strength of:** `electron/main.ts`, `electron/preload.ts`, `electron/index.html`,
`electron/src/views/*.ts` (graph, fileTree, codeView, astView, findingsRail, diffView,
chatPanel, toolbar, layout, errorScreen, settingsDialog), `electron/src/state/*.ts`,
`electron/src/lib/{diff,dom,severity,vis}.ts`, `electron/src/styles/{tokens,app}.css`,
`electron/src/vendor/vis-network.min.js`.

`npm run typecheck`, `npm run lint`, `npm test` (22 tests in 2 files) and `npm run build` all
pass in `electron/`.

**Left unticked (9):**
- *Click node → load that package's files + findings* — clicking a graph node calls
  `useGraphStore.getState().select(id)` and highlights the neighbourhood
  (`electron/src/views/graphView.ts`), but nothing subscribes to that selection to fetch a
  package's files or findings.
- *Line-numbered source, syntax highlighted* — the code pane renders plain text lines with a
  line-number gutter; there is no highlighter and neither CodeMirror nor Monaco is a dependency.
- *F§2.1 — source is visible beside the AST* — the AST is a **tab** next to Code and Diff
  (`TABS` in `electron/src/views/layout.ts`), so the AST still replaces the source rather than
  sitting beside it.
- *Option to keep the force-graph view for the export workflow* — no such toggle.
- *Filter chips: severity, CWE, tool, package, state* — severity, CWE, tool and state have
  controls; the `package` filter exists in the store (`state.packages`) but has no UI to set it.
- *Keyboard shortcuts: `⌘K` command palette, `⌘F` filter findings, `g f` focus file tree* — no
  global key handling exists; the only keyboard support is Enter/Space inside the tree, the AST
  tree, the findings list and the chat input.
- *Clicking a package node loads its file tree and findings* — the acceptance criterion has the
  same gap as the task box above.
- *The AST tree is readable at 100% zoom without horizontal scrolling* — not verifiable without
  running the UI; nothing in the code or CSS asserts it.
- *The app is usable with keyboard only for the main flows* — same reason: without the global
  shortcuts there is no keyboard route to the graph, the diff view or the filters.

**Verified statically rather than visually:** the "no absolute positioning" box rests on
`#app` being the single flex root (`electron/src/styles/app.css`); the only `position: absolute`
in the whole app is the spinner decoration inside a busy button. The contrast box rests on the
measured WCAG ratios recorded in `electron/src/styles/tokens.css` — every foreground/background
pair used by the UI is listed with its ratio, and the only colours used outside the token file
are the same severity palette (`electron/src/lib/severity.ts`). **The contrast check is manual;
there is no automated contrast test**, which the Notes in this file already asked for. The
responsive claims rest on `@media (max-width: 1180px)` and `@media (max-width: 1024px)`
breakpoints plus `minWidth: 1024` in `electron/main.ts`; they were not verified visually.
