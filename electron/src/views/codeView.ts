/**
 * The Code tab: the open file in the editor (views/codeEditor.ts), with its
 * header (path, language, findings, unsaved state, Debug and completion
 * switches), the welcome screen when nothing is open, and the inline chat
 * that changes selected code through the model and shows the proposal as a
 * diff before anything is applied.
 *
 * Breakpoints and the paused line come from the debug store; Save, Undo and
 * Redo arrive from the menu and shortcuts as aisai:editor-command events.
 */

import type { AppController } from '../app';
import type { CodeSelection, Finding } from '../api/types';
import { h } from '../lib/dom';
import { useAuditStore } from '../state/audit';
import { useFindingsStore } from '../state/findings';
import { brandMark } from '../lib/logo';
import { useWorkspaceStore } from '../state/workspace';
import { pausedLineIn, useDebugStore } from '../state/debug';

import { useChatStore } from '../state/chat';
import { useUiStore } from '../state/ui';
import { isMac } from '../lib/shortcuts';
import { icon } from '../lib/icons';
import { diffCounts, diffLines } from '../lib/linediff';
import { noTourTakenYet } from './tutorials';
import { confirmDialog } from './confirm';
import { createCodeEditor, type CodeEditor } from './codeEditor';

export interface CodeView {
  element: HTMLElement;
  dispose: () => void;
}

/** The result of a "fix" or "change" request, waiting for the user's decision. */
interface PendingEdit {
  from: number;
  to: number;
  original: string;
  replacement: string;
}

