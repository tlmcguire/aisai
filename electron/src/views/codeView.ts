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
    if (diskChanged && !window.confirm('The file changed on disk since you started editing (a fix was applied). Save anyway and overwrite that change?')) {
      return;
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

  /** Closes the edit panel. */
  const closePanel = (): void => {
    editPanel.hidden = true;
    editPanel.replaceChildren();
  };

  const showResult = (pending: PendingEdit): void => {
    const accept = h('button', { class: 'primary tiny', type: 'button' }, 'Accept');
    const reject = h('button', { class: 'tiny', type: 'button' }, 'Discard');
    accept.addEventListener('click', () => {
      editor.replace(pending.from, pending.to, pending.replacement);
      closePanel();
      editor.focus();
    });
    reject.addEventListener('click', () => {
      closePanel();
      editor.focus();
    });
    editPanel.hidden = false;
    editPanel.replaceChildren(
      h('div', { class: 'edit-panel-head' }, h('strong', { text: 'Proposed change' }), h('span', { class: 'grow' }), reject, accept),
      h(
        'div',
        { class: 'edit-diff' },
        h('pre', { class: 'edit-before', text: pending.original.replace(/\n$/, '') }),
        h('pre', { class: 'edit-after', text: pending.replacement.replace(/\n$/, '') }),
      ),
      h('p', { class: 'muted edit-note', text: 'Accepting puts the change in the editor. Save to keep it; undo takes it back.' }),
    );
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

  /** Asks the model to rewrite the selection and shows the result for review. */
  const runEdit = async (instruction: string): Promise<void> => {
    const sel = editor.selection();
    if (!sel) return;
    const file = useWorkspaceStore.getState().file;
    editPanel.hidden = false;
    editPanel.replaceChildren(
      h('div', { class: 'chat-status', role: 'status' }, h('span', { class: 'shimmer', text: 'Asking the model…' })),
    );
    try {
      const { before, after } = editor.around(sel.from, sel.to);
      const replacement = await controller.editSelection({
        path: sel.file,
        language: file?.language ?? '',
        before,
        selection: sel.text,
        after,
        instruction,
        startLine: sel.startLine,
        endLine: sel.endLine,
      });
      if (replacement === sel.text) {
        showError('The model suggested no change to this code.');
        return;
      }
      showResult({ from: sel.from, to: sel.to, original: sel.text, replacement });
    } catch (err) {
      showError(err instanceof Error ? err.message : String(err));
    }
  };

  const askForChange = (): void => {
    const input = h('input', { type: 'text', placeholder: 'What should change? e.g. “use a constant-time comparison”', 'aria-label': 'What to change' });
    const go = h('button', { class: 'primary tiny', type: 'button' }, 'Change');
    const cancel = h('button', { class: 'tiny', type: 'button' }, 'Cancel');
    const submit = (): void => {
      const text = input.value.trim();
      if (text) void runEdit(text);
    };
    go.addEventListener('click', submit);
    cancel.addEventListener('click', closePanel);
    input.addEventListener('keydown', (event) => {
      if (event.key === 'Enter') {
        event.preventDefault();
        submit();
      } else if (event.key === 'Escape') closePanel();
    });
    editPanel.hidden = false;
    editPanel.replaceChildren(h('div', { class: 'edit-panel-head' }, input, go, cancel));
    input.focus();
  };

  const actions = {
    ask: (selection: CodeSelection): void => {
      useChatStore.getState().setSelection(selection);
      window.dispatchEvent(new Event('aisai:focus-chat'));
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
    onBreakpoints: (lines) => {
      const path = editor.path();
      if (path) useDebugStore.getState().setBreakpoints(path, lines);
    },
  });

  /** Shows the breakpoints and the paused line of the open file. */
  const syncDebug = (): void => {
    const path = editor.path();
    if (!path) return;
    const debug = useDebugStore.getState();
    editor.setBreakpoints(debug.breakpoints[path] ?? []);
    editor.setPausedLine(pausedLineIn(debug, path));
  };

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
          : `Run this file under the debugger (F5)${count ? ` · ${count} breakpoint${count === 1 ? '' : 's'}` : ' · click left of a line number to set a breakpoint'}`,
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
  });

  const unsubscribeCompletion = useUiStore.subscribe((state, previous) => {
    if (state.completionEnabled !== previous.completionEnabled) syncCompletion();
  });

  const unsubscribeChat = useChatStore.subscribe((state, previous) => {
    if (state.model !== previous.model) syncCompletion();
  });

  const unsubscribeDebug = useDebugStore.subscribe((state, previous) => {
    if (state.breakpoints !== previous.breakpoints || state.status !== previous.status || state.frames !== previous.frames || state.frame !== previous.frame) {
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
      editor.destroy();
    },
  };
}
