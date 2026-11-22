/**
 * The code editor: CodeMirror 6 with the app's findings drawn into it, inline
 * model completion shown as ghost text, and a small toolbar that appears on a
 * selection.
 *
 * The editor knows nothing about the backend. The code view gives it callbacks
 * for the things that need one (completion, ask, fix, change, save), so this
 * file stays a plain widget.
 */

import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands';
import { cpp } from '@codemirror/lang-cpp';
import { go } from '@codemirror/lang-go';
import { java } from '@codemirror/lang-java';
import { javascript } from '@codemirror/lang-javascript';
import { php } from '@codemirror/lang-php';
import { python } from '@codemirror/lang-python';
import { rust } from '@codemirror/lang-rust';
import { HighlightStyle, bracketMatching, indentOnInput, syntaxHighlighting } from '@codemirror/language';
import {
  Compartment,
  EditorState,
  Prec,
  StateEffect,
  RangeSet,
  StateField,
  type Extension,
  type Range,
} from '@codemirror/state';
import {
  Decoration,
  EditorView,
  GutterMarker,
  ViewPlugin,
  WidgetType,
  drawSelection,
  gutter,
  highlightActiveLine,
  highlightActiveLineGutter,
  keymap,
  lineNumbers,
  showTooltip,
  type DecorationSet,
  type Tooltip,
  type ViewUpdate,
} from '@codemirror/view';
import { tags } from '@lezer/highlight';

import type { CodeSelection, Finding, Severity } from '../api/types';
import { h } from '../lib/dom';
import { severityColor } from '../lib/severity';

/** What the selection toolbar can do. The code view implements them. */
export interface SelectionActions {
  ask: (selection: CodeSelection) => void;
  fix: (selection: CodeSelection) => void;
  change: (selection: CodeSelection) => void;
}

export interface EditorHooks {
  /** Asks the model for ghost text at the cursor. Resolves to '' for none. */
  complete: (prefix: string, suffix: string) => Promise<string>;
  actions: SelectionActions;
  onDirty: (dirty: boolean) => void;
  onSave: () => void;
  /** The breakpoint lines changed: a click in the gutter, or an edit moved them. */
  onBreakpoints?: (lines: number[]) => void;
}

export interface CodeEditor {
  /** Replaces the document. Undo history starts over. */
  setDocument: (path: string, text: string, language: string, readOnly: boolean) => void;
  setFindings: (findings: Finding[]) => void;
  text: () => string;
  path: () => string;
  isDirty: () => boolean;
  /** Declares the current text to be what is on disk. */
  markSaved: () => void;
  /** Replaces a range, as an edit the user can undo. It types itself in and is briefly highlighted. */
  replace: (from: number, to: number, text: string) => void;
  /**
   * Shows a new version of the open file (a fix that was applied) by typing
   * the changed part in place. Returns false when the change is too large to
   * animate, so the caller replaces the document instead.
   */
  showChange: (text: string) => boolean;
  selection: () => (CodeSelection & { from: number; to: number }) | null;
  /** Text around the selection, for the model's context. */
  around: (from: number, to: number) => { before: string; after: string };
  reveal: (line: number) => void;
  /** Lets completion run only when a model can answer. */
  setCompletionEnabled: (enabled: boolean) => void;
  /** Shows these breakpoint lines (1-based). */
  setBreakpoints: (lines: number[]) => void;
  /** Marks the line the debugger is paused on, or clears it with null. */
  setPausedLine: (line: number | null) => void;
  focus: () => void;
  destroy: () => void;
}

/* ---------------- language ---------------- */

function languageExtension(language: string, path: string): Extension {
  const ext = path.split('.').pop()?.toLowerCase() ?? '';
  switch (language.toLowerCase() || ext) {
    case 'python':
    case 'py':
      return python();
    case 'javascript':
    case 'js':
    case 'jsx':
    case 'mjs':
    case 'cjs':
      return javascript({ jsx: true });
    case 'typescript':
    case 'ts':
      return javascript({ typescript: true });
    case 'tsx':
      return javascript({ typescript: true, jsx: true });
    case 'go':
      return go();
    case 'rust':
    case 'rs':
      return rust();
    case 'java':
      return java();
    case 'c':
    case 'cpp':
    case 'c++':
    case 'h':
    case 'hpp':
    case 'cc':
      return cpp();
    case 'php':
      return php();
    default:
      return [];
  }
}

