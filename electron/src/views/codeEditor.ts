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
  StateField,
  type Extension,
  type Range,
} from '@codemirror/state';
import {
  Decoration,
  EditorView,
  GutterMarker,
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
  /** Replaces a range, as an edit the user can undo. */
  replace: (from: number, to: number, text: string) => void;
  selection: () => (CodeSelection & { from: number; to: number }) | null;
  /** Text around the selection, for the model's context. */
  around: (from: number, to: number) => { before: string; after: string };
  reveal: (line: number) => void;
  /** Lets completion run only when a model can answer. */
  setCompletionEnabled: (enabled: boolean) => void;
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
        ghostField,
        selectionTooltip(() => hooks.actions, describeSelection),
        Prec.highest(
          keymap.of([
            { key: 'Tab', run: acceptGhost },
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
    replace: (from, to, text) =>
      view.dispatch({ changes: { from, to, insert: text }, selection: { anchor: from + text.length }, userEvent: 'input.replace' }),
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
    focus: () => view.focus(),
    destroy: () => {
      cancelCompletion();
      view.destroy();
    },
  };
}
