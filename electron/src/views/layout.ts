import type { AppController } from '../app';
import { h } from '../lib/dom';
import { icon } from '../lib/icons';
import { formatBytes } from '../lib/format';
import { useAuditStore } from '../state/audit';
import { DEFAULT_LAYOUT, useUiStore, type MainTab } from '../state/ui';
import { createAstView } from './astView';
import { createChatPanel } from './chatPanel';
import { createCodeView } from './codeView';
import { createDiffView } from './diffView';
import { createFileTree } from './fileTree';
import { createFindingsRail } from './findingsRail';
import { createGraphView } from './graphView';
import { createSettingsDialog } from './settingsDialog';
import { createToolbar } from './toolbar';

const TABS: Array<{ id: MainTab; label: string }> = [
  { id: 'code', label: 'Code' },
  { id: 'ast', label: 'AST' },
  { id: 'diff', label: 'Diff' },
];

export interface Layout {
  dispose: () => void;
}

/** The factor between the reference 16px and the current root font size. */
const uiScale = (): number => parseFloat(getComputedStyle(document.documentElement).fontSize) / 16;

export function createLayout(controller: AppController, root: HTMLElement): Layout {
  /* ---------------- toolbar ---------------- */
  const toolbar = h('header', { class: 'toolbar' });
  const toolbarView = createToolbar(controller, toolbar);

  /* ---------------- status strip ---------------- */
  const statusText = h('span', { class: 'legend-item', text: '' });
  const phaseLabel = h('span', { class: 'progress-phase', text: '' });
  const fill = h('div', { class: 'progress-fill', style: 'width:0%' });
  const track = h('div', { class: 'progress-track', role: 'progressbar' }, fill);
  const cancelButton = h('button', { class: 'tiny', type: 'button' }, 'Hide');
  const downloadsToggle = h(
    'button',
    { class: 'tiny ghost downloads-toggle', type: 'button', 'aria-expanded': 'true', hidden: true },
    '',
  );
  const downloadList = h('ol', { class: 'download-list', 'aria-label': 'Package downloads' });
  const downloadPanel = h('div', { class: 'download-panel', hidden: true }, downloadList);
  const statusStrip = h(
    'div',
    { class: 'status-strip', hidden: true, role: 'status' },
    phaseLabel,
    track,
    statusText,
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
    downloading: 'downloading',
    done: 'fetched',
    warning: 'fetched with warnings',
    failed: 'skipped',
  };
  const renderDownloads = (): void => {
    const audit = useAuditStore.getState();
    const entries = Object.values(audit.downloads).sort((a, b) => a.order - b.order);
    const running = audit.busy === 'audit' && audit.progressVisible;
    const finished = entries.filter((e) => e.state !== 'downloading').length;
    downloadsToggle.hidden = entries.length === 0;
    downloadsToggle.replaceChildren(
      `${finished}/${Math.max(audit.downloadTotal, entries.length)} packages`,
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
      (row.children[3] as HTMLElement).textContent = entry.detail && entry.state !== 'done' ? `${STATE_LABEL[entry.state]}: ${entry.detail}` : STATE_LABEL[entry.state];
    }
  };

  cancelButton.addEventListener('click', () => {
    const audit = useAuditStore.getState();
    audit.setProgressVisible(false);
    audit.setBusy(null);
  });

  /* ---------------- panes ---------------- */
  const graphPane = h('section', { class: 'pane pane-graph', 'aria-label': 'Dependency graph' });
  const graphHeader = h(
    'div',
    { class: 'pane-header' },
    h('span', { text: 'Dependencies' }),
    h('span', { class: 'grow' }),
  );
  const graphBody = h('div', { class: 'pane-body graph-section' });
  graphPane.append(graphHeader, graphBody);

  const filesPane = h('div', { class: 'files-section' });
  const sourcesBadge = h('span', { class: 'badge neutral', text: '' });
  const deleteSourcesButton = h(
    'button',
    { class: 'tiny', type: 'button', title: 'Delete the downloaded sources of this audit' },
    'Delete sources',
  );
  const filesHeader = h(
    'div',
    { class: 'pane-header' },
    h('span', { text: 'Files' }),
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
    'aria-label': 'Resize the dependency graph and the file tree',
  });
  graphPane.append(filesResizer, filesPane);

  /* ---------------- main pane ---------------- */
  const tabBar = h('div', { class: 'tabs', role: 'tablist' });
  const codePanel = h('div', { class: 'tab-panel', role: 'tabpanel' });
  const astPanel = h('div', { class: 'tab-panel', role: 'tabpanel', hidden: true });
  const diffPanel = h('div', { class: 'tab-panel', role: 'tabpanel', hidden: true });
  const mainPane = h('section', { class: 'pane pane-main', 'aria-label': 'Code, AST and diff' });
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
      tab.label,
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
  const railPane = h('section', { class: 'pane pane-rail', 'aria-label': 'Findings' });
  const railHeader = h(
    'div',
    { class: 'pane-header' },
    h('span', { text: 'Findings' }),
    h('span', { class: 'grow' }),
  );
  const railBody = h('div', { class: 'pane-body rail-body' });
  railPane.append(railHeader, railBody);

  /* ---------------- resizers ---------------- */
  const shell = h('main', { class: 'shell' });
  const graphResizer = h('div', {
    class: 'resizer',
    role: 'separator',
    tabindex: '0',
    'aria-orientation': 'vertical',
    'aria-label': 'Resize the dependency graph pane',
  });
  const railResizer = h('div', {
    class: 'resizer',
    role: 'separator',
    tabindex: '0',
    'aria-orientation': 'vertical',
    'aria-label': 'Resize the findings rail',
  });

  shell.append(graphPane, graphResizer, mainPane, railResizer, railPane);

  /* ---------------- chat ---------------- */
  const chatPanel = h('section', { class: 'chat-panel', 'aria-label': 'Chat' });

  const chatResizer = h('div', {
    class: 'resizer resizer-row resizer-chat',
    role: 'separator',
    tabindex: '0',
    'aria-orientation': 'horizontal',
    'aria-label': 'Resize the chat panel',
  });

  root.append(toolbar, statusStrip, downloadPanel, shell, chatResizer, chatPanel);

  /* ---------------- views ---------------- */
  const graphView = createGraphView(controller, graphBody);
  const fileTree = createFileTree(controller, filesBody);
  const codeView = createCodeView(controller, codePanel);
  const astView = createAstView(controller, astPanel);
  const diffView = createDiffView(controller, diffPanel);
  const findingsRail = createFindingsRail(controller, railBody);
  const chat = createChatPanel(controller, chatPanel);
  const settings = createSettingsDialog(controller, root);

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
    deleteSourcesButton.textContent = 'Delete sources';
    deleteSourcesButton.classList.remove('is-armed');
  };

  deleteSourcesButton.addEventListener('click', () => {
    if (!deleteArmed) {
      deleteArmed = true;
      deleteSourcesButton.textContent = 'Confirm delete';
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
    graphPane.style.setProperty('--files-share', String(ui.filesShare));
    document.documentElement.style.setProperty('--ui-scale', String(ui.uiScale));
    chatResizer.hidden = !ui.chatOpen;
    // Stored in reference pixels, applied in rem so the panes scale with the UI.
    shell.style.setProperty('--graph-width', `${ui.graphWidth / 16}rem`);
    shell.style.setProperty('--rail-width', `${ui.railWidth / 16}rem`);
  };

  const unsubscribeUi = useUiStore.subscribe((state, previous) => {
    if (
      state.graphWidth !== previous.graphWidth ||
      state.railWidth !== previous.railWidth ||
      state.chatHeight !== previous.chatHeight ||
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
    sourcesBadge.textContent = deleted ? 'sources deleted' : formatBytes(audit?.sandboxBytes ?? 0);
    sourcesBadge.classList.toggle('badge', true);
    sourcesBadge.classList.toggle('neutral', !deleted);
    sourcesBadge.classList.toggle('warn', deleted);
    deleteSourcesButton.disabled = !audit || deleted;
    if (deleted) disarmDelete();

    const visible = state.progressVisible && state.busy !== null;
    statusStrip.hidden = !visible;
    phaseLabel.textContent = state.phase ? `Phase: ${state.phase}` : '';
    statusText.textContent = state.message ?? '';
    const percent = Math.max(0, Math.min(1, state.percent));
    fill.style.width = `${Math.round(percent * 100)}%`;
    track.setAttribute('aria-valuenow', String(Math.round(percent * 100)));
    track.setAttribute('aria-valuemin', '0');
    track.setAttribute('aria-valuemax', '100');
  });

  const attachResizer = (
    resizer: HTMLElement,
    compute: (clientX: number) => number,
    getWidth: () => number,
    apply: (width: number) => void,
  ): void => {
    resizer.addEventListener('pointerdown', (event) => {
      event.preventDefault();
      resizer.setPointerCapture(event.pointerId);
      document.body.classList.add('is-resizing');
      const onMove = (move: PointerEvent): void => {
        apply(compute(move.clientX));
      };
      const onUp = (): void => {
        document.body.classList.remove('is-resizing');
        resizer.removeEventListener('pointermove', onMove);
        resizer.removeEventListener('pointerup', onUp);
      };
      resizer.addEventListener('pointermove', onMove);
      resizer.addEventListener('pointerup', onUp);
    });
    resizer.addEventListener('keydown', (event) => {
      const key = (event as KeyboardEvent).key;
      if (key !== 'ArrowLeft' && key !== 'ArrowRight') return;
      event.preventDefault();
      // The rail grows to the left, so the arrows mirror on it.
      const direction = (key === 'ArrowLeft' ? -16 : 16) * (resizer === railResizer ? -1 : 1);
      apply(getWidth() + direction);
    });
  };

  const attachRowResizer = (
    resizer: HTMLElement,
    compute: (clientY: number) => void,
    nudge: (delta: number) => void,
  ): void => {
    resizer.addEventListener('pointerdown', (event) => {
      event.preventDefault();
      resizer.setPointerCapture(event.pointerId);
      document.body.classList.add('is-resizing', 'is-resizing-row');
      const onMove = (move: PointerEvent): void => compute(move.clientY);
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
  attachRowResizer(
    chatResizer,
    (clientY) => useUiStore.getState().setChatHeight((root.getBoundingClientRect().bottom - clientY) / uiScale()),
    (delta) => useUiStore.getState().setChatHeight(useUiStore.getState().chatHeight - delta * 16),
  );
  // Double-clicking a divider puts that pane back to its default size.
  filesResizer.addEventListener('dblclick', () => useUiStore.getState().setFilesShare(DEFAULT_LAYOUT.filesShare));
  chatResizer.addEventListener('dblclick', () => useUiStore.getState().setChatHeight(DEFAULT_LAYOUT.chatHeight));
  graphResizer.addEventListener('dblclick', () => useUiStore.getState().setGraphWidth(DEFAULT_LAYOUT.graphWidth));
  railResizer.addEventListener('dblclick', () => useUiStore.getState().setRailWidth(DEFAULT_LAYOUT.railWidth));

  attachResizer(
    graphResizer,
    (clientX) => (clientX - shell.getBoundingClientRect().left) / uiScale(),
    () => useUiStore.getState().graphWidth,
    (width) => useUiStore.getState().setGraphWidth(width),
  );
  attachResizer(
    railResizer,
    (clientX) => (shell.getBoundingClientRect().right - clientX) / uiScale(),
    () => useUiStore.getState().railWidth,
    (width) => useUiStore.getState().setRailWidth(width),
  );

  applyWidths();
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
      settings.dispose();
    },
  };
}