/* ---------------- theme ---------------- */

const theme = EditorView.theme(
  {
    '&': { height: '100%', backgroundColor: 'var(--bg-base)', color: 'var(--fg)' },
    '.cm-scroller': { fontFamily: 'var(--font-mono)', fontSize: '13px', lineHeight: '20px', overflow: 'auto' },
    '.cm-content': { caretColor: 'var(--accent)', padding: '0' },
    '.cm-gutters': { backgroundColor: 'var(--bg-base)', color: 'var(--fg-muted)', border: 'none' },
    '.cm-lineNumbers .cm-gutterElement': { padding: '0 8px 0 12px', minWidth: '3.2em' },
    '.cm-activeLine': { backgroundColor: 'color-mix(in srgb, var(--accent) 6%, transparent)' },
    '.cm-activeLineGutter': { backgroundColor: 'transparent', color: 'var(--fg)' },
    '&.cm-focused': { outline: 'none' },
    '&.cm-focused .cm-cursor': { borderLeftColor: 'var(--accent)' },
    '.cm-selectionBackground, &.cm-focused .cm-selectionBackground': {
      backgroundColor: 'color-mix(in srgb, var(--accent) 28%, transparent) !important',
    },
    '.cm-ghost': { color: 'var(--fg-muted)', opacity: '0.75', fontStyle: 'italic', whiteSpace: 'pre' },
    '.cm-finding-line': { backgroundColor: 'color-mix(in srgb, var(--sev-high, #e5484d) 14%, transparent)' },
    '.cm-finding-gutter': { width: '6px' },
    '.cm-tooltip': { border: 'none', backgroundColor: 'transparent' },
  },
  { dark: true },
);

const highlight = HighlightStyle.define([
  { tag: [tags.keyword, tags.modifier, tags.controlKeyword], color: '#c792ea' },
  { tag: [tags.string, tags.special(tags.string)], color: '#c3e88d' },
  { tag: [tags.number, tags.bool, tags.null], color: '#f78c6c' },
  { tag: [tags.comment, tags.lineComment, tags.blockComment], color: '#6b8a88', fontStyle: 'italic' },
  { tag: [tags.function(tags.variableName), tags.function(tags.propertyName)], color: '#82aaff' },
  { tag: [tags.className, tags.typeName], color: '#ffcb6b' },
  { tag: [tags.operator, tags.punctuation], color: '#89ddff' },
  { tag: [tags.propertyName, tags.attributeName], color: '#b2ccd6' },
]);

/* ---------------- findings ---------------- */

const setFindingsEffect = StateEffect.define<Finding[]>();

const SEVERITY_ORDER: Severity[] = ['critical', 'high', 'medium', 'low', 'info'];

/** The worst severity of the findings that start on each line. */
function worstByLine(findings: Finding[]): Map<number, Severity> {
  const out = new Map<number, Severity>();
  for (const finding of findings) {
    const have = out.get(finding.startLine);
    if (!have || SEVERITY_ORDER.indexOf(finding.severity) < SEVERITY_ORDER.indexOf(have)) {
      out.set(finding.startLine, finding.severity);
    }
  }
  return out;
}

/** The findings of the file and the worst severity per line, computed once per change. */
interface FindingsState {
  list: Finding[];
  byLine: Map<number, Severity>;
}
const findingsField = StateField.define<FindingsState>({
  create: () => ({ list: [], byLine: new Map() }),
  update(value, tr) {
    for (const effect of tr.effects) {
      if (effect.is(setFindingsEffect)) return { list: effect.value, byLine: worstByLine(effect.value) };
    }
    return value;
  },
});

const findingDecorations = StateField.define<DecorationSet>({
  create: () => Decoration.none,
  update(value, tr) {
    const changed = tr.effects.some((e) => e.is(setFindingsEffect));
    if (!changed && !tr.docChanged) return value.map(tr.changes);
    const lines = tr.state.field(findingsField).byLine;
    const ranges: Range<Decoration>[] = [];
    for (const [line] of lines) {
      if (line < 1 || line > tr.state.doc.lines) continue;
      ranges.push(Decoration.line({ class: 'cm-finding-line', attributes: { title: 'A finding is reported on this line' } }).range(tr.state.doc.line(line).from));
    }
    return Decoration.set(ranges, true);
  },
  provide: (field) => EditorView.decorations.from(field),
});

