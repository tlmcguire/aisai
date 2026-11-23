/**
 * The bottom panel: the integrated terminal and the Debug view.
 *
 * Each terminal tab is a shell on the backend (a pseudo-terminal in the
 * audit's folder), drawn with xterm.js (https://xtermjs.org/docs/) and sized
 * with its fit addon. Keystrokes go to the backend in order; output comes
 * back as terminal.data events through terminalBus, which bypasses the
 * store because it is many small chunks a second.
 */

import { FitAddon } from '@xterm/addon-fit';
import { Terminal } from '@xterm/xterm';
import '@xterm/xterm/css/xterm.css';

import type { AppController } from '../app';
import { h } from '../lib/dom';
import { icon } from '../lib/icons';
import { t } from '../lib/i18n';
import { terminalBus, useTerminalStore } from '../state/terminal';
import { DEFAULT_LAYOUT, useUiStore, type BottomView } from '../state/ui';
import { useDebugStore } from '../state/debug';
import { createDebugPanel } from './debugPanel';
import { usePrefsStore } from '../state/prefs';
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
    { class: 'ghost tiny icon-button', type: 'button', title: t('terminal.newTerminal'), 'aria-label': t('terminal.newTerminal') },
    icon('plus'),
  );
  const growButton = h('button', { class: 'ghost tiny icon-button', type: 'button' }, icon('arrow-up'));
  const syncGrow = (): void => {
    const big = useUiStore.getState().terminalHeight >= 440;
    growButton.replaceChildren(icon(big ? 'arrow-down' : 'arrow-up'));
    const label = big ? t('terminal.shrinkTerminal') : t('terminal.enlargeTerminal');
    growButton.title = label;
    growButton.setAttribute('aria-label', label);
  };
  growButton.addEventListener('click', () => {
    const ui = useUiStore.getState();
    ui.setTerminalHeight(ui.terminalHeight >= 440 ? DEFAULT_LAYOUT.terminalHeight : 520);
  });
  useUiStore.subscribe(syncGrow);
  syncGrow();
  const hideButton = h(
    'button',
    { class: 'ghost tiny icon-button', type: 'button', title: t('terminal.hideTerminalTitle'), 'aria-label': t('terminal.hideTerminal') },
    icon('chevron-down'),
  );
  // The panel shows the shells or the debugger.
  const viewButton = (view: BottomView, label: string): HTMLButtonElement => {
    const button = h('button', { class: 'bottom-view', type: 'button', role: 'tab', text: label });
    button.addEventListener('click', () => useUiStore.getState().setBottomView(view));
    return button;
  };
  const terminalView = viewButton('terminal', t('terminal.terminalView'));
  const debugView = viewButton('debug', t('terminal.debugView'));
  const debugDot = h('span', { class: 'bottom-view-dot', hidden: true, 'aria-hidden': 'true' });
  debugView.append(debugDot);
  const header = h(
    'div',
    { class: 'terminal-header' },
    h('span', { class: 'bottom-views', role: 'tablist', 'aria-label': t('terminal.bottomPanel') }, terminalView, debugView),
    tabs,
    newButton,
    h('span', { class: 'grow' }),
    growButton,
    hideButton,
  );
  const screens = h('div', { class: 'terminal-screens' });
  const notice = h('div', { class: 'terminal-notice', hidden: true, role: 'status' });
  const debugPanel = createDebugPanel(controller);
  container.append(header, notice, screens, debugPanel.element);

  const showView = (): void => {
    const view = useUiStore.getState().bottomView;
    const debugging = view === 'debug';
    terminalView.classList.toggle('is-active', !debugging);
    terminalView.setAttribute('aria-selected', String(!debugging));
    debugView.classList.toggle('is-active', debugging);
    debugView.setAttribute('aria-selected', String(debugging));
    tabs.hidden = debugging;
    newButton.hidden = debugging;
    screens.hidden = debugging;
    debugPanel.element.hidden = !debugging;
    if (debugging) notice.hidden = true;
  };
  const syncDot = (): void => {
    const status = useDebugStore.getState().status;
    debugDot.hidden = status !== 'running' && status !== 'paused';
    debugDot.classList.toggle('is-paused', status === 'paused');
  };
  const offDebug = useDebugStore.subscribe(syncDot);
  syncDot();

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
      cursorBlink: usePrefsStore.getState().cursorBlink,
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

    notice.hidden = !state.error || useUiStore.getState().bottomView === 'debug';
    notice.textContent = state.error ? t('terminal.couldNotStart', { error: state.error }) : '';

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
            session.exited !== undefined ? t('terminal.exited', { title: session.title, code: session.exited }) : session.title,
          ),
          h(
            'button',
            {
              class: 'ghost tiny icon-button',
              type: 'button',
              title: t('terminal.closeTerminal'),
              'aria-label': t('terminal.closeTerminalNamed', { title: session.title }),
              onclick: () => void controller.closeTerminal(session.id),
            },
            icon('x'),
          ),
        ),
      ),
    );

    for (const [id, entry] of live) entry.host.hidden = id !== state.activeId;
    const active = state.activeId ? live.get(state.activeId) : undefined;
    if (active && open && useUiStore.getState().bottomView === 'terminal') {
      // The size is measured once the panel has been laid out.
      requestAnimationFrame(() => {
        sizeOf(active);
        active.term.focus();
      });
    }
  };

  /** Starts the first shell when the panel is opened with none running. */
  const ensureShell = (): void => {
    if (!useUiStore.getState().terminalOpen || useUiStore.getState().bottomView !== 'terminal') return;
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

  // The shortcut for a new terminal: the panel is opened on the terminal and
  // a shell is started in it, even when other shells are already running.
  const onNewTerminal = (): void => {
    const ui = useUiStore.getState();
    const wasEmpty = useTerminalStore.getState().sessions.length === 0;
    const wasShown = ui.terminalOpen && ui.bottomView === 'terminal';
    ui.setBottomView('terminal');
    ui.setTerminalOpen(true);
    // Showing an empty panel starts its first shell by itself.
    if (wasShown || !wasEmpty) void spawn();
  };
  window.addEventListener('aisai:new-terminal', onNewTerminal);

  const observer = new ResizeObserver(() => {
    const active = live.get(useTerminalStore.getState().activeId ?? '');
    if (active && useUiStore.getState().terminalOpen) sizeOf(active);
  });
  observer.observe(screens);

  const unsubscribeTerm = useTerminalStore.subscribe(() => render());
  const unsubscribeUi = useUiStore.subscribe((state, previous) => {
    if (state.bottomView !== previous.bottomView) {
      showView();
      render();
      ensureShell();
    }
    if (state.terminalOpen !== previous.terminalOpen) {
      render();
      ensureShell();
    }
  });

  showView();
  render();

  return {
    element: container,
    dispose: () => {
      window.removeEventListener('aisai:new-terminal', onNewTerminal);
      observer.disconnect();
      offDebug();
      debugPanel.dispose();
      unsubscribeTerm();
      unsubscribeUi();
      for (const id of Array.from(live.keys())) detach(id);
    },
  };
}
