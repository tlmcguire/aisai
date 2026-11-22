---
name: AISAI
description: A precise, forensic workbench for auditing the code inside software dependencies.
colors:
  oxide-ink: "#0a1112"
  oxide-surface: "#0f1819"
  oxide-raised: "#152123"
  oxide-inset: "#070c0d"
  oxide-hover: "#1b2a2d"
  oxide-selected: "#1d3a38"
  patina-border: "#223336"
  patina-border-strong: "#35494c"
  bone: "#dde8e3"
  lichen: "#8ea5a0"
  moss: "#7a9692"
  signal-lime: "#b4f23c"
  signal-lime-strong: "#c8ff5a"
  signal-lime-soft: "#232f16"
  patina-teal: "#5fd3c4"
  patina-teal-soft: "#17302e"
  success: "#4fd18b"
  warning: "#f0b429"
  danger: "#ff6b5e"
  danger-soft: "#35201f"
  sev-critical: "#ff7a6b"
  sev-high: "#ff9f4a"
  sev-medium: "#f0c93a"
  sev-low: "#5fd3c4"
  sev-info: "#8ea5a0"
  syn-keyword: "#f48fb1"
  syn-function: "#ffbd6e"
  syn-call: "#6fd8ec"
  syn-import: "#9fb2ae"
  syn-assign: "#e9d46f"
  syn-branch: "#ff8a8a"
  syn-class: "#c4a8ff"
  syn-string: "#a6ec78"
  diff-add-bg: "#17332a"
  diff-add-fg: "#8ae9b4"
  diff-del-bg: "#35201f"
  diff-del-fg: "#ffb9b0"
typography:
  display:
    fontFamily: "system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI Variable Text', 'Segoe UI', Ubuntu, Cantarell, sans-serif"
    fontSize: "1.75rem"
    fontWeight: 650
    lineHeight: 1.2
    letterSpacing: "-0.02em"
  headline:
    fontFamily: "system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI Variable Text', 'Segoe UI', Ubuntu, Cantarell, sans-serif"
    fontSize: "1.25rem"
    fontWeight: 650
    lineHeight: 1.3
    letterSpacing: "-0.01em"
  title:
    fontFamily: "system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI Variable Text', 'Segoe UI', Ubuntu, Cantarell, sans-serif"
    fontSize: "0.875rem"
    fontWeight: 650
    lineHeight: 1.4
  body:
    fontFamily: "system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI Variable Text', 'Segoe UI', Ubuntu, Cantarell, sans-serif"
    fontSize: "0.8125rem"
    fontWeight: 400
    lineHeight: 1.5
  label:
    fontFamily: "system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI Variable Text', 'Segoe UI', Ubuntu, Cantarell, sans-serif"
    fontSize: "0.75rem"
    fontWeight: 650
    lineHeight: 1.4
    letterSpacing: "0.01em"
  code:
    fontFamily: "'JetBrains Mono Variable', 'JetBrains Mono', ui-monospace, SFMono-Regular, Menlo, Consolas, monospace"
    fontSize: "0.75rem"
    fontWeight: 400
    lineHeight: 1.6667
rounded:
  sm: "0.25rem"
  md: "0.375rem"
  lg: "0.625rem"
  window: "0.75rem"
  pill: "999px"
spacing:
  "1": "0.25rem"
  "2": "0.5rem"
  "3": "0.75rem"
  "4": "1rem"
  "5": "1.5rem"