class FindingMarker extends GutterMarker {
  constructor(private readonly severity: Severity) {
    super();
  }
  override eq(other: FindingMarker): boolean {
    return other.severity === this.severity;
  }
  override toDOM(): HTMLElement {
    return h('span', {
      class: 'cm-finding-dot',
      style: `display:block;width:4px;height:16px;margin:2px 0 0 1px;border-radius:2px;background:${severityColor(this.severity)}`,
    });
  }
}

const findingGutter = gutter({
  class: 'cm-finding-gutter',
  lineMarker(view, line) {
    const number = view.state.doc.lineAt(line.from).number;
    const severity = view.state.field(findingsField).byLine.get(number);
    return severity ? new FindingMarker(severity) : null;
  },
  lineMarkerChange: (update) => update.transactions.some((tr) => tr.effects.some((e) => e.is(setFindingsEffect))) || update.docChanged,
});


/* ---------------- breakpoints and the paused line ---------------- */

class BreakpointMarker extends GutterMarker {
  override toDOM(): HTMLElement {
    return h('span', { class: 'cm-breakpoint', 'aria-label': 'Breakpoint' });
  }
}
const breakpointMarker = new BreakpointMarker();

/** Replaces every breakpoint (from the debug store). */
const setBreakpointsEffect = StateEffect.define<number[]>();
/** Turns the breakpoint on one line (a document position) on or off. */
const toggleBreakpointEffect = StateEffect.define<number>();

/** The breakpoints, kept as positions so they move with the code above them. */
const breakpointField = StateField.define<RangeSet<GutterMarker>>({
  create: () => RangeSet.empty,
  update(value, tr) {
    let set = value.map(tr.changes);
    for (const effect of tr.effects) {
      if (effect.is(setBreakpointsEffect)) {
        const doc = tr.state.doc;
        const ranges = effect.value
          .filter((line) => line >= 1 && line <= doc.lines)
          .sort((a, b) => a - b)
          .map((line) => breakpointMarker.range(doc.line(line).from));
        set = RangeSet.of(ranges, true);
      } else if (effect.is(toggleBreakpointEffect)) {
        const pos = effect.value;
        let had = false;
        set.between(pos, pos, () => {
          had = true;
        });
        set = had
          ? set.update({ filter: (from) => from !== pos })
          : set.update({ add: [breakpointMarker.range(pos)] });
      }
    }
    return set;
  },
});

function breakpointLines(state: EditorState): number[] {
  const lines = new Set<number>();
  const cursor = state.field(breakpointField).iter();
  for (; cursor.value; cursor.next()) lines.add(state.doc.lineAt(cursor.from).number);
  return Array.from(lines).sort((a, b) => a - b);
}

const breakpointGutter = gutter({
  class: 'cm-breakpoint-gutter',
  markers: (view) => view.state.field(breakpointField),
  initialSpacer: () => breakpointMarker,
  domEventHandlers: {
    mousedown(view, line) {
      view.dispatch({ effects: toggleBreakpointEffect.of(line.from) });
      return true;
    },
  },
});

const setPausedEffect = StateEffect.define<number | null>();

class PausedMarker extends GutterMarker {
  override toDOM(): HTMLElement {
    return h('span', { class: 'cm-paused-arrow', 'aria-label': 'Paused here' });
  }
}
const pausedMarker = new PausedMarker();

/** The line the debugger is paused on, as a line decoration and a gutter arrow. */
const pausedField = StateField.define<{ line: number | null; deco: DecorationSet; marks: RangeSet<GutterMarker> }>({
  create: () => ({ line: null, deco: Decoration.none, marks: RangeSet.empty }),
  update(value, tr) {
    let line = value.line;
    let changed = false;
    for (const effect of tr.effects) {
      if (effect.is(setPausedEffect)) {
        line = effect.value;
        changed = true;
      }
    }
    if (!changed) {
      return tr.docChanged ? { line, deco: value.deco.map(tr.changes), marks: value.marks.map(tr.changes) } : value;
    }
    if (line === null || line < 1 || line > tr.state.doc.lines) return { line: null, deco: Decoration.none, marks: RangeSet.empty };
    const from = tr.state.doc.line(line).from;
    return {
      line,
      deco: Decoration.set([Decoration.line({ class: 'cm-paused-line' }).range(from)]),
      marks: RangeSet.of([pausedMarker.range(from)]),
    };
  },
  provide: (field) => EditorView.decorations.from(field, (v) => v.deco),
});

