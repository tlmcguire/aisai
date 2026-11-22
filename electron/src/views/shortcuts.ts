import type { AppController } from '../app';
import { h } from '../lib/dom';
import { displayKeys, matches, parseKeys, type Shortcut } from '../lib/shortcuts';
import { useAuditStore } from '../state/audit';
import { useChatStore } from '../state/chat';
import { useUiStore } from '../state/ui';

/** The app's shortcuts. The order is the order of the reference sheet. */
export function appShortcuts(controller: AppController, openSheet: () => void): Shortcut[] {
  const ui = () => useUiStore.getState();
  const canScan = (): boolean => {
    const audit = useAuditStore.getState();
    return audit.current?.status === 'ready' && audit.busy === null;
  };
  return [
    { group: 'General', keys: 'Mod+,', label: 'Open Settings', inFields: true, run: () => ui().setSettingsOpen(true) },
    { group: 'General', keys: 'Mod+Shift+/', label: 'Show keyboard shortcuts', inFields: true, run: openSheet },
    { group: 'General', keys: 'Ctrl+`', label: 'Show or hide the terminal', inFields: true, run: () => ui().setTerminalOpen(!ui().terminalOpen) },

    { group: 'Debug', keys: 'F5', label: 'Debug the open Python file, or continue', inFields: true, run: () => window.dispatchEvent(new Event('aisai:debug-start')) },
    { group: 'Debug', keys: 'Shift+F5', label: 'Stop the program', inFields: true, run: () => void controller.stopDebug() },
    { group: 'Debug', keys: 'Mod+Shift+F5', label: 'Restart the program', inFields: true, run: () => void controller.restartDebug() },
    { group: 'Debug', keys: 'F6', label: 'Pause', inFields: true, run: () => controller.debugCommand('pause') },
    { group: 'Debug', keys: 'F10', label: 'Step over', inFields: true, run: () => controller.debugCommand('next') },
    { group: 'Debug', keys: 'F11', label: 'Step into', inFields: true, run: () => controller.debugCommand('step') },
    { group: 'Debug', keys: 'Shift+F11', label: 'Step out', inFields: true, run: () => controller.debugCommand('out') },
    { group: 'Editor', keys: 'Mod+Alt+K', label: 'Turn inline completion on or off', inFields: true, run: () => ui().setCompletionEnabled(!ui().completionEnabled) },
    { group: 'Audit', keys: 'Mod+L', label: 'Go to the package field (outside the editor)', run: () => document.getElementById('package-input')?.focus() },
    { group: 'Audit', keys: 'Mod+O', label: 'Open a project folder', inFields: true, run: () => void controller.chooseFolder() },
    { group: 'Audit', keys: 'Mod+Shift+S', label: 'Static scan', inFields: true, run: () => canScan() && void controller.runScan('sast') },
    { group: 'Audit', keys: 'Mod+Shift+L', label: 'LLM scan', inFields: true, run: () => canScan() && void controller.runScan('llm') },

    { group: 'View', keys: 'Mod+1', label: 'Code tab', inFields: true, run: () => ui().setActiveTab('code') },
    { group: 'View', keys: 'Mod+2', label: 'AST tab', inFields: true, run: () => ui().setActiveTab('ast') },
    { group: 'View', keys: 'Mod+3', label: 'Diff tab', inFields: true, run: () => ui().setActiveTab('diff') },
    { group: 'View', keys: 'Mod+B', label: 'Show or hide the dependencies pane', inFields: true, run: () => ui().setPaneHidden('deps', !ui().hiddenPanes.includes('deps')) },
    { group: 'View', keys: 'Mod+Alt+B', label: 'Show or hide the findings pane', inFields: true, run: () => ui().setPaneHidden('findings', !ui().hiddenPanes.includes('findings')) },

    {
      group: 'Chat',
      keys: 'Mod+J',
      label: 'Go to the chat',
      inFields: true,
      run: () => window.dispatchEvent(new Event('aisai:focus-chat')),
    },
    {
      group: 'Chat',
      keys: 'Mod+Shift+N',
      label: 'New chat',
      inFields: true,
      run: () => {
        useChatStore.getState().newThread();
        window.dispatchEvent(new Event('aisai:focus-chat'));
      },
    },

    { group: 'Editor', keys: 'Mod+S', label: 'Save the file' },
    { group: 'Editor', keys: 'F9', label: 'Set or remove a breakpoint on the cursor line' },
    { group: 'Editor', keys: 'Tab', label: 'Accept the suggested completion' },
    { group: 'Editor', keys: 'Escape', label: 'Dismiss the suggestion' },
    { group: 'Editor', keys: 'Mod+Z', label: 'Undo (also takes back an accepted change)' },
  ];
}

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

/**
 * Installs the shortcut listener and the reference sheet. Returns a cleanup.
 *
 * Keys the focused control already handled (the editor's own, a field's) are
 * left alone, so a shortcut never steals a key from what you are typing in.
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
  const shortcuts = appShortcuts(controller, openSheet);
  const parsed = shortcuts.map((s) => ({ shortcut: s, spec: parseKeys(s.keys) }));

  const renderSheet = (): void => {
    const groups = new Map<string, Shortcut[]>();
    for (const s of shortcuts) groups.set(s.group, [...(groups.get(s.group) ?? []), s]);
    const close = h('button', { class: 'ghost tiny', type: 'button', onclick: () => sheet.close() }, 'Close');
    sheet.replaceChildren(
      h('div', { class: 'shortcuts-head' }, h('h2', { text: 'Keyboard shortcuts' }), h('span', { class: 'grow' }), close),
      ...Array.from(groups, ([group, list]) =>
        h(
          'section',
          { class: 'shortcuts-group' },
          h('h3', { text: group }),
          h(
            'dl',
            {},
            ...list.flatMap((s) => [
              h('dt', {}, ...displayKeys(s.keys).map((k) => h('kbd', { text: k }))),
              h('dd', { text: s.label }),
            ]),
          ),
        ),
      ),
    );
  };

  globalShortcut = (event) => parsed.some(({ shortcut, spec }) => shortcut.run && shortcut.inFields && matches(spec, event));

  const onKeyDown = (event: KeyboardEvent): void => {
    if (event.defaultPrevented || event.repeat) return;
    const typing = inTextField(event.target);
    for (const { shortcut, spec } of parsed) {
      if (!shortcut.run || !matches(spec, event)) continue;
      if (typing && !shortcut.inFields) return;
      event.preventDefault();
      shortcut.run();
      return;
    }
  };
  // The bubbling phase, so the editor and fields see their keys first.
  document.addEventListener('keydown', onKeyDown);
  window.addEventListener('aisai:show-shortcuts', openSheet);
  return () => {
    document.removeEventListener('keydown', onKeyDown);
    window.removeEventListener('aisai:show-shortcuts', openSheet);
    sheet.remove();
    globalShortcut = () => false;
  };
}
