/**
 * The window's layout: toolbar, status and download strips, the three panes
 * (dependencies, main, findings) in the user's order, the terminal and the
 * chat, and the dividers between them.
 *
 * Divider drags are throttled to one update per animation frame
 * (rafThrottle), and transitions are switched off while the window itself
 * is being resized, so nothing wobbles. Sizes are stored in reference
 * pixels and applied in rem, so the whole interface scales with the
 * Interface size setting. It also installs the shortcuts and tutorials and
 * routes application menu commands.
 */

import type { AppController } from '../app';
import { h } from '../lib/dom';
import { icon } from '../lib/icons';
import { formatBytes } from '../lib/format';
import { t, tn } from '../lib/i18n';
import { useAuditStore } from '../state/audit';
import { DEFAULT_LAYOUT, paneName, useUiStore, type MainTab, type PaneId } from '../state/ui';
import { createAstView } from './astView';
import { createChatPanel } from './chatPanel';
import { createCodeView } from './codeView';
import { createDiffView } from './diffView';
import { createFileTree } from './fileTree';
import { createFindingsRail } from './findingsRail';
import { createGraphView } from './graphView';
import { createGraphWindow } from './graphWindow';
import { createSettingsDialog } from './settingsDialog';
import { progressStats } from '../lib/progress';
import { createTerminalPanel } from './terminalPanel';
import { installShortcuts } from './shortcuts';
import { installTutorials } from './tutorials';
import { createToolbar } from './toolbar';

const TABS: Array<{ id: MainTab; label: () => string }> = [
  { id: 'code', label: () => t('layout.tabCode') },
  { id: 'ast', label: () => t('layout.tabAst') },
  { id: 'diff', label: () => t('layout.tabDiff') },
];

export interface Layout {
  dispose: () => void;
}

/** The factor between the reference 16px and the current root font size. */
/**
 * Runs a pointer handler at most once a frame, with the latest event. A drag
 * then moves the layout in step with the display instead of in bursts.
 */
function rafThrottle<T>(fn: (arg: T) => void): (arg: T) => void {
  let pending: T | null = null;
  let frame = 0;
  return (arg: T) => {
    pending = arg;
    if (frame) return;
    frame = requestAnimationFrame(() => {
      frame = 0;
      if (pending !== null) fn(pending);
    });
  };
}

// While the window itself is being resized, the size transitions are off:
// easing behind a moving window edge is what makes the layout wobble.
let windowResizeTimer = 0;
window.addEventListener('resize', () => {
  document.body.classList.add('is-window-resizing');
  window.clearTimeout(windowResizeTimer);
  windowResizeTimer = window.setTimeout(() => document.body.classList.remove('is-window-resizing'), 180);
});

const uiScale = (): number => parseFloat(getComputedStyle(document.documentElement).fontSize) / 16;