const pausedGutter = gutter({
  class: 'cm-paused-gutter',
  markers: (view) => view.state.field(pausedField).marks,
});

/* ---------------- ghost text ---------------- */

interface Ghost {
  pos: number;
  text: string;
}
const setGhostEffect = StateEffect.define<Ghost | null>();

class GhostWidget extends WidgetType {
  constructor(private readonly text: string) {
    super();
  }
  override eq(other: GhostWidget): boolean {
    return other.text === this.text;
  }
  override toDOM(): HTMLElement {
    return h('span', { class: 'cm-ghost', 'aria-hidden': 'true', text: this.text });
  }
  override ignoreEvent(): boolean {
    return true;
  }
}

const ghostField = StateField.define<Ghost | null>({
  create: () => null,
  update(value, tr) {
    for (const effect of tr.effects) if (effect.is(setGhostEffect)) return effect.value;
    // Any edit or cursor move ends the suggestion.
    if (tr.docChanged || tr.selection) return null;
    return value;
  },
  provide: (field) =>
    EditorView.decorations.from(field, (ghost) =>
      ghost ? Decoration.set([Decoration.widget({ widget: new GhostWidget(ghost.text), side: 1 }).range(ghost.pos)]) : Decoration.none,
    ),
});

/* ---------------- selection toolbar ---------------- */

function selectionTooltip(actions: () => SelectionActions, describe: (view: EditorView) => CodeSelection | null): Extension {
  const field = StateField.define<Tooltip | null>({
    create: () => null,
    update(value, tr) {
      if (!tr.selection && !tr.docChanged && value) return value;
      const range = tr.state.selection.main;
      if (range.empty) return null;
      return {
        pos: range.to,
        above: false,
        strategy: 'fixed',
        arrow: false,
        create: (view) => {
          const pick = (run: (a: SelectionActions) => (s: CodeSelection) => void) => () => {
            const selection = describe(view);
            if (selection) run(actions())(selection);
          };
          const button = (label: string, title: string, onClick: () => void): HTMLElement =>
            h('button', { class: 'tiny', type: 'button', title, onmousedown: (e: Event) => e.preventDefault(), onclick: onClick }, label);
          const dom = h(
            'div',
            { class: 'selection-toolbar', role: 'toolbar', 'aria-label': 'Selection actions' },
            button('Ask in chat', 'Ask the chat about this code', pick((a) => a.ask)),
            button('Fix', 'Ask the model to fix this code', pick((a) => a.fix)),
            button('Change…', 'Ask the model to change this code', pick((a) => a.change)),
          );
          return { dom };
        },
      };
    },
    provide: (f) => showTooltip.from(f),
  });
  return field;
}

/* ---------------- typing in the model's changes ---------------- */

/**
 * A change made by the model is applied at once (one undo step), but it is
 * revealed like a typewriter: the part not yet "typed" is hidden, then the
 * changed lines glow briefly. This field holds both ranges.
 */
interface RevealState {
  /** The part of the change not shown yet. */
  hidden: { from: number; to: number } | null;
  /** The lines to highlight. */
  flash: { from: number; to: number } | null;
}
const setRevealEffect = StateEffect.define<RevealState>();
const hiddenMark = Decoration.mark({ class: 'cm-ai-pending' });
const flashLine = Decoration.line({ class: 'cm-ai-flash' });
const revealField = StateField.define<RevealState>({
  create: () => ({ hidden: null, flash: null }),
  update(value, tr) {
    for (const effect of tr.effects) if (effect.is(setRevealEffect)) return effect.value;
    if (!tr.docChanged) return value;
    const map = (r: { from: number; to: number } | null) =>
      r ? { from: tr.changes.mapPos(r.from, 1), to: tr.changes.mapPos(r.to, -1) } : null;
    return { hidden: map(value.hidden), flash: map(value.flash) };
  },
});

