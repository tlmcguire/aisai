import type { AppController } from '../app';
import { h } from '../lib/dom';
import { formatBytes } from '../lib/format';
import { useAuditStore } from '../state/audit';
import { useUiStore, type MainTab } from '../state/ui';
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

export function createLayout(controller: AppController, root: HTMLElement): Layout {
  /* ---------------- toolbar ---------------- */
  const toolbar = h('header', { class: 'toolbar' });
  const toolbarView = createToolbar(controller, toolbar);

  /* ---------------- status strip ---------------- */
  const statusText = h('span', { class: 'legend-item', text: '' });
  const phaseLabel = h('span', { class: 'progress-phase', text: '' });
  const fill = h('div', { class: 'progress-fill', style: 'width:0%' });
  const track = h('div', { class: 'progress-track', role: 'progressbar' }, fill);
  const cancelButton = h('button', { class: 'tiny', type: 'button' }, 'Cancel');
  const statusStrip = h(
    'div',
    { class: 'status-strip', hidden: true },
    phaseLabel,
    track,
    statusText,
    h('span', { class: 'grow' }),
    cancelButton,
  );

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
  const filesBody = h('div', { class: 'pane-body files-body' });
  filesPane.append(filesHeader, filesBody);

  graphPane.append(filesPane);

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
  tabBar.append(...tabButtons);

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

  root.append(toolbar, statusStrip, shell, chatPanel);

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
    shell.style.setProperty('--graph-width', `${ui.graphWidth}px`);
    shell.style.setProperty('--rail-width', `${ui.railWidth}px`);
  };

  const unsubscribeUi = useUiStore.subscribe((state, previous) => {
    if (state.graphWidth !== previous.graphWidth || state.railWidth !== previous.railWidth) {
      applyWidths();
    }
    if (state.activeTab !== previous.activeTab) {
      const active = state.activeTab;
      TABS.forEach((tab, index) => {
        tabButtons[index].setAttribute('aria-selected', String(tab.id === active));
      });
      codePanel.hidden = active !== 'code';
      astPanel.hidden = active !== 'ast';
      diffPanel.hidden = active !== 'diff';
    }
  });

  const unsubscribeAudit = useAuditStore.subscribe((state) => {
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
      const onMove = (move: PointerEvent): void => {
        apply(compute(move.clientX));
      };
      const onUp = (): void => {
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
      const direction = key === 'ArrowLeft' ? -16 : 16;
      apply(getWidth() + direction);
    });
  };

  attachResizer(
    graphResizer,
    (clientX) => clientX - shell.getBoundingClientRect().left,
    () => useUiStore.getState().graphWidth,
    (width) => useUiStore.getState().setGraphWidth(width),
  );
  attachResizer(
    railResizer,
    (clientX) => shell.getBoundingClientRect().right - clientX,
    () => useUiStore.getState().railWidth,
    (width) => useUiStore.getState().setRailWidth(width),
  );

  applyWidths();
  void toolbarView.refreshProviders();

  return {
    dispose: () => {
      unsubscribeUi();
      unsubscribeAudit();
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
