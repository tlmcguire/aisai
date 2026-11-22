/**
 * The expanded graph window: a large copy of a graph, in a window of its
 * own, like Settings.
 *
 * The dependency graph and the syntax tree are drawn in panes that are
 * often narrow. This window shows the same graph with room to breathe: the
 * pane keeps its place, and the window is where the graph can be read,
 * panned and exported.
 *
 * The window follows the store (state/ui.ts): the expand buttons say which
 * graph is open, and the window opens and closes with it. Both windows read
 * the same stores, so the search, the filters and the depth of the syntax
 * tree are the same on both sides.
 */

import type { AppController } from '../app';
import { SEVERITIES, type Severity } from '../api/types';
import { h } from '../lib/dom';
import { t } from '../lib/i18n';
import { icon } from '../lib/icons';
import { severityClass, severityLabel } from '../lib/severity';
import { loadVis, type VisColorSet, type VisDataSet, type VisEdgeData, type VisNetwork, type VisNodeData } from '../lib/vis';
import { useAstStore } from '../state/ast';
import { useFindingsStore } from '../state/findings';
import { useGraphStore } from '../state/graph';
import { useUiStore, type GraphKind } from '../state/ui';
import { useWorkspaceStore } from '../state/workspace';
import { astGraphData, astNetworkOptions, exportAstSvg } from './astView';
import {
  dependencyFilterUpdates,
  dependencyGraphData,
  dependencyNetworkOptions,
  download,
  exportPng,
  exportSvg,
} from './graphView';

export interface GraphWindow {
  dispose: () => void;
}

/** The dimmed colour of a node that is not part of what is being read. */
const DIM = 'rgba(140,160,155,0.18)';

/**
 * The expanded window for the graphs.
 *
 * Each graph gets its own canvas and its own network: vis-network's helper
 * for the neighbourhood highlight works on the globals that the dependency
 * graph in the pane owns, so this window draws its networks itself.
 */