components:
  button:
    backgroundColor: "{colors.oxide-raised}"
    textColor: "{colors.bone}"
    rounded: "{rounded.md}"
    padding: "4px 10px"
    height: "1.75rem"
  button-hover:
    backgroundColor: "{colors.oxide-hover}"
  button-primary:
    backgroundColor: "{colors.signal-lime}"
    textColor: "{colors.oxide-ink}"
    rounded: "{rounded.md}"
    padding: "4px 10px"
    height: "1.75rem"
  button-primary-hover:
    backgroundColor: "{colors.signal-lime-strong}"
  button-ghost:
    textColor: "{colors.lichen}"
    rounded: "{rounded.md}"
    padding: "4px 10px"
  input:
    backgroundColor: "{colors.oxide-inset}"
    textColor: "{colors.bone}"
    rounded: "{rounded.md}"
    padding: "4px 8px"
    height: "1.75rem"
  chip:
    backgroundColor: "{colors.oxide-raised}"
    textColor: "{colors.lichen}"
    rounded: "{rounded.sm}"
    padding: "2px 8px"
  chip-pressed:
    backgroundColor: "{colors.signal-lime-soft}"
    textColor: "{colors.bone}"
  badge:
    backgroundColor: "{colors.oxide-raised}"
    textColor: "{colors.lichen}"
    rounded: "{rounded.pill}"
    padding: "1px 7px"
  finding-card:
    backgroundColor: "{colors.oxide-raised}"
    textColor: "{colors.bone}"
    rounded: "{rounded.md}"
    padding: "0.5rem 0.75rem"
  tree-row:
    textColor: "{colors.bone}"
    rounded: "{rounded.md}"
    height: "1.6rem"
  tree-row-selected:
    backgroundColor: "{colors.oxide-selected}"
  tab:
    textColor: "{colors.lichen}"
    padding: "0.5rem 0.75rem"
  tab-selected:
    backgroundColor: "{colors.oxide-ink}"
    textColor: "{colors.bone}"
  segment:
    textColor: "{colors.lichen}"
    rounded: "{rounded.sm}"
    padding: "0.3rem 0.75rem"
  segment-selected:
    backgroundColor: "{colors.oxide-raised}"
    textColor: "{colors.bone}"
---

# Design System: AISAI

## Overview

**Creative North Star: "Verdigris"**

Verdigris is the green patina that forms on copper: quiet, aged, and long-lasting. The interface is that material: a dark, oxidised-green ground with bone-coloured text and a single bright signal. It is built for a security engineer reading unfamiliar code for hours in a dim room, so it is **precise** (exact alignment, tabular numbers, one control height) and **forensic** (evidence first, every claim pinned to a file and a line, serious without drama). Nothing on screen competes with the code.

The app is an operating tool, not a showcase. Controls look and behave like the host operating system: the system font, native window chrome, the arrow cursor, and familiar patterns such as segmented controls, sidebar preferences and indent-guided trees. The brand lives in the details: the oxide undertone in every neutral, the lime that marks what you can act on, and the syntax colours that make the AST read like the code it describes.

Density is high but legible. Panes sit edge to edge, separated by 1px borders rather than gutters, and every pane can be resized. The whole interface scales smoothly with the window and with a user-set interface size, from a laptop to a 4K monitor.

**Key Characteristics:**
- An oxidised green-black ground; no neutral grey anywhere.
- One signal colour (lime) reserved for action and selection.
- The system font for the interface; JetBrains Mono only for code, paths and data.
- Flat tonal layers with 1px borders; shadows only on things that float.
- Restrained, purposeful motion with exponential ease-out, all of it disabled under reduced motion.
- Native window chrome on macOS, Windows and Linux.

## Colors

A cool, oxidised palette: the neutrals carry a green undertone, one lime signal marks action, and a patina teal supports it. The accent is the one colour the user picks, and the ground follows it: the surfaces, the borders and the text are re-tinted to its hue, so the app reads as one colour family rather than a green app with a blue button.

### Primary
- **Signal Lime** (`signal-lime`): the one action colour. It fills primary buttons (Scan, Ask, Use this server) and draws the focus ring, the selected-tab indicator, the active resizer and the flagged leaf of the logo. **Signal Lime Strong** is its hover state, and **Signal Lime Soft** tints pressed chips and filter toggles.

### Secondary
- **Patina Teal** (`patina-teal`): supporting information. It colours folder icons, links, the crossbar of the logo, low-severity findings and download spinners. It never marks a primary action.

