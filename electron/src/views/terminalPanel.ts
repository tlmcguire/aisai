import { FitAddon } from '@xterm/addon-fit';
import { Terminal } from '@xterm/xterm';
import '@xterm/xterm/css/xterm.css';

import type { AppController } from '../app';
import { h } from '../lib/dom';
import { icon } from '../lib/icons';
import { terminalBus, useTerminalStore } from '../state/terminal';
import { useUiStore } from '../state/ui';
import { isGlobalShortcut } from './shortcuts';

export interface TerminalPanel {
  element: HTMLElement;
  dispose: () => void;
}

/** The colours of the terminal, taken from the app's theme. */
function terminalTheme(): Record<string, string> {
  const css = getComputedStyle(document.documentElement);
  const read = (name: string, fallback: string): string => css.getPropertyValue(name).trim() || fallback;
  return {
    background: read('--bg-base', '#0a1112'),
    foreground: read('--fg', '#dde8e3'),
    cursor: read('--accent', '#c8f562'),
    selectionBackground: 'rgba(200, 245, 98, 0.28)',
  };
}

/**
 * The terminal panel: a shell at the bottom of the window, like an editor's.
 *
 * Each tab is one shell on the backend (a pseudo-terminal in the folder of the
 * audit). xterm.js draws it; the keys go to the backend and the output comes
 * back as events. Nothing here runs a command by itself: the shell starts when
 * the panel is opened and only the person typing sends it input.
 */
export function createTerminalPanel(controller: AppController, container: HTMLElement): TerminalPanel {
  const tabs = h('div', { class: 'terminal-tabs', role: 'tablist' });
  const newButton = h(
    'button',
    { class: 'ghost tiny icon-button', type: 'button', title: 'New terminal', 'aria-label': 'New terminal' },
    icon('plus'),
  );
  const hideButton = h(
    'button',
    { class: 'ghost tiny icon-button', type: 'button', title: 'Hide the terminal (Ctrl+`)', 'aria-label': 'Hide the terminal' },
    icon('chevron-down'),
  );
  const header = h(
    'div',
    { class: 'terminal-header' },
    h('span', { class: 'terminal-title', text: 'Terminal' }),
    tabs,
    newButton,
    h('span', { class: 'grow' }),
    hideButton,
  );
  const screens = h('div', { class: 'terminal-screens' });
  const notice = h('div', { class: 'terminal-notice', hidden: true, role: 'status' });
  container.append(header, notice, screens);

  interface Live {
    term: Terminal;
    fit: FitAddon;
    host: HTMLElement;
    stop: () => void;
  }
  const live = new Map<string, Live>();

  const sizeOf = (entry: Live): void => {
    try {
      entry.fit.fit();
    } catch {
      /* the panel is hidden: there is nothing to measure */
    }
  };

  const attach = (id: string): void => {
    if (live.has(id)) return;
    const host = h('div', { class: 'terminal-screen', hidden: true });
    screens.append(host);
    const term = new Terminal({
      fontFamily: 'JetBrains Mono Variable, ui-monospace, monospace',
      fontSize: 13,
      cursorBlink: true,
      theme: terminalTheme(),
      scrollback: 5000,
      allowProposedApi: false,
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(host);
    // The app's own shortcuts (Ctrl+` and the like) reach the app, not the shell.
    term.attachCustomKeyEventHandler((event) => !isGlobalShortcut(event));
    term.onData((data) => controller.terminalInput(id, data));
    term.onResize(({ cols, rows }) => controller.terminalResize(id, cols, rows));
    const stop = terminalBus.listen(id, (bytes) => term.write(bytes));
    live.set(id, { term, fit, host, stop });
  };

  const detach = (id: string): void => {
    const entry = live.get(id);
    if (!entry) return;
    entry.stop();
    entry.term.dispose();
    entry.host.remove();
    live.delete(id);
  };

  const render = (): void => {
    const state = useTerminalStore.getState();
    const open = useUiStore.getState().terminalOpen;

    notice.hidden = !state.error;
    notice.textContent = state.error ? `The terminal could not start: ${state.error}` : '';

    for (const session of state.sessions) attach(session.id);
    for (const id of Array.from(live.keys())) {
      if (!state.sessions.some((s) => s.id === id)) detach(id);
    }

    tabs.replaceChildren(
      ...state.sessions.map((session) =>
        h(
          'div',
          { class: `terminal-tab${session.id === state.activeId ? ' is-active' : ''}${session.exited !== undefined ? ' is-exited' : ''}` },
          h(
            'button',
            {
              class: 'terminal-tab-name',
              type: 'button',
              role: 'tab',
              'aria-selected': String(session.id === state.activeId),
              title: session.cwd,
              onclick: () => useTerminalStore.getState().select(session.id),
            },
            session.exited !== undefined ? `${session.title} (exited ${session.exited})` : session.title,
          ),
          h(
            'button',
            {
              class: 'ghost tiny icon-button',
              type: 'button',
              title: 'Close this terminal',
              'aria-label': `Close ${session.title}`,
              onclick: () => void controller.closeTerminal(session.id),
            },
            icon('x'),
          ),
        ),
      ),
    );

    for (const [id, entry] of live) entry.host.hidden = id !== state.activeId;
    const active = state.activeId ? live.get(state.activeId) : undefined;
    if (active && open) {
      // The size is measured once the panel has been laid out.
      requestAnimationFrame(() => {
        sizeOf(active);
        active.term.focus();
      });
    }
  };

  /** Starts the first shell when the panel is opened with none running. */
  const ensureShell = (): void => {
    if (!useUiStore.getState().terminalOpen) return;
    const state = useTerminalStore.getState();
    if (state.sessions.length === 0) void spawn();
  };

  const spawn = async (): Promise<void> => {
    const probe = live.get(useTerminalStore.getState().activeId ?? '');
    const cols = probe?.term.cols ?? 100;
    const rows = probe?.term.rows ?? 14;
    await controller.openTerminal(cols, rows);
  };

  newButton.addEventListener('click', () => void spawn());
  hideButton.addEventListener('click', () => useUiStore.getState().setTerminalOpen(false));

  const observer = new ResizeObserver(() => {
    const active = live.get(useTerminalStore.getState().activeId ?? '');
    if (active && useUiStore.getState().terminalOpen) sizeOf(active);
  });
  observer.observe(screens);

  const unsubscribeTerm = useTerminalStore.subscribe(() => render());
  const unsubscribeUi = useUiStore.subscribe((state, previous) => {
    if (state.terminalOpen !== previous.terminalOpen) {
      render();
      ensureShell();
    }
  });

  render();

  return {
    element: container,
    dispose: () => {
      observer.disconnect();
      unsubscribeTerm();
      unsubscribeUi();
      for (const id of Array.from(live.keys())) detach(id);
    },
  };
}