/** The decorations of the reveal, computed against the document. */
const revealDecorations = EditorView.decorations.compute([revealField], (state) => {
  const { hidden, flash } = state.field(revealField);
  const ranges: Range<Decoration>[] = [];
  if (flash) {
    const first = state.doc.lineAt(Math.min(flash.from, state.doc.length)).number;
    const last = state.doc.lineAt(Math.min(Math.max(flash.from, flash.to), state.doc.length)).number;
    for (let n = first; n <= last && n - first < 400; n++) ranges.push(flashLine.range(state.doc.line(n).from));
  }
  if (hidden && hidden.to > hidden.from) ranges.push(hiddenMark.range(hidden.from, Math.min(hidden.to, state.doc.length)));
  return Decoration.set(ranges, true);
});

/** Reveal [from, to) like typing, then glow the lines for a moment. */
function typeIn(view: EditorView, from: number, to: number): void {
  const length = to - from;
  const reduce = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches;
  const flashAfter = (): void => {
    view.dispatch({ effects: setRevealEffect.of({ hidden: null, flash: { from, to } }) });
    window.setTimeout(() => {
      if (view.state.field(revealField).flash) view.dispatch({ effects: setRevealEffect.of({ hidden: null, flash: null }) });
    }, 1800);
  };
  if (length <= 0 || reduce) {
    flashAfter();
    return;
  }
  // About 120 characters a frame at most, and never longer than a second.
  const duration = Math.min(1000, Math.max(250, length * 6));
  const start = performance.now();
  const step = (now: number): void => {
    const t = Math.min(1, (now - start) / duration);
    const shown = from + Math.round(length * (1 - Math.pow(1 - t, 2)));
    if (t >= 1) {
      flashAfter();
      return;
    }
    view.dispatch({ effects: setRevealEffect.of({ hidden: { from: shown, to }, flash: null }) });
    requestAnimationFrame(step);
  };
  view.dispatch({ effects: setRevealEffect.of({ hidden: { from, to }, flash: null }) });
  requestAnimationFrame(step);
}

/* ---------------- the minimap ---------------- */

/** Width of the minimap in CSS pixels, and the height of one line in it. */
const MINIMAP_WIDTH = 84;
const MINIMAP_LINE = 2;

/**
 * A scaled-down picture of the whole file beside the editor: each line is a
 * strip as long as its text, lines with findings are tinted by severity, and
 * the visible part is outlined. Click or drag to scroll.
 */