### Neutral
- **Oxide Ink** (`oxide-ink`): the base ground, behind the code pane and the graph.
- **Oxide Surface** (`oxide-surface`): the toolbar, the side rails, the chat panel and the Settings window.
- **Oxide Raised** (`oxide-raised`): buttons, cards, badges and finding cards.
- **Oxide Inset** (`oxide-inset`): text fields, code blocks and the code view, recessed below the surface.
- **Oxide Hover / Oxide Selected**: row hover and the selected row, file or Settings section.
- **Patina Border / Patina Border Strong**: 1px pane dividers and control outlines; the strong variant for chips and the edges of floating windows.
- **Bone** (`bone`): primary text, 15.14:1 on Oxide Ink.
- **Lichen** (`lichen`): secondary text, labels and pane headers (6.2:1 or better on every surface).
- **Moss** (`moss`): tertiary text, line numbers and hints (5.1:1 on Oxide Raised).

The neutrals are the ground, and the ground is re-tinted to the hue of the accent: picking the blue accent gives a blue-inked app, not a green one with blue buttons. Lime keeps the house Verdigris ink it was designed on. The severity, syntax and status colours are meanings, not decoration, and they never move.

### Semantic
- **Severity**: critical (`sev-critical`), high (`sev-high`), medium (`sev-medium`), low (`sev-low`), info (`sev-info`). Each is used as text, or as a border over its own 16% tint, and never as a large fill.
- **Status**: success, warning and danger, for provider state, download state and destructive confirmations.
- **Syntax**: `syn-*` colours the AST node types and file-language tags with a real editor palette. They appear only on code-derived content.
- **Diff**: `diff-add-*` and `diff-del-*` for proposed patches.

### Named Rules
**The One Signal Rule.** Signal Lime marks only what the user can act on now, or what is selected. If two lime elements compete on one screen, one of them is wrong.

**The No Grey Rule.** Every neutral carries the oxide undertone. A neutral grey (equal red, green and blue) is a regression.

**The Tint Rule.** The ground carries the hue of the accent. Change the accent and the surfaces, the borders and the text change with it; the palette may not keep the colour of the accent it was set to. The tint is applied by `electron/src/lib/tint.ts`, and it may not cost the palette its contrast.

**The Measured Contrast Rule.** Every text pair is at least 4.5:1 and recorded with its measured ratio in `electron/src/styles/tokens.css`. A new pair is not done until its ratio is in that table.

## Typography

**Interface Font:** the system font (SF Pro on macOS, Segoe UI Variable on Windows, Ubuntu or Cantarell on Linux)
**Code Font:** JetBrains Mono Variable (self-hosted, with ui-monospace fallbacks)

**Character:** the interface speaks the platform's own voice, so it feels native. JetBrains Mono carries everything that is code, a path, a version, a line number or a measurement. Mono is never used as decoration.

### Hierarchy
- **Display** (650, 1.75rem, -0.02em): the welcome screen question only.
- **Headline** (650, 1.25rem, -0.01em): Settings section titles and error screens.
- **Title** (650, 0.875rem): welcome actions, dialog headings and emphasised list items.
- **Body** (400, 0.8125rem, line-height 1.5): all running text, findings and chat. Explanatory text is capped at about 62ch.
- **Label** (650, 0.75rem, 0.01em): pane headers ("Dependencies", "Files", "Findings") and form labels. Always sentence case, never uppercase.
- **Code** (400, 0.75rem, 20px line height): the code view, diffs, the AST, file paths, versions and badges with numbers. Numbers are always tabular.

The root size is fluid, `clamp(15px, 13.4px + 0.16vw, 22px)`, multiplied by the user's interface-size setting (85% to 130%). Every size above is in rem, so the whole interface scales together.

### Named Rules
**The Code Is Mono Rule.** Mono appears only on content a developer would paste into a terminal or editor: code, paths, versions, rule IDs, model names and keyboard keys.

