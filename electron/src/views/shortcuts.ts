import type { AppController } from '../app';
import { h } from '../lib/dom';
import { keysFor, toAccelerator, useKeybindingStore } from '../lib/keybindings';
import { displayKeys, isMac, matches, parseKeys, type Shortcut } from '../lib/shortcuts';
import { useAuditStore } from '../state/audit';
import { useChatStore } from '../state/chat';
import { useUiStore } from '../state/ui';

/** What the code view does for the Save, Undo and Redo shortcuts and menu items. */
export type EditorCommand = 'save' | 'undo' | 'redo';

/** Asks the code view to save, undo or redo in the open file. */
export function editorCommand(command: EditorCommand): void {
  window.dispatchEvent(new CustomEvent<EditorCommand>('aisai:editor-command', { detail: command }));
}

/**
 * Undo or redo where the focus is: a text field undoes its own typing, and
 * anywhere else the open file is meant (after clicking Accept, say).
 * document.execCommand is deprecated but is still the only way to drive a
 * field's native undo stack: https://developer.mozilla.org/docs/Web/API/Document/execCommand
 */
function undoOrRedo(command: 'undo' | 'redo'): void {
  const el = document.activeElement as HTMLElement | null;
  if (el && (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' || el.isContentEditable) && !el.closest('.cm-editor')) {
    document.execCommand(command);
    return;
  }
  editorCommand(command);
}

/** The default redo keys: ⇧⌘Z on macOS, Ctrl+Y on Windows and Linux. */
const REDO_KEYS = isMac() ? 'Mod+Shift+Z' : 'Mod+Y';

/**
 * The app's shortcuts, with their default keys. The order is the order of the
 * reference sheet and of the Keyboard section of Settings. Ids are stable:
 * they key the user's own bindings, so they must not be renamed.
 */
export function appShortcuts(controller: AppController, openSheet: () => void): Shortcut[] {
  const ui = () => useUiStore.getState();
  const canScan = (): boolean => {
    const audit = useAuditStore.getState();
    return audit.current?.status === 'ready' && audit.busy === null;
  };
  return [
    { id: 'app.settings', group: 'General', keys: 'Mod+,', label: 'Open Settings', inFields: true, run: () => ui().setSettingsOpen(true) },
    { id: 'app.shortcuts', group: 'General', keys: 'Mod+Shift+/', label: 'Show keyboard shortcuts', inFields: true, run: openSheet },
    { id: 'app.terminal', group: 'General', keys: 'Ctrl+`', label: 'Show or hide the terminal', inFields: true, run: () => ui().setTerminalOpen(!ui().terminalOpen) },
    { id: 'app.tutorials', group: 'General', keys: '', label: 'Open the tutorials', inFields: true, run: () => window.dispatchEvent(new Event('aisai:tutorials')) },

    { id: 'file.save', group: 'File', keys: 'Mod+S', label: 'Save the open file', inFields: true, run: () => editorCommand('save') },
    { id: 'file.open-folder', group: 'File', keys: 'Mod+O', label: 'Open a project folder', inFields: true, run: () => void controller.chooseFolder() },
    { id: 'file.export-pdf', group: 'File', keys: 'Mod+Shift+E', label: 'Export the report as PDF', inFields: true, run: () => window.dispatchEvent(new CustomEvent('aisai:export-report', { detail: 'pdf' })) },

    { id: 'edit.undo', group: 'Edit', keys: 'Mod+Z', label: 'Undo (also takes back an accepted change)', inFields: true, run: () => undoOrRedo('undo') },
    { id: 'edit.redo', group: 'Edit', keys: REDO_KEYS, label: 'Redo', inFields: true, run: () => undoOrRedo('redo') },
    { id: 'edit.copy', group: 'Edit', keys: 'Mod+C', label: 'Copy', fixed: true },
    { id: 'edit.paste', group: 'Edit', keys: 'Mod+V', label: 'Paste', fixed: true },
    { id: 'edit.select-all', group: 'Edit', keys: 'Mod+A', label: 'Select all', fixed: true },

    { id: 'editor.inlineChat', group: 'Editor', keys: 'Mod+K', label: 'Change the selected code with the model (inline chat)' },
    { id: 'editor.breakpoint', group: 'Editor', keys: 'F9', label: 'Set or remove a breakpoint on the cursor line' },
    { id: 'editor.completion', group: 'Editor', keys: 'Mod+Alt+K', label: 'Turn inline completion on or off', inFields: true, run: () => ui().setCompletionEnabled(!ui().completionEnabled) },
    { id: 'editor.accept', group: 'Editor', keys: 'Tab', label: 'Accept the suggested completion', fixed: true },
    { id: 'editor.dismiss', group: 'Editor', keys: 'Escape', label: 'Dismiss the suggestion', fixed: true },

    { id: 'debug.start', group: 'Debug', keys: 'F5', label: 'Debug the open Python file, or continue', inFields: true, run: () => window.dispatchEvent(new Event('aisai:debug-start')) },
    { id: 'debug.stop', group: 'Debug', keys: 'Shift+F5', label: 'Stop the program', inFields: true, run: () => void controller.stopDebug() },
    { id: 'debug.restart', group: 'Debug', keys: 'Mod+Shift+F5', label: 'Restart the program', inFields: true, run: () => void controller.restartDebug() },
    { id: 'debug.pause', group: 'Debug', keys: 'F6', label: 'Pause', inFields: true, run: () => controller.debugCommand('pause') },
    { id: 'debug.stepOver', group: 'Debug', keys: 'F10', label: 'Step over', inFields: true, run: () => controller.debugCommand('next') },
    { id: 'debug.stepInto', group: 'Debug', keys: 'F11', label: 'Step into', inFields: true, run: () => controller.debugCommand('step') },
    { id: 'debug.stepOut', group: 'Debug', keys: 'Shift+F11', label: 'Step out', inFields: true, run: () => controller.debugCommand('out') },
    { id: 'debug.runToCursor', group: 'Debug', keys: 'Mod+F10', label: 'Run to the cursor line', inFields: true, run: () => window.dispatchEvent(new Event('aisai:run-to-cursor')) },

    { id: 'audit.package', group: 'Audit', keys: 'Mod+L', label: 'Go to the package field (outside the editor)', run: () => document.getElementById('package-input')?.focus() },
    { id: 'audit.scanFile', group: 'Audit', keys: 'Mod+Alt+S', label: 'Scan the open file only', inFields: true, run: () => window.dispatchEvent(new Event('aisai:scan-file')) },
    { id: 'audit.static', group: 'Audit', keys: 'Mod+Shift+S', label: 'Static scan', inFields: true, run: () => canScan() && void controller.runScan('sast') },
    { id: 'audit.llm', group: 'Audit', keys: 'Mod+Shift+L', label: 'LLM scan', inFields: true, run: () => canScan() && void controller.runScan('llm') },

    { id: 'view.code', group: 'View', keys: 'Mod+1', label: 'Code tab', inFields: true, run: () => ui().setActiveTab('code') },
    { id: 'view.ast', group: 'View', keys: 'Mod+2', label: 'AST tab', inFields: true, run: () => ui().setActiveTab('ast') },
    { id: 'view.diff', group: 'View', keys: 'Mod+3', label: 'Diff tab', inFields: true, run: () => ui().setActiveTab('diff') },
    { id: 'view.deps', group: 'View', keys: 'Mod+B', label: 'Show or hide the dependencies pane', inFields: true, run: () => ui().setPaneHidden('deps', !ui().hiddenPanes.includes('deps')) },
    { id: 'view.findings', group: 'View', keys: 'Mod+Alt+B', label: 'Show or hide the findings pane', inFields: true, run: () => ui().setPaneHidden('findings', !ui().hiddenPanes.includes('findings')) },

    { id: 'chat.focus', group: 'Chat', keys: 'Mod+J', label: 'Go to the chat', inFields: true, run: () => window.dispatchEvent(new Event('aisai:focus-chat')) },
    {
      id: 'chat.new',
      group: 'Chat',
      keys: 'Mod+Shift+N',
      label: 'New chat',
      inFields: true,
      run: () => {
        useChatStore.getState().newThread();
        window.dispatchEvent(new Event('aisai:focus-chat'));
      },
    },
  ];
}

/** A shortcut with the user's binding applied in place of its default. */
export function boundShortcuts(list: Shortcut[]): Shortcut[] {
  return list.map((s) => (s.fixed ? s : { ...s, keys: keysFor(s.id, s.keys) }));
}

/** The actions the application menu shows keys for, by shortcut id. */
const MENU_IDS = ['app.settings', 'app.shortcuts', 'file.save', 'file.open-folder', 'file.export-pdf', 'edit.undo', 'edit.redo'];

/**
 * Whether a key event is one of the app's shortcuts that work everywhere. The
 * terminal asks, so it lets those keys through instead of sending them to the
 * shell.
 */
let globalShortcut: (event: KeyboardEvent) => boolean = () => false;
export const isGlobalShortcut = (event: KeyboardEvent): boolean => globalShortcut(event);

/** Whether the focus is somewhere text is typed. */
function inTextField(target: EventTarget | null): boolean {
  const el = target as HTMLElement | null;
  if (!el) return false;
  return el.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(el.tagName) || Boolean(el.closest?.('.cm-editor, .xterm'));
}

/** The reference sheet's keys for one shortcut; "not set" when unbound. */
export function keyCaps(keys: string): HTMLElement[] {
  if (!keys) return [h('span', { class: 'muted', text: 'not set' })];
  return displayKeys(keys).map((k) => h('kbd', { text: k }));
}

/**
 * Installs the shortcut listener and the reference sheet. Returns a cleanup.
 *
 * Keys the focused control already handled (the editor's own, a field's) are
 * left alone, so a shortcut never steals a key from what you are typing in.
 * A change of binding in Settings takes effect at once.
 */
export function installShortcuts(controller: AppController, root: HTMLElement): () => void {
  const sheet = h('dialog', { class: 'shortcuts-dialog', 'aria-label': 'Keyboard shortcuts' });
  root.append(sheet);
  const openSheet = (): void => {
    if (sheet.open) {
      sheet.close();
      return;
    }
    renderSheet();
    sheet.showModal();
  };
  const defaults = appShortcuts(controller, openSheet);
  let shortcuts = boundShortcuts(defaults);
  let parsed = shortcuts.filter((s) => s.keys).map((s) => ({ shortcut: s, spec: parseKeys(s.keys) }));

  const rebind = (): void => {
    shortcuts = boundShortcuts(defaults);
    parsed = shortcuts.filter((s) => s.keys).map((s) => ({ shortcut: s, spec: parseKeys(s.keys) }));
    // The menu shows the same keys; it does not handle them itself.
    const menu: Record<string, string> = {};
    for (const s of shortcuts) if (MENU_IDS.includes(s.id)) menu[s.id] = toAccelerator(s.keys);
    void window.aisai.setMenuShortcuts?.(menu);
  };
  rebind();
  const offBindings = useKeybindingStore.subscribe(rebind);

  const renderSheet = (): void => {
    const groups = new Map<string, Shortcut[]>();
    for (const s of shortcuts) groups.set(s.group, [...(groups.get(s.group) ?? []), s]);
    const close = h('button', { class: 'ghost tiny', type: 'button', onclick: () => sheet.close() }, 'Close');
    const customise = h('button', {
      class: 'tiny',
      type: 'button',
      onclick: () => {
        sheet.close();
        useUiStore.getState().setSettingsSection('keyboard');
        useUiStore.getState().setSettingsOpen(true);
      },
    }, 'Change shortcuts…');
    sheet.replaceChildren(
      h('div', { class: 'shortcuts-head' }, h('h2', { text: 'Keyboard shortcuts' }), h('span', { class: 'grow' }), customise, close),
      ...Array.from(groups, ([group, list]) =>
        h(
          'section',
          { class: 'shortcuts-group' },
          h('h3', { text: group }),
          h('dl', {}, ...list.flatMap((s) => [h('dt', {}, ...keyCaps(s.keys)), h('dd', { text: s.label })])),
        ),
      ),
    );
  };

  globalShortcut = (event) => parsed.some(({ shortcut, spec }) => shortcut.run && shortcut.inFields && matches(spec, event));

  const onKeyDown = (event: KeyboardEvent): void => {
    if (event.defaultPrevented || event.repeat) return;
    // The Keyboard section of Settings is recording a new binding.
    if (document.body.classList.contains('is-recording-keys')) return;
    const typing = inTextField(event.target);
    for (const { shortcut, spec } of parsed) {
      if (!shortcut.run || !matches(spec, event)) continue;
      if (typing && !shortcut.inFields) return;
      event.preventDefault();
      shortcut.run();
      return;
    }
  };

  // Menu items the main process sends when they are clicked.
  const offMenu = window.aisai.onMenuCommand((command) => {
    if (command === 'save') editorCommand('save');
    else if (command === 'undo' || command === 'redo') undoOrRedo(command);
    else if (command === 'shortcuts') openSheet();
  });

  // The bubbling phase, so the editor and fields see their keys first.
  document.addEventListener('keydown', onKeyDown);
  window.addEventListener('aisai:show-shortcuts', openSheet);
  return () => {
    document.removeEventListener('keydown', onKeyDown);
    window.removeEventListener('aisai:show-shortcuts', openSheet);
    offBindings();
    offMenu();
    sheet.remove();
    globalShortcut = () => false;
  };
}