export function createGraphWindow(controller: AppController, container: HTMLElement): GraphWindow {
  const title = h('h2', { class: 'graph-window-title' });
  const depsControls = h('div', { class: 'graph-window-controls' });
  const astControls = h('div', { class: 'graph-window-controls' });
  const exports = h('div', { class: 'graph-window-controls' });
  const close = h(
    'button',
    { class: 'ghost icon pref-close', type: 'button', 'aria-label': t('graph.windowClose'), title: t('graph.windowCloseTitle') },
    icon('x'),
  );

  const depsCanvas = h('div', { class: 'graph-canvas graph-window-canvas', role: 'img', 'aria-label': t('graph.windowTitle') });
  const astCanvas = h('div', { class: 'graph-canvas graph-window-canvas', role: 'img', 'aria-label': t('ast.windowTitle'), hidden: true });
  const status = h('div', { class: 'empty-state', hidden: true });

  const dialog = h(
    'dialog',
    { class: 'graph-window' },
    h(
      'div',
      { class: 'graph-window-shell' },
      h('div', { class: 'graph-window-header' }, title, h('span', { class: 'grow' }), depsControls, astControls, exports, close),
      h('div', { class: 'graph-window-body' }, depsCanvas, astCanvas, status),
    ),
  );
  container.append(dialog);

  /* ---------------- the dependency graph ---------------- */

  const searchInput = h('input', {
    type: 'search',
    placeholder: t('graph.searchPlaceholder'),
    'aria-label': t('graph.searchLabel'),
  });
  searchInput.addEventListener('input', () => {
    useGraphStore.getState().setSearch(searchInput.value);
  });

  const severityChips = SEVERITIES.map((severity) =>
    h(
      'button',
      {
        class: `chip ${severityClass(severity)}`,
        type: 'button',
        'aria-pressed': 'false',
        // The severity is a value, so it travels in the markup rather than
        // in the label, which is translated.
        dataset: { severity },
        onclick: () => useGraphStore.getState().toggleSeverity(severity),
      },
      severityLabel(severity),
    ),
  );
  depsControls.append(searchInput, h('div', { class: 'row' }, ...severityChips));

  let depsNetwork: VisNetwork | null = null;
  let depsNodes: VisDataSet<VisNodeData> | null = null;
  let depsEdges: VisDataSet<VisEdgeData> | null = null;
  /** The colour of every node, so a selection can be undone. */
  const depsColors = new Map<string, VisColorSet>();

  /** Repaints the nodes: hidden by the filters, dimmed by the selection. */
  const paintDeps = (): void => {
    if (!depsNodes) return;
    const state = useGraphStore.getState();
    const selectedId = state.selectedId;
    const near = new Set<string>();
    if (selectedId && depsNetwork) {
      near.add(selectedId);
      for (const id of depsNetwork.getConnectedNodes(selectedId)) near.add(id);
    }
    depsNodes.update(
      dependencyFilterUpdates(state).map((update) => ({
        id: update.id,
        hidden: update.hidden,
        borderWidth: update.borderWidth,
        color:
          selectedId && !near.has(update.id)
            ? DIM
            : depsColors.get(update.id) ?? (typeof update.color === 'string' ? update.color : DIM),
      })),
    );
    for (const chip of severityChips) {
      const severity = chip.dataset.severity as Severity | undefined;
      chip.setAttribute(
        'aria-pressed',
        String(severity ? state.severities.includes(severity) : false),
      );
    }
  };

  const renderDeps = (): void => {
    const state = useGraphStore.getState();
    const data = dependencyGraphData(state.nodes, state.edges);
    for (const node of data.nodes) {
      if (node.color && typeof node.color !== 'string') depsColors.set(node.id, node.color);
    }
    if (!depsNetwork) {
      const vis = window.vis;
      if (!vis) return;
      depsNodes = new vis.DataSet<VisNodeData>(data.nodes);
      depsEdges = new vis.DataSet<VisEdgeData>(data.edges);
      depsNetwork = new vis.Network(canvasFor('deps'), { nodes: depsNodes, edges: depsEdges }, dependencyNetworkOptions());
      depsNetwork.on('stabilizationIterationsDone', () => {
        depsNetwork?.setOptions({ physics: { enabled: false } });
        depsNetwork?.fit({ animation: { duration: 650, easingFunction: 'easeInOutQuad' } });
      });
      depsNetwork.on('click', (params) => {
        const ids = (params as { nodes?: string[] }).nodes ?? [];
        useGraphStore.getState().select(ids[0] ?? null);
        paintDeps();
      });
      depsNetwork.on('doubleClick', (params) => {
        const ids = (params as { nodes?: string[] }).nodes ?? [];
        if (ids[0]) depsNetwork?.focus(ids[0], { scale: 1.3, animation: true });
      });
    } else {
      depsNodes?.clear();
      depsNodes?.add(data.nodes);
      depsEdges?.clear();
      depsEdges?.add(data.edges);
      depsNetwork?.setOptions({ physics: { enabled: true } });
      depsNetwork?.stabilize(300);
    }
    paintDeps();
  };

  /* ---------------- the syntax tree ---------------- */

  const depthSelect = h(
    'select',
    {
      'aria-label': t('ast.maxDepthLabel'),
      onchange: (event: Event) => {
        const value = (event.target as HTMLSelectElement).value;
        useAstStore.getState().setDepthLimit(value === 'all' ? Number.POSITIVE_INFINITY : Number(value));
      },
    },
    ...[1, 2, 3, 4, 6, 8].map((depth) =>
      h('option', { value: String(depth), selected: depth === useAstStore.getState().depthLimit }, t('ast.depthOption', { depth })),
    ),
    h('option', { value: 'all' }, t('ast.allDepths')),
  );
  astControls.append(h('span', { class: 'legend-item', text: t('ast.depth') }), depthSelect);

  let astNetwork: VisNetwork | null = null;
  let astData: { nodes: VisNodeData[]; edges: VisEdgeData[] } = { nodes: [], edges: [] };

  const renderAst = (): void => {
    const ast = useWorkspaceStore.getState().ast;
    if (!ast) {
      astNetwork?.destroy();
      astNetwork = null;
      astData = { nodes: [], edges: [] };
      return;
    }
    const findings = useFindingsStore.getState().findings.filter((finding) => finding.file === ast.path);
    const { nodes, edges } = astGraphData(
      ast,
      findings,
      useAstStore.getState().depthLimit,
      useAstStore.getState().nodeCap,
    );
    astData = { nodes, edges };
    // The syntax tree is laid out from scratch each time, so the network is
    // rebuilt rather than fed new nodes.
    astNetwork?.destroy();
    astNetwork = null;
    const vis = window.vis;
    if (!vis) return;
    const nodeSet = new vis.DataSet<VisNodeData>(nodes);
    const edgeSet = new vis.DataSet<VisEdgeData>(edges);
    astNetwork = new vis.Network(canvasFor('ast'), { nodes: nodeSet, edges: edgeSet }, astNetworkOptions());
    astNetwork.on('click', (params) => {
      const id = (params as { nodes?: string[] }).nodes?.[0];
      const node = id ? ast.nodes.find((entry) => entry.id === id) : undefined;
      if (node) void controller.openFile(ast.path, node.startLine);
    });
    setTimeout(() => astNetwork?.fit({ animation: false }), 150);
  };

  /* ---------------- the window itself ---------------- */

  /** The canvas of a graph: each graph has its own, so both can be drawn. */
  function canvasFor(graph: GraphKind): HTMLElement {
    return graph === 'deps' ? depsCanvas : astCanvas;
  }

  const pngButton = h('button', { class: 'tiny', type: 'button', title: t('graph.exportPng') }, 'PNG');
  const svgButton = h('button', { class: 'tiny', type: 'button', title: t('graph.exportSvg') }, 'SVG');
  exports.append(pngButton, svgButton);

  pngButton.addEventListener('click', () => {
    const href = exportPng(canvasFor(currentKind));
    if (href) download(`aisai-${currentKind === 'deps' ? 'dependency-graph' : 'ast-graph'}.png`, href);
  });

  svgButton.addEventListener('click', () => {
    if (currentKind === 'deps') {
      const svg = depsNetwork ? exportSvg(depsNetwork, useGraphStore.getState().nodes, useGraphStore.getState().edges) : null;
      if (!svg) return;
      const url = URL.createObjectURL(new Blob([svg], { type: 'image/svg+xml' }));
      download('aisai-dependency-graph.svg', url);
      setTimeout(() => URL.revokeObjectURL(url), 5000);
      return;
    }
    const svg = astNetwork ? exportAstSvg(astNetwork, astData.nodes, astData.edges) : null;
    if (!svg) return;
    const url = URL.createObjectURL(new Blob([svg], { type: 'image/svg+xml' }));
    download('aisai-ast-graph.svg', url);
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  });

  let currentKind: GraphKind = 'deps';

  const showKind = (kind: GraphKind): void => {
    currentKind = kind;
    title.textContent = kind === 'deps' ? t('graph.windowTitle') : t('ast.windowTitle');
    depsControls.hidden = kind !== 'deps';
    astControls.hidden = kind !== 'ast';
    depsCanvas.hidden = kind !== 'deps';
    astCanvas.hidden = kind !== 'ast';
    status.hidden = true;
    if (kind === 'deps') {
      const empty = useGraphStore.getState().nodes.length === 0;
      status.hidden = !empty;
      if (empty) {
        status.replaceChildren(h('h3', { text: t('graph.noGraphYet') }), h('p', { text: t('graph.noGraphHint') }));
      }
      renderDeps();
    } else {
      const empty = !useWorkspaceStore.getState().ast;
      status.hidden = !empty;
      if (empty) {
        status.replaceChildren(h('h3', { text: t('ast.noTree') }), h('p', { text: t('ast.noTreeHint') }));
      }
      renderAst();
    }
    // The canvas only has its size once the window is open.
    requestAnimationFrame(() => {
      const network = kind === 'deps' ? depsNetwork : astNetwork;
      network?.fit({ animation: false });
    });
  };

  const open = (kind: GraphKind): void => {
    if (!dialog.open) dialog.showModal();
    showKind(kind);
  };

  close.addEventListener('click', () => dialog.close());
  // The store says whether the window is open, so the window follows it.
  dialog.addEventListener('close', () => {
    if (useUiStore.getState().expandedGraph) useUiStore.getState().setExpandedGraph(null);
  });
  // A click on the backdrop closes the window, like a sheet.
  dialog.addEventListener('mousedown', (event) => {
    if (event.target === dialog) dialog.close();
  });

  // The window is as big as its canvas allows: a resize fits the graph again.
  const observer = new ResizeObserver(() => {
    const network = currentKind === 'deps' ? depsNetwork : astNetwork;
    network?.fit({ animation: false });
  });
  observer.observe(depsCanvas);
  observer.observe(astCanvas);

  const unsubscribeUi = useUiStore.subscribe((state, previous) => {
    if (state.expandedGraph !== previous.expandedGraph) {
      if (state.expandedGraph) open(state.expandedGraph);
      else if (dialog.open) dialog.close();
    }
    // The colours of the dependency graph follow the theme.
    if ((state.theme !== previous.theme || state.accent !== previous.accent) && dialog.open && currentKind === 'deps') {
      renderDeps();
    }
  });

  const unsubscribeGraph = useGraphStore.subscribe((state, previous) => {
    if (!dialog.open || currentKind !== 'deps') return;
    if (state.nodes !== previous.nodes || state.edges !== previous.edges) {
      renderDeps();
      return;
    }
    if (searchInput.value !== state.search) searchInput.value = state.search;
    paintDeps();
  });

  const unsubscribeWorkspace = useWorkspaceStore.subscribe((state, previous) => {
    if (!dialog.open || currentKind !== 'ast') return;
    if (state.ast !== previous.ast) renderAst();
  });

  const unsubscribeFindings = useFindingsStore.subscribe((state, previous) => {
    if (!dialog.open || currentKind !== 'ast') return;
    if (state.findings !== previous.findings) renderAst();
  });

  const unsubscribeAst = useAstStore.subscribe((state, previous) => {
    if (!dialog.open || currentKind !== 'ast') return;
    if (state.depthLimit !== previous.depthLimit) {
      depthSelect.value = state.depthLimit === Number.POSITIVE_INFINITY ? 'all' : String(state.depthLimit);
    }
    if (state.depthLimit !== previous.depthLimit || state.nodeCap !== previous.nodeCap) renderAst();
  });

  // The graph is drawn once the library is there.
  void loadVis().catch((err: unknown) => {
    console.error('[aisai] the expanded graph could not be rendered', err);
    status.hidden = false;
    status.replaceChildren(h('h3', { text: t('graph.renderFailed') }), h('p', { text: err instanceof Error ? err.message : String(err) }));
  });

  return {
    dispose: () => {
      unsubscribeUi();
      unsubscribeGraph();
      unsubscribeWorkspace();
      unsubscribeFindings();
      unsubscribeAst();
      observer.disconnect();
      depsNetwork?.destroy();
      depsNetwork = null;
      astNetwork?.destroy();
      astNetwork = null;
      dialog.remove();
    },
  };
}