**The No Shouting Rule.** No uppercase tracked labels and no eyebrow text above headings. A heading carries its own weight.

## Layout

The window is a single column: a toolbar that doubles as the title bar, an optional status strip and download tracker, the three-pane shell, and the chat panel.

- **Three panes:** the left column holds the dependency graph above the file tree, the centre holds the Code, AST and Diff tabs, and the right rail holds the findings.
- **Sizes:** the defaults are a left column of 300px, a right rail of 340px, a chat panel of 220px, and a file tree taking 42% of the left column. Each is stored in reference pixels and applied in rem, so sizes scale with the interface.
- **Resizing:** every pane edge is a 5px drag handle that responds to the pointer and to the arrow keys. Double-clicking a handle resets that pane. The chat is the exception: its handle stands at the front of the chat bar, a grip you drag and two arrows that step the panel, so nothing floats over the bar and covers it. Sizes set from the Layout settings glide into place; dragging follows the pointer with no transition.
- **Spacing:** a 4px-based rhythm (0.25 / 0.5 / 0.75 / 1 / 1.5rem). Related controls are packed tightly; panes are separated by borders, never by gutters.
- **Window chrome:** the toolbar is the window's drag region. On macOS it leaves 5.25rem on the left for the inset traffic lights; on Windows and Linux it leaves room on the right for the controls overlay.
- **Minimum size:** the window opens at 1440×900 and can shrink to 960×600 without horizontal scrolling.

## Elevation & Depth

Depth comes from tone, not shadow. Surfaces step from Oxide Inset (recessed) through Oxide Ink and Oxide Surface to Oxide Raised, separated by 1px Patina Border lines. Shadows appear only on things that sit above the page: the Settings window, the package suggestions and the drop overlay.

### Shadow Vocabulary
- **Panel** (`box-shadow: 0 12px 32px rgba(0,0,0,0.45), 0 2px 6px rgba(0,0,0,0.3)`): modal windows such as Settings.
- **Pop** (`box-shadow: 0 14px 30px rgba(0,0,0,0.5), 0 2px 4px rgba(0,0,0,0.35)`): popovers such as the package suggestions.

### Named Rules
**The Shadows Float Rule.** A shadow means "this is above the page". Cards, panes and rows at rest have none. Every shadow has an offset and a soft blur; a zero-offset glow is not part of the system.

## Shapes

Softly squared and functional. Controls use a gently rounded corner (`rounded.md`), small tags and chips a tighter one (`rounded.sm`), containers and tables `rounded.lg`, and the Settings window `rounded.window`. Only status badges and finding counts are pills. Borders are always 1px. Severity is never shown as a thick coloured side stripe.

Tree disclosure arrows are drawn as rotated strokes rather than glyphs, and they turn 90° when a node opens. Indent guides are hairlines spaced 0.95rem apart in the file tree and 1.1rem apart in the AST.

## Components

### Buttons
Native and restrained.
- **Shape:** gently rounded (`rounded.md`), the same 1.75rem height as text fields in the toolbar.
- **Default:** Oxide Raised with a Patina Border.
- **Primary:** a Signal Lime fill with Oxide Ink text, used once per area for its main action.
- **Ghost:** transparent with Lichen text, for secondary tools such as Expand, Collapse and Forget key.
- **Hover / active / focus:** the background lifts to Oxide Hover (primary moves to Signal Lime Strong), a press moves the button down 1px, and keyboard focus shows a 2px Signal Lime ring.
- **Busy:** a 11px spinner appears in place and the button is disabled.
- **Destructive:** two steps. The first click arms the button ("Confirm delete", in danger red), and the arming lapses after 5 seconds.

### Chips and badges
- **Severity chips:** filter toggles. When off they are Oxide Raised with Lichen text; when on they take their severity border over the matching 16% tint.
- **Badges:** pills for status and metadata (language, size, CWE, source). The provider badge in the toolbar carries a status dot and the provider's name, and opens Settings when clicked.