export function createLayout(controller: AppController, root: HTMLElement): Layout {
  /* ---------------- toolbar ---------------- */
  const toolbar = h('header', { class: 'toolbar' });
  const toolbarView = createToolbar(controller, toolbar);

  /* ---------------- status strip ---------------- */
  const statusText = h('span', { class: 'legend-item', text: '' });
  const statsText = h('span', { class: 'legend-item progress-stats', text: '' });
  const phaseLabel = h('span', { class: 'progress-phase', text: '' });
  const fill = h('div', { class: 'progress-fill', style: 'width:0%' });
  const track = h('div', { class: 'progress-track', role: 'progressbar' }, fill);
  const cancelButton = h('button', { class: 'tiny', type: 'button' }, t('layout.hide'));
  const downloadsToggle = h(
    'button',
    { class: 'tiny ghost downloads-toggle', type: 'button', 'aria-expanded': 'true', hidden: true },
    '',
  );
  const downloadList = h('ol', { class: 'download-list', 'aria-label': t('layout.packageDownloads') });
  // A failed audit or scan says so, in words, until it is dismissed. A full
  // disk gets a button to the place where space can be freed.
  const errorText = h('span', { class: 'error-banner-text' });
  const errorAction = h('button', { class: 'tiny', type: 'button', hidden: true }, t('layout.freeUpSpace'));
  const errorDismiss = h('button', { class: 'tiny ghost', type: 'button', 'aria-label': t('layout.dismissMessage') }, t('layout.dismiss'));
  const errorBanner = h('div', { class: 'error-banner', role: 'alert', hidden: true }, errorText, h('span', { class: 'grow' }), errorAction, errorDismiss);
  errorDismiss.addEventListener('click', () => useAuditStore.getState().setError(null));
  errorAction.addEventListener('click', () => {
    useUiStore.getState().setSettingsSection('sources');
    useUiStore.getState().setSettingsOpen(true);
  });
  const downloadPanel = h('div', { class: 'download-panel', hidden: true }, downloadList);
  const statusStrip = h(
    'div',
    { class: 'status-strip', hidden: true, role: 'status' },
    phaseLabel,
    track,
    statusText,
    statsText,
    h('span', { class: 'grow' }),
    downloadsToggle,
    cancelButton,
  );
  let downloadsOpen = true;
  downloadsToggle.addEventListener('click', () => {
    downloadsOpen = !downloadsOpen;
    downloadsToggle.setAttribute('aria-expanded', String(downloadsOpen));
    renderDownloads();
  });

  // One row per package, reused across events so the list never flashes.
  const downloadRows = new Map<string, HTMLElement>();
  const STATE_LABEL: Record<string, string> = {
    downloading: t('layout.stateDownloading'),
    done: t('layout.stateFetched'),
    warning: t('layout.stateFetchedWithWarnings'),
    failed: t('layout.stateSkipped'),
  };
  const renderDownloads = (): void => {
    const audit = useAuditStore.getState();
    const entries = Object.values(audit.downloads).sort((a, b) => a.order - b.order);
    const running = audit.busy === 'audit' && audit.progressVisible;
    const finished = entries.filter((e) => e.state !== 'downloading').length;
    downloadsToggle.hidden = entries.length === 0;
    const total = Math.max(audit.downloadTotal, entries.length);
    downloadsToggle.replaceChildren(
      tn('layout.downloadProgress', total, { done: finished }),
      icon(downloadsOpen ? 'chevron-up' : 'chevron-down'),
    );
    downloadPanel.hidden = !running || !downloadsOpen || entries.length === 0;
    if (!entries.length) {
      downloadRows.clear();
      downloadList.replaceChildren();
      return;
    }
    for (const entry of entries) {
      const key = `${entry.package}@${entry.version}`;
      let row = downloadRows.get(key);
      if (!row) {
        row = h(
          'li',
          { class: 'download-row' },
          h('span', { class: 'download-icon', 'aria-hidden': 'true' }),
          h('span', { class: 'download-name' }),
          h('span', { class: 'download-version' }),
          h('span', { class: 'download-state' }),
        );
        downloadRows.set(key, row);
        downloadList.append(row);
      }
      row.dataset.state = entry.state;
      row.title = entry.detail;
      (row.children[1] as HTMLElement).textContent = entry.package;
      (row.children[2] as HTMLElement).textContent = entry.version;
      (row.children[2] as HTMLElement).textContent = entry.bytes ? `${entry.version} · ${formatBytes(entry.bytes)}` : entry.version;
      const stateLabel = STATE_LABEL[entry.state];
      (row.children[3] as HTMLElement).textContent =
        entry.detail && entry.state !== 'done' ? t('layout.stateDetail', { state: stateLabel, detail: entry.detail }) : stateLabel;
    }
  };

  cancelButton.addEventListener('click', () => {
    const audit = useAuditStore.getState();
    audit.setProgressVisible(false);
    audit.setBusy(null);
  });

  /* ---------------- panes ---------------- */
  const graphPane = h('section', { class: 'pane pane-graph', 'aria-label': t('layout.dependencyGraph') });
  const graphHeader = h(
    'div',
    { class: 'pane-header' },
    h('span', { text: t('layout.dependencies') }),
    h('span', { class: 'grow' }),
  );
  const graphBody = h('div', { class: 'pane-body graph-section' });
  graphPane.append(graphHeader, graphBody);

  const filesPane = h('div', { class: 'files-section' });
  const sourcesBadge = h('span', { class: 'badge neutral', text: '' });
  const deleteSourcesButton = h(
    'button',
    { class: 'tiny', type: 'button', title: t('layout.deleteSourcesTitle') },
    t('layout.deleteSources'),
  );
  const filesHeader = h(
    'div',
    { class: 'pane-header' },
    h('span', { text: t('layout.files') }),
    sourcesBadge,
    h('span', { class: 'grow' }),
    deleteSourcesButton,
  );
  const filesBody = h('div', { class: 'files-body' });
  filesPane.append(filesHeader, filesBody);

  const filesResizer = h('div', {
    class: 'resizer resizer-row',
    role: 'separator',
    tabindex: '0',
    'aria-orientation': 'horizontal',
    'aria-label': t('layout.resizeGraphAndFiles'),
  });
  graphPane.append(filesResizer, filesPane);

  /* ---------------- main pane ---------------- */
  const tabBar = h('div', { class: 'tabs', role: 'tablist' });
  const codePanel = h('div', { class: 'tab-panel', role: 'tabpanel' });
  const astPanel = h('div', { class: 'tab-panel', role: 'tabpanel', hidden: true });
  const diffPanel = h('div', { class: 'tab-panel', role: 'tabpanel', hidden: true });
  const mainPane = h('section', { class: 'pane pane-main', 'aria-label': t('layout.codeAstDiff') });
  mainPane.append(tabBar, codePanel, astPanel, diffPanel);

  const tabButtons = TABS.map((tab) =>
    h(
      'button',
      {
        class: 'tab',
        type: 'button',
        role: 'tab',
        'aria-selected': String(tab.id === 'code'),
        onclick: () => useUiStore.getState().setActiveTab(tab.id),
      },
      tab.label(),
    ),
  );
  const indicator = h('span', { class: 'tab-indicator', 'aria-hidden': 'true' });
  tabBar.append(...tabButtons, indicator);

  // The indicator slides under the selected tab.
  const placeIndicator = (): void => {
    const active = tabButtons.find((button) => button.getAttribute('aria-selected') === 'true');
    if (!active) return;
    tabBar.style.setProperty('--tab-x', `${active.offsetLeft}px`);
    tabBar.style.setProperty('--tab-w', `${active.offsetWidth}px`);
  };
  const resizeObserver = new ResizeObserver(placeIndicator);
  resizeObserver.observe(tabBar);

  /* ---------------- findings rail ---------------- */
  const railPane = h('section', { class: 'pane pane-rail', 'aria-label': t('layout.findings') });
  const railHeader = h(
    'div',
    { class: 'pane-header' },
    h('span', { text: t('layout.findings') }),
    h('span', { class: 'grow' }),
  );
  const railBody = h('div', { class: 'pane-body rail-body' });
  railPane.append(railHeader, railBody);

  /* ---------------- movable panes ---------------- */
  const shell = h('main', { class: 'shell' });
  const paneEls: Record<PaneId, HTMLElement> = { deps: graphPane, main: mainPane, findings: railPane };
  const PANE_MIME = 'application/x-aisai-pane';

  // Every pane gets a grip to drag it by and two arrows for the keyboard.
  const paneTools = (id: PaneId): HTMLElement => {
    const grip = h('span', {
      class: 'pane-grip',
      draggable: 'true',
      title: t('layout.dragPane', { pane: paneName(id) }),
      'aria-hidden': 'true',
      text: '⋮⋮',
    });
    grip.addEventListener('dragstart', (event) => {
      event.dataTransfer?.setData(PANE_MIME, id);
      if (event.dataTransfer) event.dataTransfer.effectAllowed = 'move';
      document.body.classList.add('is-moving-pane');
      paneEls[id].classList.add('is-dragged');
    });
    grip.addEventListener('dragend', () => {
      document.body.classList.remove('is-moving-pane');
      paneEls[id].classList.remove('is-dragged');
      clearDropMarks();
    });
    const arrow = (label: string, text: string, delta: -1 | 1): HTMLElement =>
      h('button', {
        class: 'ghost tiny pane-move',
        type: 'button',
        title: label,
        'aria-label': t('layout.movePane', { action: label, pane: paneName(id) }),
        text,
        onclick: () => useUiStore.getState().nudgePane(id, delta),
      });
    return h('span', { class: 'pane-tools' }, grip, arrow(t('layout.moveLeft'), '‹', -1), arrow(t('layout.moveRight'), '›', 1));
  };
  graphHeader.append(paneTools('deps'));
  railHeader.append(paneTools('findings'));
  tabBar.append(h('span', { class: 'grow' }), paneTools('main'));

  const clearDropMarks = (): void => {
    for (const el of Object.values(paneEls)) el.classList.remove('drop-before', 'drop-after');
  };
  for (const id of Object.keys(paneEls) as PaneId[]) {
    const el = paneEls[id];
    const side = (event: DragEvent): 'before' | 'after' => {
      const rect = el.getBoundingClientRect();
      return event.clientX < rect.left + rect.width / 2 ? 'before' : 'after';
    };
    el.addEventListener('dragover', (event) => {
      if (!event.dataTransfer?.types.includes(PANE_MIME)) return;
      event.preventDefault();
      event.dataTransfer.dropEffect = 'move';
      clearDropMarks();
      el.classList.add(side(event) === 'before' ? 'drop-before' : 'drop-after');
    });
    el.addEventListener('dragleave', (event) => {
      if (!el.contains(event.relatedTarget as Node | null)) el.classList.remove('drop-before', 'drop-after');
    });
    el.addEventListener('drop', (event) => {
      const dragged = event.dataTransfer?.getData(PANE_MIME) as PaneId | undefined;
      if (!dragged || dragged === id || !(dragged in paneEls)) return;
      event.preventDefault();
      const where = side(event);
      clearDropMarks();
      const rest = useUiStore.getState().paneOrder.filter((pane) => pane !== dragged);
      useUiStore.getState().movePane(dragged, rest.indexOf(id) + (where === 'after' ? 1 : 0));
    });
  }

  /* ---------------- chat ---------------- */
  const chatPanel = h('section', { class: 'chat-panel', 'aria-label': t('layout.chat') });

  const chatResizer = h('div', {
    class: 'resizer resizer-row resizer-chat',
    role: 'separator',
    tabindex: '0',
    'aria-orientation': 'horizontal',
    'aria-label': t('layout.resizeChat'),
  });

  // The terminal sits under the panes and above the chat, like an editor's.
  const terminalPanel = h('section', { class: 'terminal-panel', 'aria-label': t('layout.terminal'), hidden: true });
  const terminalResizer = h('div', {
    class: 'resizer resizer-row',
    role: 'separator',
    tabindex: '0',
    'aria-orientation': 'horizontal',
    'aria-label': t('layout.resizeTerminal'),
    hidden: true,
  });
  /**
   * A grab handle on a divider: a pill to drag, with two arrows that step the
   * panel bigger or smaller.
   *
   * The whole pill is a drag target. A press anywhere on it — the dots, an
   * arrow, the space between them — starts a drag of the divider, because the
   * arrows used to swallow the press, which left the handle looking grabbable
   * while only the invisible strip beside it really dragged. An arrow the
   * pointer never moves still steps the panel, so the handle can be hit at
   * any point: press and drag to resize, press and release on an arrow to
   * step. Dragging anywhere on the divider works too; the pill makes it
   * obvious, and the arrows help when a drag is hard (a trackpad, a tremor).
   *
   * The step is decided on pointerup rather than on click, because the
   * divider captures the pointer as soon as it is pressed, and that
   * suppresses the click the arrows would otherwise have listened for.
   */
  const grabHandle = (resizer: HTMLElement, name: string, step: (bigger: boolean) => void): (() => void) => {
    const bigger = h('button', { class: 'grab-arrow', type: 'button', tabindex: '-1' });
    const smaller = h('button', { class: 'grab-arrow', type: 'button', tabindex: '-1' });
    const pill = h('span', { class: 'grab-handle' }, bigger, h('span', { class: 'grab-dots', 'aria-hidden': 'true' }), smaller);
    // What the press started on, and whether it turned into a drag.
    let downOn: HTMLElement | null = null;
    let dragged = false;
    resizer.addEventListener('pointerdown', (event) => {
      downOn = event.target as HTMLElement;
      dragged = false;
      const origin = { x: event.clientX, y: event.clientY };
      const onMove = (move: PointerEvent): void => {
        if (Math.abs(move.clientX - origin.x) + Math.abs(move.clientY - origin.y) > 4) dragged = true;
      };
      resizer.addEventListener('pointermove', onMove);
      resizer.addEventListener('pointerup', () => resizer.removeEventListener('pointermove', onMove), { once: true });
    });
    resizer.addEventListener('pointerup', () => {
      if (!dragged) {
        // The press may have landed on the arrow's icon rather than the
        // button itself, so ask the button that owns it.
        const arrow = downOn?.closest('.grab-arrow');
        if (arrow === bigger) step(true);
        else if (arrow === smaller) step(false);
      }
      downOn = null;
      dragged = false;
    });
    for (const button of [bigger, smaller]) {
      // A double click on an arrow must not reset the divider.
      button.addEventListener('dblclick', (event) => event.stopPropagation());
    }
    resizer.append(pill);
    /** Points the arrows the way the panel grows: up, or left when docked at the side. */
    const sync = (sideways = false): void => {
      const label = name === 'chat' ? t('layout.chat') : t('layout.terminal');
      bigger.replaceChildren(icon(sideways ? 'chevron-left' : 'chevron-up'));
      smaller.replaceChildren(icon(sideways ? 'chevron-right' : 'chevron-down'));
      bigger.title = t('layout.makeBigger', { name: label });
      smaller.title = t('layout.makeSmaller', { name: label });
      pill.classList.toggle('is-sideways', sideways);
    };
    sync();
    return () => sync(useUiStore.getState().chatDock === 'right' && name === 'chat');
  };
  const STEP = 80;
  grabHandle(terminalResizer, 'terminal', (grow) => {
    const ui = useUiStore.getState();
    ui.setTerminalHeight(ui.terminalHeight + (grow ? STEP : -STEP));
  });
  const syncChatHandle = grabHandle(chatResizer, 'chat', (grow) => {
    const ui = useUiStore.getState();
    if (ui.chatDock === 'right') ui.setChatWidth(ui.chatWidth + (grow ? STEP : -STEP));
    else ui.setChatHeight(ui.chatHeight + (grow ? STEP : -STEP));
  });
  syncChatHandle();
  useUiStore.subscribe((state, previous) => {
    if (state.chatDock !== previous.chatDock) syncChatHandle();
  });

  const stack = h('div', { class: 'stack' }, shell, terminalResizer, terminalPanel);
  const workspace = h('div', { class: 'workspace' }, stack, chatResizer, chatPanel);
  root.append(toolbar, errorBanner, statusStrip, downloadPanel, workspace);

  /* ---------------- views ---------------- */
  const graphView = createGraphView(controller, graphBody);
  const fileTree = createFileTree(controller, filesBody);
  const codeView = createCodeView(controller, codePanel);
  const astView = createAstView(controller, astPanel);
  const diffView = createDiffView(controller, diffPanel);
  const findingsRail = createFindingsRail(controller, railBody);
  const chat = createChatPanel(controller, chatPanel);
  const terminal = createTerminalPanel(controller, terminalPanel);
  const removeShortcuts = installShortcuts(controller, root);
  const removeTutorials = installTutorials(root);
  const settings = createSettingsDialog(controller, root);
  // The expanded graph window: the same graphs, with room to breathe.
  const graphWindow = createGraphWindow(controller, root);
  // Application menu items; Save, Undo, Redo and Keyboard Shortcuts are
  // handled with the shortcuts (views/shortcuts.ts).
  const removeMenu = window.aisai.onMenuCommand((command) => {
    const ui = useUiStore.getState();
    switch (command) {
      case 'open-folder':
        void controller.chooseFolder();
        break;
      case 'about':
        ui.setSettingsSection('about');
        ui.setSettingsOpen(true);
        break;
      case 'settings':
        ui.setSettingsOpen(true);
        break;
      case 'export-pdf':
      case 'export-html':
        window.dispatchEvent(new CustomEvent('aisai:export-report', { detail: command === 'export-pdf' ? 'pdf' : 'html' }));
        break;
      case 'tutorials':
        window.dispatchEvent(new Event('aisai:tutorials'));
        break;
      default:
        break;
    }
  });

  /* ---------------- wiring ---------------- */
  /* ---------------- deleting the downloaded sources ---------------- */
  //
  // Deleting the sources of an audit is irreversible, so the button asks
  // twice: the first click arms it, the second one deletes. The arming
  // lapses after a few seconds so a stray click cannot delete anything
  // later on.
  let deleteArmed = false;
  let deleteTimer: ReturnType<typeof setTimeout> | null = null;

  const disarmDelete = (): void => {
    deleteArmed = false;
    if (deleteTimer) clearTimeout(deleteTimer);
    deleteTimer = null;
    deleteSourcesButton.textContent = t('layout.deleteSources');
    deleteSourcesButton.classList.remove('is-armed');
  };

  deleteSourcesButton.addEventListener('click', () => {
    if (!deleteArmed) {
      deleteArmed = true;
      deleteSourcesButton.textContent = t('layout.confirmDelete');
      deleteSourcesButton.classList.add('is-armed');
      if (deleteTimer) clearTimeout(deleteTimer);
      deleteTimer = setTimeout(disarmDelete, 5000);
      return;
    }
    disarmDelete();
    void controller.deleteSources();
  });

  const applyWidths = (): void => {
    const ui = useUiStore.getState();
    root.style.setProperty('--chat-height', `${ui.chatHeight / 16}rem`);
    root.style.setProperty('--chat-width', `${ui.chatWidth / 16}rem`);
    root.style.setProperty('--terminal-height', `${ui.terminalHeight / 16}rem`);
    terminalPanel.hidden = !ui.terminalOpen;
    terminalResizer.hidden = !ui.terminalOpen;
    graphPane.style.setProperty('--files-share', String(ui.filesShare));
    document.documentElement.style.setProperty('--ui-scale', String(ui.uiScale));
    // Stored in reference pixels, applied in rem so the panes scale with the UI.
    shell.style.setProperty('--graph-width', `${ui.graphWidth / 16}rem`);
    shell.style.setProperty('--rail-width', `${ui.railWidth / 16}rem`);
  };

  /** Puts the visible panes in the stored order, with a divider between neighbours. */
  const renderShell = (): void => {
    const ui = useUiStore.getState();
    const visible = ui.paneOrder.filter((id) => id === 'main' || !ui.hiddenPanes.includes(id));
    const nodes: HTMLElement[] = [];
    visible.forEach((id, index) => {
      if (index > 0) nodes.push(makeResizer(visible[index - 1], id));
      nodes.push(paneEls[id]);
    });
    shell.replaceChildren(...nodes);
    for (const id of Object.keys(paneEls) as PaneId[]) {
      paneEls[id].dataset.slot = String(visible.indexOf(id));
    }
    const dockRight = ui.chatDock === 'right';
    workspace.classList.toggle('dock-right', dockRight);
    chatResizer.classList.toggle('resizer-row', !dockRight);
    chatResizer.setAttribute('aria-orientation', dockRight ? 'vertical' : 'horizontal');
    chatResizer.hidden = !ui.chatOpen && !dockRight;
    placeIndicator();
  };

  const unsubscribeUi = useUiStore.subscribe((state, previous) => {
    if (
      state.paneOrder !== previous.paneOrder ||
      state.hiddenPanes !== previous.hiddenPanes ||
      state.chatDock !== previous.chatDock ||
      state.chatOpen !== previous.chatOpen
    ) {
      renderShell();
    }
    if (
      state.graphWidth !== previous.graphWidth ||
      state.railWidth !== previous.railWidth ||
      state.chatHeight !== previous.chatHeight ||
      state.chatWidth !== previous.chatWidth ||
      state.terminalHeight !== previous.terminalHeight ||
      state.terminalOpen !== previous.terminalOpen ||
      state.filesShare !== previous.filesShare ||
      state.uiScale !== previous.uiScale ||
      state.chatOpen !== previous.chatOpen
    ) {
      applyWidths();
      placeIndicator();
    }
    if (state.activeTab !== previous.activeTab) {
      const active = state.activeTab;
      TABS.forEach((tab, index) => {
        tabButtons[index].setAttribute('aria-selected', String(tab.id === active));
      });
      codePanel.hidden = active !== 'code';
      astPanel.hidden = active !== 'ast';
      diffPanel.hidden = active !== 'diff';
      placeIndicator();
    }
  });

  // Download and scan statistics: counts, size, speed, elapsed time and an
  // estimate of the time left. Refreshed every second while a job runs.
  const renderStats = (): void => {
    const state = useAuditStore.getState();
    if (!state.progressVisible || state.busy === null) {
      statsText.textContent = '';
      return;
    }
    const entries = Object.values(state.downloads);
    statsText.textContent = progressStats({
      now: Date.now(),
      startedAt: state.jobStartedAt || Date.now(),
      phaseStartedAt: state.phaseStartedAt || Date.now(),
      phaseStartPercent: state.phaseStartPercent,
      percent: state.percent,
      downloadsDone: entries.filter((e) => e.state !== 'downloading').length,
      downloadsTotal: state.phase === 'acquiring' ? Math.max(state.downloadTotal, entries.length) : 0,
      downloadedBytes: entries.reduce((sum, e) => sum + (e.bytes ?? 0), 0),
    });
  };
  const statsTimer = window.setInterval(renderStats, 1000);

  const unsubscribeAudit = useAuditStore.subscribe((state, previous) => {
    if (
      state.downloads !== previous.downloads ||
      state.busy !== previous.busy ||
      state.progressVisible !== previous.progressVisible
    ) {
      renderDownloads();
    }
    const audit = state.current;
    const deleted = audit?.sourcesDeleted ?? false;
    sourcesBadge.textContent = deleted ? t('layout.sourcesDeleted') : formatBytes(audit?.sandboxBytes ?? 0);
    sourcesBadge.classList.toggle('badge', true);
    sourcesBadge.classList.toggle('neutral', !deleted);
    sourcesBadge.classList.toggle('warn', deleted);
    deleteSourcesButton.disabled = !audit || deleted;
    if (deleted) disarmDelete();

    const visible = state.progressVisible && state.busy !== null;
    statusStrip.hidden = !visible;
    errorBanner.hidden = !state.error;
    errorText.textContent = state.error ?? '';
    errorAction.hidden = !/disk (space|is full)/i.test(state.error ?? '');
    phaseLabel.textContent = state.phase ? t('layout.phase', { phase: state.phase }) : '';
    statusText.textContent = state.message ?? '';
    const percent = Math.max(0, Math.min(1, state.percent));
    fill.style.width = `${Math.round(percent * 100)}%`;
    track.setAttribute('aria-valuenow', String(Math.round(percent * 100)));
    track.setAttribute('aria-valuemin', '0');
    track.setAttribute('aria-valuemax', '100');
    renderStats();
  });

  const attachRowResizer = (
    resizer: HTMLElement,
    compute: (clientY: number) => void,
    nudge: (delta: number) => void,
  ): void => {
    resizer.addEventListener('pointerdown', (event) => {
      event.preventDefault();
      resizer.setPointerCapture(event.pointerId);
      document.body.classList.add('is-resizing', 'is-resizing-row');
      const onMove = rafThrottle((move: PointerEvent): void => compute(move.clientY));
      const onUp = (): void => {
        document.body.classList.remove('is-resizing', 'is-resizing-row');
        resizer.removeEventListener('pointermove', onMove);
        resizer.removeEventListener('pointerup', onUp);
      };
      resizer.addEventListener('pointermove', onMove);
      resizer.addEventListener('pointerup', onUp);
    });
    resizer.addEventListener('keydown', (event) => {
      const key = (event as KeyboardEvent).key;
      if (key !== 'ArrowUp' && key !== 'ArrowDown') return;
      event.preventDefault();
      nudge(key === 'ArrowUp' ? -1 : 1);
    });
  };

  const PANE_WIDTH: Record<'deps' | 'findings', { get: () => number; set: (w: number) => void; reset: () => void }> = {
    deps: {
      get: () => useUiStore.getState().graphWidth,
      set: (w) => useUiStore.getState().setGraphWidth(w),
      reset: () => useUiStore.getState().setGraphWidth(DEFAULT_LAYOUT.graphWidth),
    },
    findings: {
      get: () => useUiStore.getState().railWidth,
      set: (w) => useUiStore.getState().setRailWidth(w),
      reset: () => useUiStore.getState().setRailWidth(DEFAULT_LAYOUT.railWidth),
    },
  };

  /**
   * A divider between two neighbouring columns. It resizes whichever of the
   * two is not the main column; the main column takes the remaining space.
   */
  function makeResizer(left: PaneId, right: PaneId): HTMLElement {
    const controlled = (left !== 'main' ? left : right) as 'deps' | 'findings';
    const growsRight = left !== 'main'; // the controlled pane is left of the divider
    const resizer = h('div', {
      class: 'resizer',
      role: 'separator',
      tabindex: '0',
      'aria-orientation': 'vertical',
      'aria-label': t('layout.resizePane', { pane: paneName(controlled) }),
    });
    const control = PANE_WIDTH[controlled];
    resizer.addEventListener('pointerdown', (event) => {
      event.preventDefault();
      resizer.setPointerCapture(event.pointerId);
      document.body.classList.add('is-resizing');
      const rect = paneEls[controlled].getBoundingClientRect();
      const onMove = rafThrottle((move: PointerEvent): void => {
        control.set((growsRight ? move.clientX - rect.left : rect.right - move.clientX) / uiScale());
      });
      const onUp = (): void => {
        document.body.classList.remove('is-resizing');
        resizer.removeEventListener('pointermove', onMove);
        resizer.removeEventListener('pointerup', onUp);
      };
      resizer.addEventListener('pointermove', onMove);
      resizer.addEventListener('pointerup', onUp);
    });
    resizer.addEventListener('keydown', (event) => {
      const key = event.key;
      if (key !== 'ArrowLeft' && key !== 'ArrowRight') return;
      event.preventDefault();
      const toward = key === 'ArrowRight' ? 16 : -16;
      control.set(control.get() + (growsRight ? toward : -toward));
    });
    resizer.addEventListener('dblclick', () => control.reset());
    return resizer;
  }

  // The chat panel is docked below or beside the workspace; the same divider
  // resizes it either way.
  chatResizer.addEventListener('pointerdown', (event) => {
    event.preventDefault();
    chatResizer.setPointerCapture(event.pointerId);
    const dockRight = useUiStore.getState().chatDock === 'right';
    document.body.classList.add('is-resizing');
    if (!dockRight) document.body.classList.add('is-resizing-row');
    const onMove = rafThrottle((move: PointerEvent): void => {
      const ui = useUiStore.getState();
      if (ui.chatDock === 'right') {
        ui.setChatWidth((workspace.getBoundingClientRect().right - move.clientX) / uiScale());
      } else {
        ui.setChatHeight((root.getBoundingClientRect().bottom - move.clientY) / uiScale());
      }
    });
    const onUp = (): void => {
      document.body.classList.remove('is-resizing', 'is-resizing-row');
      chatResizer.removeEventListener('pointermove', onMove);
      chatResizer.removeEventListener('pointerup', onUp);
    };
    chatResizer.addEventListener('pointermove', onMove);
    chatResizer.addEventListener('pointerup', onUp);
  });
  chatResizer.addEventListener('keydown', (event) => {
    const ui = useUiStore.getState();
    const dockRight = ui.chatDock === 'right';
    const grow = dockRight ? 'ArrowLeft' : 'ArrowUp';
    const shrink = dockRight ? 'ArrowRight' : 'ArrowDown';
    if (event.key !== grow && event.key !== shrink) return;
    event.preventDefault();
    const delta = event.key === grow ? 16 : -16;
    if (dockRight) ui.setChatWidth(ui.chatWidth + delta);
    else ui.setChatHeight(ui.chatHeight + delta);
  });
  chatResizer.addEventListener('dblclick', () => {
    const ui = useUiStore.getState();
    if (ui.chatDock === 'right') ui.setChatWidth(DEFAULT_LAYOUT.chatWidth);
    else ui.setChatHeight(DEFAULT_LAYOUT.chatHeight);
  });

  attachRowResizer(
    terminalResizer,
    (clientY) => useUiStore.getState().setTerminalHeight((stack.getBoundingClientRect().bottom - clientY) / uiScale()),
    (delta) => useUiStore.getState().setTerminalHeight(useUiStore.getState().terminalHeight - delta * 16),
  );
  terminalResizer.addEventListener('dblclick', () => useUiStore.getState().setTerminalHeight(DEFAULT_LAYOUT.terminalHeight));

  attachRowResizer(
    filesResizer,
    (clientY) => {
      const rect = graphPane.getBoundingClientRect();
      const header = graphHeader.getBoundingClientRect().height;
      const usable = rect.height - header;
      useUiStore.getState().setFilesShare((rect.bottom - clientY) / usable);
    },
    (delta) => useUiStore.getState().setFilesShare(useUiStore.getState().filesShare - delta * 0.03),
  );
  filesResizer.addEventListener('dblclick', () => useUiStore.getState().setFilesShare(DEFAULT_LAYOUT.filesShare));

  applyWidths();
  renderShell();
  requestAnimationFrame(placeIndicator);
  void document.fonts?.ready.then(placeIndicator);
  void toolbarView.refreshProviders();

  return {
    dispose: () => {
      unsubscribeUi();
      unsubscribeAudit();
      resizeObserver.disconnect();
      if (deleteTimer) clearTimeout(deleteTimer);
      toolbarView.dispose();
      graphView.dispose();
      fileTree.dispose();
      codeView.dispose();
      astView.dispose();
      diffView.dispose();
      findingsRail.dispose();
      chat.dispose();
      terminal.dispose();
      removeShortcuts();
      removeTutorials();
      window.clearInterval(statsTimer);
      removeMenu();
      settings.dispose();
      graphWindow.dispose();
    },
  };
}
