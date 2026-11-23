import type { AppController } from '../app';
import { h } from '../lib/dom';
import { t } from '../lib/i18n';
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
    { id: 'app.settings', group: t('shortcuts.groupGeneral'), keys: 'Mod+,', label: t('shortcuts.openSettings'), inFields: true, run: () => ui().setSettingsOpen(true) },
    { id: 'app.shortcuts', group: t('shortcuts.groupGeneral'), keys: 'Mod+Shift+/', label: t('shortcuts.showKeyboardShortcuts'), inFields: true, run: openSheet },
    { id: 'app.terminal', group: t('shortcuts.groupGeneral'), keys: 'Ctrl+`', label: t('shortcuts.toggleTerminal'), inFields: true, run: () => ui().setTerminalOpen(!ui().terminalOpen) },
    { id: 'app.newTerminal', group: t('shortcuts.groupGeneral'), keys: 'Ctrl+Shift+`', label: t('shortcuts.newTerminal'), inFields: true, run: () => window.dispatchEvent(new Event('aisai:new-terminal')) },
    { id: 'app.tutorials', group: t('shortcuts.groupGeneral'), keys: '', label: t('shortcuts.openTutorials'), inFields: true, run: () => window.dispatchEvent(new Event('aisai:tutorials')) },

    { id: 'file.save', group: t('shortcuts.groupFile'), keys: 'Mod+S', label: t('shortcuts.saveFile'), inFields: true, run: () => editorCommand('save') },
    { id: 'file.open-folder', group: t('shortcuts.groupFile'), keys: 'Mod+O', label: t('shortcuts.openFolder'), inFields: true, run: () => void controller.chooseFolder() },
    { id: 'file.export-pdf', group: t('shortcuts.groupFile'), keys: 'Mod+Shift+E', label: t('shortcuts.exportPdf'), inFields: true, run: () => window.dispatchEvent(new CustomEvent('aisai:export-report', { detail: 'pdf' })) },

    { id: 'edit.undo', group: t('shortcuts.groupEdit'), keys: 'Mod+Z', label: t('shortcuts.undo'), inFields: true, run: () => undoOrRedo('undo') },
    { id: 'edit.redo', group: t('shortcuts.groupEdit'), keys: REDO_KEYS, label: t('shortcuts.redo'), inFields: true, run: () => undoOrRedo('redo') },
    { id: 'edit.copy', group: t('shortcuts.groupEdit'), keys: 'Mod+C', label: t('shortcuts.copy'), fixed: true },
    { id: 'edit.paste', group: t('shortcuts.groupEdit'), keys: 'Mod+V', label: t('shortcuts.paste'), fixed: true },
    { id: 'edit.select-all', group: t('shortcuts.groupEdit'), keys: 'Mod+A', label: t('shortcuts.selectAll'), fixed: true },

    { id: 'editor.inlineChat', group: t('shortcuts.groupEditor'), keys: 'Mod+K', label: t('shortcuts.inlineChat') },
    { id: 'editor.breakpoint', group: t('shortcuts.groupEditor'), keys: 'F9', label: t('shortcuts.toggleBreakpoint') },
    { id: 'editor.completion', group: t('shortcuts.groupEditor'), keys: 'Mod+Alt+K', label: t('shortcuts.toggleCompletion'), inFields: true, run: () => ui().setCompletionEnabled(!ui().completionEnabled) },
    { id: 'editor.accept', group: t('shortcuts.groupEditor'), keys: 'Tab', label: t('shortcuts.acceptCompletion'), fixed: true },
    { id: 'editor.dismiss', group: t('shortcuts.groupEditor'), keys: 'Escape', label: t('shortcuts.dismissSuggestion'), fixed: true },

    { id: 'debug.start', group: t('shortcuts.groupDebug'), keys: 'F5', label: t('shortcuts.debugStart'), inFields: true, run: () => window.dispatchEvent(new Event('aisai:debug-start')) },
    { id: 'debug.stop', group: t('shortcuts.groupDebug'), keys: 'Shift+F5', label: t('shortcuts.debugStop'), inFields: true, run: () => void controller.stopDebug() },
    { id: 'debug.restart', group: t('shortcuts.groupDebug'), keys: 'Mod+Shift+F5', label: t('shortcuts.debugRestart'), inFields: true, run: () => void controller.restartDebug() },
    { id: 'debug.pause', group: t('shortcuts.groupDebug'), keys: 'F6', label: t('shortcuts.pause'), inFields: true, run: () => controller.debugCommand('pause') },
    { id: 'debug.stepOver', group: t('shortcuts.groupDebug'), keys: 'F10', label: t('shortcuts.stepOver'), inFields: true, run: () => controller.debugCommand('next') },
    { id: 'debug.stepInto', group: t('shortcuts.groupDebug'), keys: 'F11', label: t('shortcuts.stepInto'), inFields: true, run: () => controller.debugCommand('step') },
    { id: 'debug.stepOut', group: t('shortcuts.groupDebug'), keys: 'Shift+F11', label: t('shortcuts.stepOut'), inFields: true, run: () => controller.debugCommand('out') },
    { id: 'debug.runToCursor', group: t('shortcuts.groupDebug'), keys: 'Mod+F10', label: t('shortcuts.runToCursor'), inFields: true, run: () => window.dispatchEvent(new Event('aisai:run-to-cursor')) },

    { id: 'audit.package', group: t('shortcuts.groupAudit'), keys: 'Mod+L', label: t('shortcuts.goToPackageField'), run: () => document.getElementById('package-input')?.focus() },
    { id: 'audit.scanFile', group: t('shortcuts.groupAudit'), keys: 'Mod+Alt+S', label: t('shortcuts.scanOpenFile'), inFields: true, run: () => window.dispatchEvent(new Event('aisai:scan-file')) },
    { id: 'audit.static', group: t('shortcuts.groupAudit'), keys: 'Mod+Shift+S', label: t('shortcuts.staticScan'), inFields: true, run: () => canScan() && void controller.runScan('sast') },
    { id: 'audit.llm', group: t('shortcuts.groupAudit'), keys: 'Mod+Shift+L', label: t('shortcuts.llmScan'), inFields: true, run: () => canScan() && void controller.runScan('llm') },

    { id: 'view.code', group: t('shortcuts.groupView'), keys: 'Mod+1', label: t('shortcuts.codeTab'), inFields: true, run: () => ui().setActiveTab('code') },
    { id: 'view.ast', group: t('shortcuts.groupView'), keys: 'Mod+2', label: t('shortcuts.astTab'), inFields: true, run: () => ui().setActiveTab('ast') },
    { id: 'view.diff', group: t('shortcuts.groupView'), keys: 'Mod+3', label: t('shortcuts.diffTab'), inFields: true, run: () => ui().setActiveTab('diff') },
    { id: 'view.deps', group: t('shortcuts.groupView'), keys: 'Mod+B', label: t('shortcuts.toggleDependenciesPane'), inFields: true, run: () => ui().setPaneHidden('deps', !ui().hiddenPanes.includes('deps')) },
    { id: 'view.findings', group: t('shortcuts.groupView'), keys: 'Mod+Alt+B', label: t('shortcuts.toggleFindingsPane'), inFields: true, run: () => ui().setPaneHidden('findings', !ui().hiddenPanes.includes('findings')) },

    { id: 'chat.focus', group: t('shortcuts.groupChat'), keys: 'Mod+J', label: t('shortcuts.goToChat'), inFields: true, run: () => window.dispatchEvent(new Event('aisai:focus-chat')) },
    {
      id: 'chat.new',
      group: t('shortcuts.groupChat'),
      keys: 'Mod+Shift+N',
      label: t('shortcuts.newChat'),
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
  if (!keys) return [h('span', { class: 'muted', text: t('shortcuts.notSet') })];
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
  const sheet = h('dialog', { class: 'shortcuts-dialog', 'aria-label': t('shortcuts.sheetAria') });
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
    // The name of a group is translated, and every call of t() gives a new
    // object, so a map keyed by the name itself would make a group of its
    // own for every shortcut. The text of the name is the same for the whole
    // group, so the map is keyed by that.
    const groups = new Map<string, Shortcut[]>();
    const names = new Map<string, string>();
    for (const s of shortcuts) {
      const key = String(s.group);
      groups.set(key, [...(groups.get(key) ?? []), s]);
      if (!names.has(key)) names.set(key, s.group);
    }
    const close = h('button', { class: 'ghost tiny', type: 'button', onclick: () => sheet.close() }, t('shortcuts.close'));
    const customise = h('button', {
      class: 'tiny',
      type: 'button',
      onclick: () => {
        sheet.close();
        useUiStore.getState().setSettingsSection('keyboard');
        useUiStore.getState().setSettingsOpen(true);
      },
    }, t('shortcuts.changeShortcuts'));
    sheet.replaceChildren(
      h('div', { class: 'shortcuts-head' }, h('h2', { text: t('shortcuts.sheetTitle') }), h('span', { class: 'grow' }), customise, close),
      ...Array.from(groups, ([group, list]) =>
        h(
          'section',
          { class: 'shortcuts-group' },
          h('h3', { text: names.get(group) ?? group }),
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