### Finding cards
- Oxide Raised with a 1px border and `rounded.md`. The severity badge leads the header, followed by the CWE, the tool and a confidence meter.
- On hover the card shifts 2px to the right. The selected card takes Signal Lime Soft. A new card eases in once, with a slight blur, and cards that are already shown never re-animate.

### Inputs
- Oxide Inset with a Patina Border and `rounded.md`. The caret is Signal Lime. API keys are password fields whose placeholder says whether a key is already saved in the keychain.

### Navigation
- **Tabs:** Lichen text with no fill; the selected tab takes Bone text on Oxide Ink, with a 2px Signal Lime underline that slides between tabs.
- **Segmented controls:** for mutually exclusive choices, such as the server type. The selected segment rises to Oxide Raised with a hairline inset ring.
- **Preferences sidebar:** Settings is a sheet with a section list on the left; the current section takes Oxide Selected.

### Trees (signature)
The file tree and the AST share one grammar: indent guides, stroke-drawn arrows, a 1.6rem row height and roving keyboard focus (arrow keys, Home, End, and type-to-jump in the file tree).
- **File tree:** files carry the logo of their language — the mark an editor shows, in the colours of that language — dimmed when AISAI cannot scan the language; the header of an open file carries the same mark. Folders carry a teal folder icon and a finding count in their worst severity's colour. Filter matches are highlighted with a Signal Lime tint.
- **AST:** each node type is a chip in its syntax colour, and its line range is set in tabular numbers. Rows stagger in only when a new tree loads, never when a node is toggled.

### Download tracker (signature)
A strip under the status bar lists each package as it downloads: a spinner while it downloads, a dot when it is done (green), has warnings (amber) or was skipped (red), then the name and version in mono and the state in Lichen. Each package's row is reused across updates, so the list never flashes.

### Icons
Drawn as SVG on a 16-unit grid: a 1.5 stroke, round caps and joins, in the current text colour, sized 1em so they sit on the text baseline. The set lives in `electron/src/lib/icons.ts` (gear, chevrons, sort arrows). A new icon is added there in the same stroke, never as a Unicode character. The settings gear turns 45° on hover; no other icon moves.

The language logos are the one exception: they are the file icons of the Material Icon Theme for Visual Studio Code, in `electron/src/assets/langicons`, kept in the colours and the drawing of their own language rather than redrawn on our grid, because a logo people already know is worth more than one that matches the house style. A file of a language with no mark keeps the plain mono tag.

### The mark
The letter A drawn as a syntax tree: a Bone root node and branches, a Patina Teal crossbar edge, and one Signal Lime flagged leaf. While a job runs, the leaf pulses; the mark is the progress indicator.

## Do's and Don'ts

### Do:
- **Do** reserve Signal Lime for the primary action, the focus ring and the current selection.
- **Do** give every neutral the oxide undertone, and record each new text pair's measured contrast in `tokens.css`.
- **Do** set code, paths, versions and numbers in JetBrains Mono with tabular figures.
- **Do** separate panes with 1px borders and let the user resize every one of them.
- **Do** use exponential ease-out (`cubic-bezier(0.16, 1, 0.3, 1)`, 120 / 220 / 520ms) and respect reduced motion.
- **Do** update lists by key so existing rows keep their element, focus and hover; only genuinely new items animate.

### Don't:
- **Don't** use neutral greys, or the GitHub dark palette this system replaced.
- **Don't** use uppercase tracked labels, eyebrow text, or monospace as a "technical" costume.
- **Don't** add shadows to cards, panes or rows at rest, or use zero-offset glows.
- **Don't** mark severity with a thick coloured side border; use the badge and the severity tint.
- **Don't** use emoji or Unicode glyphs as icons in new work; draw them as SVG or CSS strokes.
- **Don't** rebuild a whole list on every store update; that caused the flicker this system was cleaned of.