export function createCodeView(controller: AppController, container: HTMLElement): CodeView {
  const header = h('div', { class: 'file-header' }, h('span', { text: 'No file open' }));
  const editPanel = h('div', { class: 'edit-panel', hidden: true });
  const editorHost = h('div', { class: 'code-editor', hidden: true });
  const placeholder = h('div', { class: 'code-lines' });
  container.dataset.tour = 'code';
  header.dataset.tour = 'file-header';
  container.append(header, editPanel, editorHost, placeholder);

  let shownKey = '';
  let shownHeader = '';
  let dirty = false;
  /** The file changed on disk (a patch was applied) while it had unsaved edits. */
  let diskChanged = false;

  /** The first screen: two ways in, and the audits already on disk. */
  const welcome = (deleted: { package: string } | null = null): HTMLElement => {
    const scanPackage = h('button', { class: 'welcome-action primary-action', type: 'button' },
      h('span', { class: 'welcome-action-title', text: 'Audit a package' }),
      h('span', { class: 'welcome-action-text', text: 'From PyPI, npm or crates.io, or paste a package or repository link: pull the source and its dependencies, then run a static or LLM scan.' }),
    );
    scanPackage.addEventListener('click', () => document.getElementById('package-input')?.focus());
    const openFolder = h('button', { class: 'welcome-action', type: 'button' },
      h('span', { class: 'welcome-action-title', text: 'Open a local project' }),
      h('span', { class: 'welcome-action-text', text: 'Scan a folder on this computer. Any of 14 languages. Or drop it on the window.' }),
    );
    openFolder.addEventListener('click', () => void controller.chooseFolder());
    const recent = useAuditStore.getState().audits.slice(0, 6);
    return h(
      'div',
      { class: 'welcome' },
      h('div', { class: 'welcome-mark' }, brandMark()),
      h('h1', { class: 'welcome-title', text: deleted ? `The sources of ${deleted.package} were deleted` : 'What should we look at?' }),
      deleted
        ? h('p', { class: 'welcome-lede', text: 'Its findings and report are kept, but there is no code left to read or scan. Pull it again, or start with something else.' })
        : null,
      h('div', { class: 'welcome-actions' }, scanPackage, openFolder),
      h('button', { class: 'welcome-tour', type: 'button', onclick: () => window.dispatchEvent(new Event('aisai:tutorials')) },
        icon('play'),
        noTourTakenYet() ? 'New here? Take a two-minute tour' : 'Tutorials',
      ),
      h('p', { class: 'welcome-keys' },
        h('kbd', { text: navigator.userAgent.includes('Mac') ? '⌘O' : 'Ctrl+O' }), ' opens a folder · ',
        h('kbd', { text: 'Enter' }), ' in the package field starts a pull',
      ),
      recent.length
        ? h('div', { class: 'welcome-recent' },
            h('h2', { text: 'Recent audits' }),
            h('ul', {}, ...recent.map((audit) =>
              h('li', {},
                h('button', { class: 'welcome-recent-item', type: 'button', onclick: () => void controller.selectAudit(audit.id) },
                  h('span', { class: 'source-name', text: audit.package }),
                  audit.version ? h('span', { class: 'source-version', text: audit.version }) : null,
                  h('span', { class: 'grow' }),
                  h('span', { class: 'muted', text: `${audit.findingCount ?? 0} findings` }),
                ),
              ),
            )),
          )
        : null,
    );
  };


  /* ---------------- editing ---------------- */

  const save = async (): Promise<void> => {
    if (!editor.isDirty()) return;
    const path = editor.path();
    const text = editor.text();
    if (diskChanged) {
      const ok = await confirmDialog('The file changed on disk since you started editing (a fix was applied). Saving overwrites that change.', {
        confirmLabel: 'Overwrite',
        danger: true,
        title: 'Overwrite the change on disk?',
      });
      if (!ok) return;
    }
    const ok = await controller.saveFile(path, text);
    if (ok) {
      editor.markSaved();
      diskChanged = false;
      refreshHeader();
    }
  };

  const discard = (): void => {
    const file = useWorkspaceStore.getState().file;
    if (file) loadFile(file, true);
  };

  /* ---------------- inline chat ----------------
   *
   * Select code and pick Fix or Change (or press ⌘K / Ctrl+K): a box opens
   * above the editor to say what should change, the model's answer comes
   * back as a line diff, and nothing touches the file until Accept. Accept
   * puts the change in the editor as one undo step; Save writes it. */

  /** Bumped on every request, so an answer that arrives after Cancel is dropped. */
  let editToken = 0;
  /** The instructions given before, newest last, for ↑ in the box. */
  const instructionHistory: string[] = [];

  /** Closes the edit panel. */
  const closePanel = (): void => {
    editToken += 1;
    editPanel.hidden = true;
    editPanel.replaceChildren();
    editPanel.onkeydown = null;
  };

  const lineRange = (sel: { startLine: number; endLine: number }): string =>
    sel.startLine === sel.endLine ? `line ${sel.startLine}` : `lines ${sel.startLine}–${sel.endLine}`;

  const showResult = (pending: PendingEdit, sel: { startLine: number; endLine: number }, instruction: string): void => {
    const diff = diffLines(pending.original, pending.replacement);
    const { added, removed } = diffCounts(diff);
    const accept = h('button', { class: 'primary tiny', type: 'button', title: `Put the change in the editor (${isMac() ? '⌘↵' : 'Ctrl+Enter'})` }, 'Accept');
    const reject = h('button', { class: 'tiny', type: 'button', title: 'Throw the proposal away (Esc)' }, 'Discard');
    const retry = h('button', { class: 'ghost tiny', type: 'button', title: 'Ask again with the same instruction' }, 'Try again');
    const refine = h('input', {
      type: 'text',
      class: 'inline-refine',
      placeholder: 'Ask for a further change to this proposal… e.g. “keep the old name”',
      'aria-label': 'A further change to the proposal',
    });
    const doAccept = (): void => {
      editor.replace(pending.from, pending.to, pending.replacement);
      closePanel();
      editor.focus();
    };
    accept.addEventListener('click', doAccept);
    reject.addEventListener('click', () => {
      closePanel();
      editor.focus();
    });
    retry.addEventListener('click', () => void runEdit(instruction, { from: pending.from, to: pending.to, text: pending.original, ...sel }));
    refine.addEventListener('keydown', (event) => {
      if (event.key !== 'Enter') return;
      event.preventDefault();
      const more = refine.value.trim();
      if (!more) return;
      // The further change applies to the proposal; the diff stays against the original.
      void runEdit(more, { from: pending.from, to: pending.to, text: pending.original, ...sel }, pending.replacement);
    });
    editPanel.onkeydown = (event) => {
      if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') {
        event.preventDefault();
        doAccept();
      } else if (event.key === 'Escape') {
        event.preventDefault();
        closePanel();
        editor.focus();
      }
    };
    editPanel.hidden = false;
    editPanel.replaceChildren(
      h('div', { class: 'edit-panel-head' },
        h('strong', { text: 'Proposed change' }),
        h('span', { class: 'muted inline-meta', text: `${lineRange(sel)} · “${instruction}”` }),
        h('span', { class: 'diff-count' }, h('span', { class: 'is-add', text: `+${added}` }), ' ', h('span', { class: 'is-remove', text: `−${removed}` })),
        h('span', { class: 'grow' }),
        retry, reject, accept,
      ),
      h('div', { class: 'inline-diff', role: 'region', 'aria-label': 'The proposed change, line by line' },
        ...diff.map((line) =>
          h('div', { class: `inline-diff-line is-${line.kind}` },
            h('span', { class: 'inline-diff-mark', 'aria-hidden': 'true', text: line.kind === 'add' ? '+' : line.kind === 'remove' ? '−' : ' ' }),
            h('span', { class: 'sr-only', text: line.kind === 'add' ? 'added: ' : line.kind === 'remove' ? 'removed: ' : '' }),
            h('code', { text: line.text || ' ' }),
          ),
        ),
      ),
      refine,
      h('p', { class: 'muted edit-note', text: `Accept puts the change in the editor; Save keeps it, and undo takes it back. ${isMac() ? '⌘↵' : 'Ctrl+Enter'} accepts, Esc discards.` }),
    );
    accept.focus();
  };

  const showError = (message: string): void => {
    editPanel.hidden = false;
    const close = h('button', { class: 'tiny', type: 'button' }, 'Close');
    close.addEventListener('click', closePanel);
    editPanel.replaceChildren(
      h('div', { class: 'edit-panel-head' }, h('strong', { text: 'The model could not help' }), h('span', { class: 'grow' }), close),
      h('p', { class: 'edit-note', text: message }),
    );
  };

  interface EditTarget {
    from: number;
    to: number;
    text: string;
    startLine: number;
    endLine: number;
  }

  /** Asks the model to rewrite the selection and shows the result for review. */
  const runEdit = async (instruction: string, target?: EditTarget, starting?: string): Promise<void> => {
    const sel = target ?? (() => {
      const current = editor.selection();
      return current ? { from: current.from, to: current.to, text: current.text, startLine: current.startLine, endLine: current.endLine } : null;
    })();
    if (!sel) return;
    const file = useWorkspaceStore.getState().file;
    const token = ++editToken;
    const cancel = h('button', { class: 'ghost tiny', type: 'button' }, 'Cancel');
    cancel.addEventListener('click', () => {
      closePanel();
      editor.focus();
    });
    editPanel.hidden = false;
    editPanel.onkeydown = (event) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        closePanel();
        editor.focus();
      }
    };
    const model = useChatStore.getState().model.name;
    editPanel.replaceChildren(
      h('div', { class: 'edit-panel-head', role: 'status' },
        h('span', { class: 'spinner', 'aria-hidden': 'true' }),
        h('span', { class: 'shimmer', text: `Asking ${model || 'the model'} to change ${lineRange(sel)}…` }),
        h('span', { class: 'muted inline-meta', text: `“${instruction}”` }),
        h('span', { class: 'grow' }),
        cancel,
      ),
    );
    cancel.focus();
    try {
      const { before, after } = editor.around(sel.from, sel.to);
      const replacement = await controller.editSelection({
        path: editor.path(),
        language: file?.language ?? '',
        before,
        selection: starting ?? sel.text,
        after,
        instruction,
        startLine: sel.startLine,
        endLine: sel.endLine,
      });
      if (token !== editToken) return; // cancelled, or superseded
      if (replacement === sel.text) {
        showError('The model suggested no change to this code. Try saying more precisely what should change.');
        return;
      }
      showResult({ from: sel.from, to: sel.to, original: sel.text, replacement }, sel, instruction);
    } catch (err) {
      if (token !== editToken) return;
      showError(err instanceof Error ? err.message : String(err));
    }
  };

  /** Ideas for the instruction box; a click fills it in. */
  const SUGGESTIONS = [
    'Fix the security weakness',
    'Validate the input',
    'Handle errors properly',
    'Simplify this',
    'Add type hints',
    'Add comments explaining it',
  ];

  const askForChange = (): void => {
    let sel = editor.selection();
    if (!sel) {
      // ⌘K with no selection means the current line.
      editor.selectLine(editor.cursorLine());
      sel = editor.selection();
      if (!sel) return;
    }
    const target: EditTarget = { from: sel.from, to: sel.to, text: sel.text, startLine: sel.startLine, endLine: sel.endLine };
    const input = h('textarea', {
      class: 'inline-instruction',
      rows: '1',
      placeholder: 'What should change? e.g. “use a constant-time comparison”',
      'aria-label': 'What to change',
    });
    const go = h('button', { class: 'primary tiny', type: 'button' }, 'Change');
    const cancel = h('button', { class: 'ghost tiny', type: 'button' }, 'Cancel');
    let historyAt = instructionHistory.length;
    const grow = (): void => {
      input.style.height = 'auto';
      input.style.height = `${Math.min(140, input.scrollHeight)}px`;
    };
    const submit = (): void => {
      const text = input.value.trim();
      if (!text) {
        input.focus();
        return;
      }
      if (instructionHistory[instructionHistory.length - 1] !== text) instructionHistory.push(text);
      void runEdit(text, target);
    };
    go.addEventListener('click', submit);
    cancel.addEventListener('click', () => {
      closePanel();
      editor.focus();
    });
    input.addEventListener('input', grow);
    input.addEventListener('keydown', (event) => {
      if (event.key === 'Enter' && !event.shiftKey) {
        event.preventDefault();
        submit();
      } else if (event.key === 'Escape') {
        event.preventDefault();
        closePanel();
        editor.focus();
      } else if (event.key === 'ArrowUp' && !input.value.includes('\n') && instructionHistory.length) {
        event.preventDefault();
        historyAt = Math.max(0, historyAt - 1);
        input.value = instructionHistory[historyAt] ?? '';
        grow();
      } else if (event.key === 'ArrowDown' && !input.value.includes('\n') && instructionHistory.length) {
        event.preventDefault();
        historyAt = Math.min(instructionHistory.length, historyAt + 1);
        input.value = instructionHistory[historyAt] ?? '';
        grow();
      }
    });
    const chips = SUGGESTIONS.map((text) =>
      h('button', {
        class: 'chip inline-suggestion',
        type: 'button',
        onclick: () => {
          input.value = text;
          grow();
          input.focus();
        },
      }, text),
    );
    editPanel.hidden = false;
    editPanel.onkeydown = null;
    editPanel.replaceChildren(
      h('div', { class: 'edit-panel-head' },
        h('strong', { text: 'Change the code' }),
        h('span', { class: 'muted inline-meta', text: `${lineRange(target)} of ${editor.path().split('/').pop() ?? 'this file'}` }),
        h('span', { class: 'grow' }),
      ),
      h('div', { class: 'inline-input-row' }, input, go, cancel),
      h('div', { class: 'inline-suggestions' }, ...chips),
      h('p', { class: 'muted edit-note', text: '↵ sends · ⇧↵ new line · ↑ earlier instructions · Esc cancels' }),
    );
    input.focus();
  };

  const actions = {
    ask: (selection: CodeSelection): void => {
      useChatStore.getState().setSelection(selection);
      window.dispatchEvent(new Event('aisai:focus-chat'));
    },
    explain: (selection: CodeSelection): void => {
      useChatStore.getState().setSelection(selection);
      window.dispatchEvent(new CustomEvent('aisai:focus-chat', { detail: { ask: 'Explain what this code does, step by step, and point out anything risky.' } }));
    },
    fix: (): void => {
      void runEdit('Fix the bug or security weakness in this code. Change as little as possible.');
    },
    change: (): void => askForChange(),
  };

  const editor: CodeEditor = createCodeEditor(editorHost, {
    complete: (prefix, suffix) => {
      const file = useWorkspaceStore.getState().file;
      return controller.complete(file?.path ?? '', file?.language ?? '', prefix, suffix);
    },
    actions,
    onDirty: (value) => {
      dirty = value;
      refreshHeader();
    },
    onSave: () => void save(),
    onInlineChat: () => askForChange(),
    onBreakpoints: (lines) => {
      const path = editor.path();
      if (path) useDebugStore.getState().setBreakpoints(path, lines);
    },
    debugValue: (name) => {
      const debug = useDebugStore.getState();
      if (pausedLineIn(debug, editor.path()) === null) return null;
      const found = debug.locals.find((v) => v.name === name) ?? debug.globals.find((v) => v.name === name);
      return found ? { value: found.value, type: found.type } : null;
    },
  });

  /** "a = 1  b = 'x'" for the variables the paused line uses. */
  const inlineValuesFor = (line: number): string => {
    const debug = useDebugStore.getState();
    const text = editor.text().split('\n')[line - 1] ?? '';
    const names = new Set(text.match(/[A-Za-z_][A-Za-z0-9_]*/g) ?? []);
    const parts: string[] = [];
    for (const v of [...debug.locals, ...debug.globals]) {
      if (!names.has(v.name) || parts.some((p) => p.startsWith(`${v.name} =`))) continue;
      const value = v.value.length > 40 ? `${v.value.slice(0, 39)}…` : v.value;
      parts.push(`${v.name} = ${value}`);
      if (parts.length >= 5) break;
    }
    const joined = parts.join('   ');
    return joined.length > 160 ? `${joined.slice(0, 159)}…` : joined;
  };

  /** Shows the breakpoints and the paused line of the open file. */
  const syncDebug = (): void => {
    const path = editor.path();
    if (!path) return;
    const debug = useDebugStore.getState();
    editor.setBreakpoints(debug.breakpoints[path] ?? []);
    const paused = pausedLineIn(debug, path);
    editor.setPausedLine(paused);
    editor.setInlineValues(paused, paused === null ? '' : inlineValuesFor(paused));
  };

  // Run to cursor: from the shortcut, the debug panel or the header button.
  const onRunToCursor = (): void => {
    const path = editor.path();
    if (!path || !/\.py$/i.test(path) || !controller.canDebug()) return;
    void controller.runToCursor(path, editor.cursorLine());
  };
  window.addEventListener('aisai:run-to-cursor', onRunToCursor);

  /* ---------------- header ---------------- */

  let headerFile: { path: string; language: string; findings: number } | null = null;

  const refreshHeader = (): void => {
    if (!headerFile) return;
    const model = useChatStore.getState().model;
    const completionOn = useUiStore.getState().completionEnabled;
    const saveButton = h('button', { class: 'primary tiny', type: 'button', title: 'Save (Ctrl/Cmd+S)' }, 'Save');
    const discardButton = h('button', { class: 'tiny', type: 'button', title: 'Throw away the unsaved changes' }, 'Discard');
    saveButton.addEventListener('click', () => void save());
    discardButton.addEventListener('click', discard);
    header.replaceChildren(
      h('span', { text: headerFile.path }),
      h('span', { class: 'badge neutral', text: headerFile.language || 'text' }),
      h('span', {
        class: `badge ${headerFile.findings ? 'bad' : 'ok'}`,
        text: headerFile.findings ? `${headerFile.findings} finding(s)` : 'no findings',
      }),
      ...(dirty ? [h('span', { class: 'badge warn', text: 'unsaved' })] : []),
      ...(diskChanged
        ? [
            h('span', {
              class: 'badge bad',
              text: 'changed on disk',
              title: 'The file was changed (a fix was applied) while you had unsaved edits. Saving now would overwrite that change.',
            }),
            h('button', { class: 'tiny', type: 'button', title: 'Load the file as it is on disk and drop your edits', onclick: () => discard() }, 'Reload'),
          ]
        : []),
      h('span', { class: 'grow' }),
      ...scanFileButtons(),
      ...debugButton(),
      h('button', {
        class: `tiny completion-toggle${completionOn && model.available ? ' is-on' : ''}`,
        type: 'button',
        role: 'switch',
        'aria-checked': String(completionOn && model.available),
        disabled: !model.available || undefined,
        text: completionOn && model.available ? 'completion on' : 'completion off',
        title: !model.available
          ? `${model.reason} Inline completion needs a model.`
          : completionOn
            ? `Inline completion by ${model.name}. Tab accepts, Esc dismisses. Click to turn off (${isMac() ? '⌘⌥K' : 'Ctrl+Alt+K'}).`
            : `Inline completion is off. Click to turn on (${isMac() ? '⌘⌥K' : 'Ctrl+Alt+K'}).`,
        onclick: () => useUiStore.getState().setCompletionEnabled(!useUiStore.getState().completionEnabled),
      }),
      ...(dirty ? [discardButton, saveButton] : []),
    );
  };

  /** Scans only the open file: the static rules, or the model's review. */
  const scanFile = (engine: 'sast' | 'llm'): void => {
    const path = editor.path();
    if (!path) return;
    // Unsaved edits are not on disk yet, and a scan reads the disk.
    if (editor.isDirty()) {
      useAuditStore.getState().setError('Save the file first: a scan reads the saved file.');
      return;
    }
    void controller.runScan(engine, path);
  };
  const onScanFile = (): void => {
    const audit = useAuditStore.getState();
    if (audit.current?.status === 'ready' && audit.busy === null) scanFile('sast');
  };
  window.addEventListener('aisai:scan-file', onScanFile);

  const scanFileButtons = (): HTMLElement[] => {
    const audit = useAuditStore.getState();
    const ready = audit.current?.status === 'ready' && !audit.current.sourcesDeleted && audit.busy === null;
    const model = useChatStore.getState().model;
    const staticBtn = h('button', {
      class: 'tiny scan-file-button',
      type: 'button',
      disabled: !ready || undefined,
      title: ready ? `Run the static rules over this file only (${isMac() ? '⌘⌥S' : 'Ctrl+Alt+S'})` : 'Wait for the current job to finish',
      onclick: () => scanFile('sast'),
    }, 'Scan file');
    const llmBtn = h('button', {
      class: 'tiny scan-file-button',
      type: 'button',
      disabled: !ready || !model.available || undefined,
      title: !model.available ? `${model.reason} The model review needs a model.` : `Ask ${model.name} to review this file only`,
      onclick: () => scanFile('llm'),
    }, 'LLM review');
    return [staticBtn, llmBtn];
  };

  /** "Debug" for a Python file of a local project; the panel explains the rest. */
  const debugButton = (): HTMLElement[] => {
    if (!headerFile || !/\.py$/i.test(headerFile.path) || !controller.canDebug()) return [];
    const debug = useDebugStore.getState();
    const running = debug.status === 'running' || debug.status === 'paused' || debug.status === 'starting';
    const count = Object.values(debug.breakpoints).reduce((n, lines) => n + lines.length, 0);
    return [
      h('button', {
        class: `tiny debug-file-button${running ? ' is-on' : ''}`,
        type: 'button',
        title: running
          ? 'The debugger is running: show it'
          : `Run this file under the debugger (F5)${count ? ` · ${count} breakpoint${count === 1 ? '' : 's'}` : ' · click a line number to set a breakpoint'}`,
        onclick: () => {
          useUiStore.getState().setBottomView('debug');
          useUiStore.getState().setTerminalOpen(true);
          if (!running) window.dispatchEvent(new Event('aisai:debug-start'));
        },
      }, icon('bug'), running ? (debug.status === 'paused' ? 'paused' : 'debugging') : 'Debug'),
    ];
  };

  /* ---------------- rendering ---------------- */

  const loadFile = (file: NonNullable<ReturnType<typeof useWorkspaceStore.getState>['file']>, force = false): void => {
    const audit = useAuditStore.getState().current;
    const locked = Boolean(audit?.sourcesDeleted);
    const text = file.lines.join('\n');
    const samePath = editor.path() === file.path;
    // The text the user is typing is never replaced from under them; they
    // are told the file changed and choose.
    if (!force && samePath && editor.isDirty()) {
      if (editor.text() !== text && !diskChanged) {
        diskChanged = true;
        refreshHeader();
      }
      return;
    }
    if (!force && samePath && editor.text() === text) return;
    diskChanged = false;
    closePanel();
    // The same file came back changed (a fix was applied): type the change in.
    if (!force && samePath && editor.showChange(text)) {
      editor.setFindings(findingsFor(file));
      return;
    }
    editor.setDocument(file.path, text, file.language, locked);
    editor.setFindings(findingsFor(file));
    syncDebug();
  };

  const findingsFor = (file: { path: string; findings?: Finding[] }): Finding[] => {
    const all = useFindingsStore.getState().findings;
    return [
      ...(file.findings ?? []),
      ...all.filter((f) => f.file === file.path && !(file.findings ?? []).some((g) => g.id === f.id)),
    ];
  };

  const render = (): void => {
    const state = useWorkspaceStore.getState();
    const file = state.file;

    const audits = useAuditStore.getState().audits;
    const currentAudit = useAuditStore.getState().current;
    const deletedAudit = currentAudit?.sourcesDeleted ? currentAudit : null;
    if (!file && (!currentAudit || deletedAudit)) {
      const key = `welcome|${deletedAudit?.id ?? ''}|${audits.map((a) => a.id).join(',')}`;
      if (shownKey === key) return;
      shownKey = key;
      shownHeader = '';
      headerFile = null;
      editorHost.hidden = true;
      placeholder.hidden = false;
      header.replaceChildren(h('span', { text: 'Start' }));
      placeholder.replaceChildren(welcome(deletedAudit));
      return;
    }

    if (!file) {
      if (shownKey === 'none') return;
      shownKey = 'none';
      shownHeader = '';
      headerFile = null;
      editorHost.hidden = true;
      placeholder.hidden = false;
      header.replaceChildren(h('span', { text: 'No file open' }));
      placeholder.replaceChildren(
        h(
          'div',
          { class: 'empty-state' },
          h('h3', { text: 'Pick a file to read the code' }),
          h('p', {
            text:
              'Choose a file in the tree on the left, or click a finding in the rail to jump ' +
              'straight to the offending line. Findings are highlighted in the gutter and inline. ' +
              'You can edit the file here: select code to ask about it, fix it or change it.',
          }),
        ),
      );
      return;
    }

    shownKey = `file|${file.path}`;
    placeholder.hidden = true;
    editorHost.hidden = false;
    const findings = findingsFor(file);
    const headerKey = `${file.path}|${findings.length}`;
    if (headerKey !== shownHeader) {
      shownHeader = headerKey;
      headerFile = { path: file.path, language: file.language, findings: findings.length };
      refreshHeader();
    }
    loadFile(file);
    editor.setFindings(findings);
  };

  const syncCompletion = (): void => {
    editor.setCompletionEnabled(useChatStore.getState().model.available && useUiStore.getState().completionEnabled);
    refreshHeader();
  };

  const unsubscribe = useWorkspaceStore.subscribe((state, previous) => {
    if (state.file !== previous.file) {
      render();
    }
    if (state.jumpToken !== previous.jumpToken && state.jumpLine) {
      editor.reveal(state.jumpLine);
    }
  });

  const unsubscribeFindings = useFindingsStore.subscribe((state, previous) => {
    if (state.findings !== previous.findings) render();
  });

  const unsubscribeAudit = useAuditStore.subscribe((state, previous) => {
    if (state.current !== previous.current || state.audits !== previous.audits) {
      render();
      refreshHeader();
    }
    // The scan buttons are disabled while a job runs.
    if (state.busy !== previous.busy) refreshHeader();
  });

  const unsubscribeCompletion = useUiStore.subscribe((state, previous) => {
    if (state.completionEnabled !== previous.completionEnabled) syncCompletion();
  });

  const unsubscribeChat = useChatStore.subscribe((state, previous) => {
    if (state.model !== previous.model) syncCompletion();
  });

  // Save, Undo and Redo from the menu, or their keys pressed outside the editor.
  const onEditorCommand = (event: Event): void => {
    const command = (event as CustomEvent<'save' | 'undo' | 'redo'>).detail;
    if (!editor.path()) return;
    if (command === 'save') void save();
    else if (command === 'undo') editor.undo();
    else if (command === 'redo') editor.redo();
  };
  window.addEventListener('aisai:editor-command', onEditorCommand);

  const unsubscribeDebug = useDebugStore.subscribe((state, previous) => {
    if (state.breakpoints !== previous.breakpoints || state.status !== previous.status || state.frames !== previous.frames || state.frame !== previous.frame || state.locals !== previous.locals) {
      syncDebug();
    }
    if (state.status !== previous.status || state.breakpoints !== previous.breakpoints) refreshHeader();
  });

  render();
  syncCompletion();

  return {
    element: container,
    dispose: () => {
      unsubscribe();
      unsubscribeFindings();
      unsubscribeAudit();
      unsubscribeChat();
      unsubscribeCompletion();
      unsubscribeDebug();
      window.removeEventListener('aisai:editor-command', onEditorCommand);
      window.removeEventListener('aisai:run-to-cursor', onRunToCursor);
      window.removeEventListener('aisai:scan-file', onScanFile);
      editor.destroy();
    },
  };
}