const minimap = ViewPlugin.fromClass(
  class {
    private readonly canvas = document.createElement('canvas');
    private readonly view: EditorView;
    private frame = 0;
    private dragging = false;

    constructor(view: EditorView) {
      this.view = view;
      this.canvas.className = 'cm-minimap';
      this.canvas.setAttribute('aria-hidden', 'true');
      view.dom.append(this.canvas);
      view.dom.classList.add('has-minimap');
      this.canvas.addEventListener('pointerdown', (event) => {
        this.dragging = true;
        this.canvas.setPointerCapture(event.pointerId);
        this.scrollTo(event);
      });
      this.canvas.addEventListener('pointermove', (event) => this.dragging && this.scrollTo(event));
      this.canvas.addEventListener('pointerup', () => (this.dragging = false));
      view.scrollDOM.addEventListener('scroll', this.schedule);
      this.schedule();
    }

    update(update: ViewUpdate): void {
      if (update.docChanged || update.viewportChanged || update.geometryChanged || update.transactions.length) this.schedule();
    }

    destroy(): void {
      cancelAnimationFrame(this.frame);
      this.view.scrollDOM.removeEventListener('scroll', this.schedule);
      this.view.dom.classList.remove('has-minimap');
      this.canvas.remove();
    }

    private readonly schedule = (): void => {
      cancelAnimationFrame(this.frame);
      this.frame = requestAnimationFrame(() => this.draw());
    };

    /** The canvas offset, in minimap pixels, that keeps the viewport in sight. */
    private offset(totalHeight: number, height: number): number {
      const { scrollDOM } = this.view;
      if (totalHeight <= height) return 0;
      const ratio = scrollDOM.scrollTop / Math.max(1, scrollDOM.scrollHeight - scrollDOM.clientHeight);
      return ratio * (totalHeight - height);
    }

    private scrollTo(event: PointerEvent): void {
      const { scrollDOM, state } = this.view;
      const rect = this.canvas.getBoundingClientRect();
      const total = state.doc.lines * MINIMAP_LINE;
      const y = event.clientY - rect.top + this.offset(total, rect.height);
      const fraction = Math.min(1, Math.max(0, y / Math.max(1, total)));
      scrollDOM.scrollTop = fraction * scrollDOM.scrollHeight - scrollDOM.clientHeight / 2;
    }

    private draw(): void {
      const { state, scrollDOM } = this.view;
      const dpr = window.devicePixelRatio || 1;
      const width = MINIMAP_WIDTH;
      const height = this.canvas.clientHeight;
      if (!height) return;
      this.canvas.width = Math.round(width * dpr);
      this.canvas.height = Math.round(height * dpr);
      const ctx = this.canvas.getContext('2d');
      if (!ctx) return;
      ctx.scale(dpr, dpr);

      const css = getComputedStyle(this.view.dom);
      const ink = css.getPropertyValue('--fg-muted').trim() || '#8ea5a0';
      const accent = css.getPropertyValue('--accent').trim() || '#b4f23c';
      const sevColor: Record<string, string> = {
        critical: css.getPropertyValue('--sev-critical').trim() || '#ff7a6b',
        high: css.getPropertyValue('--sev-high').trim() || '#ff9f4a',
        medium: css.getPropertyValue('--sev-medium').trim() || '#f0c93a',
        low: css.getPropertyValue('--sev-low').trim() || '#5fd3c4',
        info: css.getPropertyValue('--sev-info').trim() || '#8ea5a0',
      };
      const byLine = state.field(findingsField).byLine;

      const total = state.doc.lines * MINIMAP_LINE;
      const offset = this.offset(total, height);
      const first = Math.max(1, Math.floor(offset / MINIMAP_LINE));
      const last = Math.min(state.doc.lines, Math.ceil((offset + height) / MINIMAP_LINE));
      const charW = (width - 8) / 100;
      for (let n = first; n <= last; n++) {
        const line = state.doc.line(n);
        const y = (n - 1) * MINIMAP_LINE - offset;
        const text = line.text;
        const indent = text.length - text.trimStart().length;
        const len = Math.min(100 - indent, text.trimEnd().length - indent);
        const severity = byLine.get(n);
        if (severity) {
          ctx.globalAlpha = 0.25;
          ctx.fillStyle = sevColor[severity] ?? accent;
          ctx.fillRect(0, y, width, MINIMAP_LINE);
        }
        if (len > 0) {
          ctx.globalAlpha = severity ? 0.95 : 0.55;
          ctx.fillStyle = severity ? sevColor[severity] ?? accent : ink;
          ctx.fillRect(4 + Math.min(indent, 40) * charW, y, Math.max(1, len * charW), Math.max(1, MINIMAP_LINE - 0.5));
        }
      }

      // The part of the file on screen.
      const viewTop = (scrollDOM.scrollTop / Math.max(1, scrollDOM.scrollHeight)) * total - offset;
      const viewHeight = Math.max(8, (scrollDOM.clientHeight / Math.max(1, scrollDOM.scrollHeight)) * total);
      ctx.globalAlpha = 0.14;
      ctx.fillStyle = accent;
      ctx.fillRect(0, viewTop, width, viewHeight);
      ctx.globalAlpha = 0.7;
      ctx.strokeStyle = accent;
      ctx.strokeRect(0.5, viewTop + 0.5, width - 1, viewHeight - 1);
    }
  },
);

/* ---------------- the editor ---------------- */

/** Wait after the last keystroke before asking the model for a completion. */
const COMPLETE_DELAY_MS = 650;

export function createCodeEditor(host: HTMLElement, hooks: EditorHooks): CodeEditor {
  let currentPath = '';
  let savedText = '';
  let dirty = false;
  let completionEnabled = false;
  let completeTimer: ReturnType<typeof setTimeout> | null = null;
  let requestToken = 0;
  /** One completion request at a time: a slow model must not pile them up. */
  let inFlight = false;

  const language = new Compartment();
  const readOnly = new Compartment();

  const describeSelection = (view: EditorView): CodeSelection | null => {
    const range = view.state.selection.main;
    if (range.empty) return null;
    return {
      file: currentPath,
      startLine: view.state.doc.lineAt(range.from).number,
      endLine: view.state.doc.lineAt(range.to).number,
      text: view.state.sliceDoc(range.from, range.to),
    };
  };

  const cancelCompletion = (): void => {
    if (completeTimer) clearTimeout(completeTimer);
    completeTimer = null;
    requestToken += 1;
  };

  const scheduleCompletion = (view: EditorView): void => {
    cancelCompletion();
    if (!completionEnabled || view.state.readOnly) return;
    const token = requestToken;
    completeTimer = setTimeout(() => {
      completeTimer = null;
      const { head, empty } = view.state.selection.main;
      if (!empty) return;
      const line = view.state.doc.lineAt(head);
      // Only suggest at the end of a line (or before trailing blanks), where
      // ghost text would not push code around.
      if (view.state.sliceDoc(head, line.to).trim() !== '') return;
      const prefix = view.state.sliceDoc(0, head);
      if (!prefix.trim()) return;
      const suffix = view.state.sliceDoc(head);
      if (inFlight) return;
      inFlight = true;
      void hooks
        .complete(prefix, suffix)
        .finally(() => {
          inFlight = false;
        })
        .then((text) => {
        // A newer keystroke, or a cursor that moved, makes this stale.
        if (token !== requestToken || !text || view.state.selection.main.head !== head) return;
        view.dispatch({ effects: setGhostEffect.of({ pos: head, text }) });
      });
    }, COMPLETE_DELAY_MS);
  };

  const acceptGhost = (view: EditorView): boolean => {
    const ghost = view.state.field(ghostField);
    if (!ghost) return false;
    view.dispatch({
      changes: { from: ghost.pos, insert: ghost.text },
      selection: { anchor: ghost.pos + ghost.text.length },
      userEvent: 'input.complete',
    });
    return true;
  };

  const extensions: Extension[] = [
        theme,
        syntaxHighlighting(highlight),
        breakpointGutter,
        pausedGutter,
        lineNumbers(),
        findingGutter,
        highlightActiveLine(),
        highlightActiveLineGutter(),
        history(),
        drawSelection(),
        indentOnInput(),
        bracketMatching(),
        EditorState.tabSize.of(4),
        language.of([]),
        readOnly.of(EditorState.readOnly.of(true)),
        findingsField,
        findingDecorations,
        breakpointField,
        pausedField,
        revealField,
        revealDecorations,
        minimap,
        ghostField,
        selectionTooltip(() => hooks.actions, describeSelection),
        Prec.highest(
          keymap.of([
            { key: 'Tab', run: acceptGhost },
            {
              key: 'F9',
              preventDefault: true,
              run: (v) => {
                v.dispatch({ effects: toggleBreakpointEffect.of(v.state.doc.lineAt(v.state.selection.main.head).from) });
                return true;
              },
            },
            {
              key: 'Escape',
              run: (v) => {
                if (!v.state.field(ghostField)) return false;
                cancelCompletion();
                v.dispatch({ effects: setGhostEffect.of(null) });
                return true;
              },
            },
            {
              key: 'Mod-s',
              preventDefault: true,
              run: () => {
                hooks.onSave();
                return true;
              },
            },
          ]),
        ),
        keymap.of([...defaultKeymap, ...historyKeymap, indentWithTab]),
        EditorView.updateListener.of((update: ViewUpdate) => {
          // Breakpoints the user set or an edit moved are reported; the ones
          // the debug store sent are not echoed back.
          const external = update.transactions.some((tr) => tr.effects.some((e) => e.is(setBreakpointsEffect)));
          if (!external && hooks.onBreakpoints && update.startState.field(breakpointField) !== update.state.field(breakpointField)) {
            const before = breakpointLines(update.startState);
            const after = breakpointLines(update.state);
            if (before.join(',') !== after.join(',')) hooks.onBreakpoints(after);
          }
          if (update.docChanged) {
            const nowDirty = update.state.doc.toString() !== savedText;
            if (nowDirty !== dirty) {
              dirty = nowDirty;
              hooks.onDirty(dirty);
            }
            // Only the user's own typing asks for a completion.
            if (update.transactions.some((tr) => tr.isUserEvent('input.type') || tr.isUserEvent('input.paste') || tr.isUserEvent('delete'))) {
              scheduleCompletion(update.view);
            } else if (!update.transactions.some((tr) => tr.isUserEvent('input.complete'))) {
              cancelCompletion();
            }
          } else if (update.selectionSet) {
            cancelCompletion();
          }
        }),
  ];

  const view = new EditorView({ parent: host, state: EditorState.create({ doc: '', extensions }) });

  // Lets a script (the smoke test, a future keyboard command) select whole lines.
  host.addEventListener('aisai:select-lines', (event) => {
    const { startLine, endLine } = (event as CustomEvent<{ startLine: number; endLine: number }>).detail;
    const doc = view.state.doc;
    const from = doc.line(Math.max(1, Math.min(doc.lines, startLine))).from;
    const to = doc.line(Math.max(1, Math.min(doc.lines, endLine))).to;
    view.focus();
    view.dispatch({ selection: { anchor: from, head: to } });
  });

  return {
    setDocument: (path, text, lang, locked) => {
      cancelCompletion();
      currentPath = path;
      savedText = text;
      dirty = false;
      hooks.onDirty(false);
      // A new state starts the undo history over, and the compartments are
      // then set for this file.
      view.setState(EditorState.create({ doc: text, extensions }));
      view.dispatch({
        effects: [
          language.reconfigure(languageExtension(lang, path)),
          readOnly.reconfigure(EditorState.readOnly.of(locked)),
        ],
      });
    },
    setFindings: (findings) => view.dispatch({ effects: setFindingsEffect.of(findings) }),
    text: () => view.state.doc.toString(),
    path: () => currentPath,
    isDirty: () => dirty,
    markSaved: () => {
      savedText = view.state.doc.toString();
      dirty = false;
      hooks.onDirty(false);
    },
    replace: (from, to, text) => {
      view.dispatch({ changes: { from, to, insert: text }, selection: { anchor: from + text.length }, userEvent: 'input.replace' });
      typeIn(view, from, from + text.length);
    },
    showChange: (text) => {
      const old = view.state.doc.toString();
      if (old === text) return true;
      let start = 0;
      const max = Math.min(old.length, text.length);
      while (start < max && old.charCodeAt(start) === text.charCodeAt(start)) start++;
      let endOld = old.length;
      let endNew = text.length;
      while (endOld > start && endNew > start && old.charCodeAt(endOld - 1) === text.charCodeAt(endNew - 1)) {
        endOld--;
        endNew--;
      }
      const inserted = text.slice(start, endNew);
      // A rewrite of most of the file is just loaded; typing it in would be slow.
      if (inserted.length > 20000) return false;
      view.dispatch({ changes: { from: start, to: endOld, insert: inserted }, effects: EditorView.scrollIntoView(start, { y: 'center' }) });
      savedText = view.state.doc.toString();
      dirty = false;
      hooks.onDirty(false);
      typeIn(view, start, start + inserted.length);
      return true;
    },
    selection: () => {
      const described = describeSelection(view);
      if (!described) return null;
      const range = view.state.selection.main;
      return { ...described, from: range.from, to: range.to };
    },
    around: (from, to) => ({
      before: view.state.sliceDoc(Math.max(0, from - 6000), from),
      after: view.state.sliceDoc(to, Math.min(view.state.doc.length, to + 6000)),
    }),
    reveal: (line) => {
      const n = Math.max(1, Math.min(view.state.doc.lines, line));
      const pos = view.state.doc.line(n).from;
      view.dispatch({
        selection: { anchor: pos },
        effects: EditorView.scrollIntoView(pos, { y: 'center' }),
      });
    },
    setCompletionEnabled: (enabled) => {
      completionEnabled = enabled;
      if (!enabled) {
        cancelCompletion();
        if (view.state.field(ghostField)) view.dispatch({ effects: setGhostEffect.of(null) });
      }
    },
    setBreakpoints: (lines) => {
      if (breakpointLines(view.state).join(',') === [...lines].sort((a, b) => a - b).join(',')) return;
      view.dispatch({ effects: setBreakpointsEffect.of(lines) });
    },
    setPausedLine: (line) => {
      if (view.state.field(pausedField).line === line) return;
      const effects: StateEffect<unknown>[] = [setPausedEffect.of(line)];
      if (line !== null && line >= 1 && line <= view.state.doc.lines) {
        effects.push(EditorView.scrollIntoView(view.state.doc.line(line).from, { y: 'nearest', yMargin: 80 }));
      }
      view.dispatch({ effects });
    },
    focus: () => view.focus(),
    destroy: () => {
      cancelCompletion();
      view.destroy();
    },
  };
}
