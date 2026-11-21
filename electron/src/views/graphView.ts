import type { AppController } from '../app';
import { SEVERITIES, type GraphNode, type Severity } from '../api/types';
import { h } from '../lib/dom';
import { severityClass, severityColor, severityFill, severityLabel } from '../lib/severity';
import {
  createNetwork,
  highlightNeighbourhood,
  loadVis,
  type VisColorSet,
  type VisDataSet,
  type VisEdgeData,
  type VisNetwork,
  type VisNodeData,
} from '../lib/vis';
import { findPath, useGraphStore } from '../state/graph';

function nodeColor(node: GraphNode): VisColorSet {
  return {
    background: severityFill(node.maxSeverity),
    border: severityColor(node.maxSeverity),
    highlight: { background: severityFill(node.maxSeverity), border: '#ffffff' },
    hover: { background: severityFill(node.maxSeverity), border: '#ffffff' },
  };
}

function visNode(node: GraphNode): VisNodeData {
  return {
    id: node.id,
    label: `${node.name}\nv${node.version}`,
    title: `${node.name} ${node.version}\n${node.findingCount} findings, ${node.vulnCount} advisories`,
    // Direct dependencies get a distinct shape.
    shape: node.direct ? 'diamond' : 'dot',
    color: nodeColor(node),
    borderWidth: node.direct ? 2 : 1,
    font: { color: '#e6edf3', size: 11, face: 'monospace', multi: true },
  };
}

function visEdge(from: string, to: string): VisEdgeData {
  return {
    id: `${from}->${to}`,
    from,
    to,
    color: { color: '#484f58', highlight: '#58a6ff', hover: '#58a6ff' },
    width: 1,
    arrows: 'to',
    smooth: { type: 'continuous' },
  };
}

function download(filename: string, href: string): void {
  const anchor = document.createElement('a');
  anchor.href = href;
  anchor.download = filename;
  anchor.rel = 'noopener';
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
}

/**
 * PNG export.
 *
 * vis-network 9 has no `canvasToPNG()`; the renderer paints into a real
 * `<canvas>`, so we composite that canvas onto an opaque background.
 */
function exportPng(container: HTMLElement): string | null {
  const canvas = container.querySelector('canvas');
  if (!canvas) return null;
  const output = document.createElement('canvas');
  output.width = canvas.width;
  output.height = canvas.height;
  const context = output.getContext('2d');
  if (!context) return null;
  context.fillStyle = '#0d1117';
  context.fillRect(0, 0, output.width, output.height);
  context.drawImage(canvas, 0, 0);
  return output.toDataURL('image/png');
}

/**
 * SVG export.
 *
 * vis-network 9 has no `canvasToSVG()` either, so the SVG is generated from
 * the live node positions returned by `network.getPositions()`.
 */
function exportSvg(
  network: VisNetwork,
  nodes: GraphNode[],
  edges: Array<{ source: string; target: string }>,
): string | null {
  if (!nodes.length) return null;
  const positions = network.getPositions(nodes.map((node) => node.id)) as Record<
    string,
    { x: number; y: number }
  >;

  const points = nodes
    .map((node) => positions[node.id])
    .filter((point): point is { x: number; y: number } => !!point);
  if (!points.length) return null;

  const padding = 60;
  const minX = Math.min(...points.map((p) => p.x)) - padding;
  const maxX = Math.max(...points.map((p) => p.x)) + padding;
  const minY = Math.min(...points.map((p) => p.y)) - padding;
  const maxY = Math.max(...points.map((p) => p.y)) + padding;
  const width = Math.max(320, maxX - minX);
  const height = Math.max(240, maxY - minY);

  const parts: string[] = [
    `<svg xmlns="http://www.w3.org/2000/svg" width="${Math.round(width)}" height="${Math.round(height)}" viewBox="${minX} ${minY} ${width} ${height}">`,
    '<defs><marker id="arrow" markerWidth="8" markerHeight="8" refX="8" refY="3" orient="auto"><path d="M0,0 L0,6 L8,3 z" fill="#8b949e"/></marker></defs>',
    '<rect x="-100000" y="-100000" width="200000" height="200000" fill="#0d1117"/>',
  ];

  for (const edge of edges) {
    const from = positions[edge.source];
    const to = positions[edge.target];
    if (!from || !to) continue;
    parts.push(
      `<line x1="${from.x}" y1="${from.y}" x2="${to.x}" y2="${to.y}" stroke="#484f58" stroke-width="1" marker-end="url(#arrow)"/>`,
    );
  }

  for (const node of nodes) {
    const point = positions[node.id];
    if (!point) continue;
    const radius = node.direct ? 11 : 7;
    parts.push(
      `<circle cx="${point.x}" cy="${point.y}" r="${radius}" fill="${severityFill(node.maxSeverity)}" stroke="${severityColor(node.maxSeverity)}" stroke-width="${node.direct ? 2 : 1}"/>`,
      `<text x="${point.x}" y="${point.y - radius - 5}" fill="#e6edf3" font-family="monospace" font-size="11" text-anchor="middle">${node.name}</text>`,
      `<text x="${point.x}" y="${point.y - radius - 16}" fill="#9198a1" font-family="monospace" font-size="10" text-anchor="middle">v${node.version}</text>`,
    );
  }

  parts.push('</svg>');
  return parts.join('\n');
}

export interface GraphView {
  element: HTMLElement;
  dispose: () => void;
}

export function createGraphView(_controller: AppController, container: HTMLElement): GraphView {
  const canvas = h('div', { class: 'graph-canvas' });
  const status = h('div', { class: 'empty-state' }, 'Loading the dependency graph…');

  const searchInput = h('input', {
    type: 'search',
    placeholder: 'Search packages…',
    'aria-label': 'Search packages in the dependency graph',
  });

  const pathInput = h('input', {
    type: 'search',
    placeholder: 'Why is this package here? e.g. urllib3',
    'aria-label': 'Path finder: package name',
  });

  const pathResult = h('span', { class: 'legend-item', text: '' });

  const severityChips = SEVERITIES.map((severity) =>
    h(
      'button',
      {
        class: `chip ${severityClass(severity)}`,
        type: 'button',
        'aria-pressed': 'false',
        onclick: () => {
          useGraphStore.getState().toggleSeverity(severity);
        },
      },
      severityLabel(severity),
    ),
  );

  const exportPngButton = h('button', { class: 'tiny', type: 'button', title: 'Export as PNG' }, 'PNG');
  const exportSvgButton = h('button', { class: 'tiny', type: 'button', title: 'Export as SVG' }, 'SVG');

  const cycleBadge = h('span', { class: 'badge neutral', text: '' });

  const controls = h(
    'div',
    { class: 'graph-controls' },
    h('div', { class: 'row' }, searchInput),
    h('div', { class: 'row' }, ...severityChips),
    h('div', { class: 'row' }, pathInput, pathResult),
    h(
      'div',
      { class: 'row' },
      h('span', { class: 'legend-item', text: 'Export:' }),
      exportPngButton,
      exportSvgButton,
    ),
  );

  const legend = h(
    'div',
    { class: 'graph-legend' },
    h(
      'span',
      { class: 'legend-item' },
      h('span', { class: 'legend-swatch', style: 'background:#ff7b72' }),
      'critical',
    ),
    h(
      'span',
      { class: 'legend-item' },
      h('span', { class: 'legend-swatch', style: 'background:#ffa657' }),
      'high',
    ),
    h(
      'span',
      { class: 'legend-item' },
      h('span', { class: 'legend-swatch', style: 'background:#d29922' }),
      'medium',
    ),
    h(
      'span',
      { class: 'legend-item' },
      h('span', { class: 'legend-swatch', style: 'background:#58a6ff' }),
      'low',
    ),
    h(
      'span',
      { class: 'legend-item' },
      h('span', { class: 'legend-swatch', style: 'background:#8b949e' }),
      'info',
    ),
    h('span', { class: 'legend-item', text: '◆ direct dependency' }),
    cycleBadge,
  );

  container.append(controls, canvas, status, legend);

  let network: VisNetwork | null = null;
  let nodeSet: VisDataSet<VisNodeData> | null = null;
  let edgeSet: VisDataSet<VisEdgeData> | null = null;
  let destroyed = false;

  const applyFilters = (): void => {
    if (!nodeSet) return;
    const state = useGraphStore.getState();
    const search = state.search.trim().toLowerCase();
    const severityFilter = state.severities;
    const pathNodes = new Set(state.path);
    const hasPath = pathNodes.size > 0;

    const updates: VisNodeData[] = [];
    for (const node of state.nodes) {
      const matchesSearch = !search || node.name.toLowerCase().includes(search);
      const matchesSeverity =
        severityFilter.length === 0 || severityFilter.includes(node.maxSeverity);
      const inPath = !hasPath || pathNodes.has(node.id);
      updates.push({
        id: node.id,
        hidden: !(matchesSearch && matchesSeverity),
        color: inPath ? nodeColor(node) : 'rgba(200,200,200,0.22)',
        borderWidth: inPath && hasPath ? 3 : node.direct ? 2 : 1,
      });
    }
    nodeSet.update(updates);

    for (const chip of severityChips) {
      const severity = chip.textContent?.toLowerCase() as Severity | undefined;
      chip.setAttribute(
        'aria-pressed',
        String(severity ? severityFilter.includes(severity) : false),
      );
    }
  };

  const renderData = (): void => {
    const state = useGraphStore.getState();
    if (!state.nodes.length) {
      status.hidden = false;
      status.replaceChildren(
        h('h3', { text: 'No dependency graph yet' }),
        h('p', {
          text:
            'Start an audit from the toolbar to download a package and build its dependency graph. ' +
            'The graph shows every transitive dependency, coloured by the worst severity found.',
        }),
      );
      return;
    }
    status.hidden = true;

    const nodes = state.nodes.map(visNode);
    const edges = state.edges.map((edge) => visEdge(edge.source, edge.target));

    // The vendored neighbourhood helper restores colours from this map.
    window.nodeColors = {};
    for (const node of nodes) {
      window.nodeColors[node.id] = node.color as VisColorSet;
    }

    nodeSet?.clear();
    nodeSet?.add(nodes);
    edgeSet?.clear();
    edgeSet?.add(edges);

    // The physics layout needs a moment, then the whole graph is fitted into
    // the (small) graph pane.
    network?.stabilize(200);
    window.setTimeout(() => network?.fit({ animation: false }), 120);

    applyFilters();

    cycleBadge.textContent = state.cycleCount
      ? `${state.cycleCount} dependency cycle${state.cycleCount === 1 ? '' : 's'}`
      : 'no dependency cycles';
    cycleBadge.className = state.cycleCount ? 'badge bad' : 'badge ok';
  };

  searchInput.addEventListener('input', () => {
    useGraphStore.getState().setSearch(searchInput.value);
    applyFilters();
  });

  const runPathFinder = (): void => {
    const state = useGraphStore.getState();
    const query = pathInput.value.trim().toLowerCase();
    if (!query) {
      state.setPath(null, []);
      pathResult.textContent = '';
      applyFilters();
      return;
    }
    const target =
      state.nodes.find((node) => node.name.toLowerCase() === query) ??
      state.nodes.find((node) => node.name.toLowerCase().includes(query));
    if (!target) {
      state.setPath(null, []);
      pathResult.textContent = `No package named “${pathInput.value.trim()}” in this audit.`;
      applyFilters();
      return;
    }
    const rootId = state.nodes.find((node) => node.direct)?.id;
    const path = findPath(state.nodes, state.edges, target.id, rootId);
    state.setPath(target.id, path);
    pathResult.textContent = path.length
      ? path.map((id) => state.nodes.find((n) => n.id === id)?.name ?? id).join(' → ')
      : `No dependency path from the root package to ${target.name}.`;
    applyFilters();
    if (network && path.length) {
      network.focus(target.id, { scale: 1.2, animation: true });
    }
  };

  pathInput.addEventListener('keydown', (event) => {
    if (event.key === 'Enter') {
      event.preventDefault();
      runPathFinder();
    }
  });
  pathInput.addEventListener('change', () => {
    runPathFinder();
  });

  exportPngButton.addEventListener('click', () => {
    const href = exportPng(canvas);
    if (!href) {
      pathResult.textContent = 'The graph canvas is not ready yet.';
      return;
    }
    download('aisai-dependency-graph.png', href);
  });

  exportSvgButton.addEventListener('click', () => {
    const state = useGraphStore.getState();
    const svg = network ? exportSvg(network, state.nodes, state.edges) : null;
    if (!svg) {
      pathResult.textContent = 'The graph canvas is not ready yet.';
      return;
    }
    const blob = new Blob([svg], { type: 'image/svg+xml' });
    const url = URL.createObjectURL(blob);
    download('aisai-dependency-graph.svg', url);
    setTimeout(() => URL.revokeObjectURL(url), 5000);
  });

  const unsubscribe = useGraphStore.subscribe((state, previous) => {
    if (state.nodes !== previous.nodes || state.edges !== previous.edges) {
      renderData();
      return;
    }
    if (
      state.search !== previous.search ||
      state.severities !== previous.severities ||
      state.path !== previous.path
    ) {
      applyFilters();
    }
  });

  void loadVis()
    .then(() => {
      if (destroyed) return;
      const created = createNetwork(
        canvas,
        [],
        [],
        {
          // The labels do not scale with the zoom: a graph that is fitted
          // into a narrow pane would otherwise shrink its labels until
          // they cannot be read.
          scaling: { label: { enabled: false } },
          nodes: {
            shape: 'dot',
            font: { color: '#e6edf3', size: 11, face: 'monospace', multi: true },
            margin: { top: 6, right: 10, bottom: 6, left: 10 },
            borderWidth: 1,
          },
          edges: {
            color: { color: '#484f58' },
            smooth: { type: 'continuous' },
            arrows: { to: { enabled: true, scaleFactor: 0.6 } },
          },
          interaction: { hover: true, tooltipDelay: 120, navigationButtons: false, keyboard: false },
          physics: {
            enabled: true,
            stabilization: { enabled: true, iterations: 300, fit: true, updateInterval: 50 },
            barnesHut: { springLength: 150 },
          },
        },
      );
      network = created.network;
      nodeSet = created.nodes;
      edgeSet = created.edges;

      network.on('click', (params) => {
        const nodeIds = (params as { nodes?: string[] }).nodes ?? [];
        const selectedId = nodeIds[0] ?? null;
        useGraphStore.getState().select(selectedId);
        highlightNeighbourhood(selectedId);
      });
      network.on('doubleClick', (params) => {
        const nodeIds = (params as { nodes?: string[] }).nodes ?? [];
        const id = nodeIds[0];
        if (id) network?.focus(id, { scale: 1.3, animation: true });
      });

      renderData();
    })
    .catch((err: unknown) => {
      console.error('[aisai] the dependency graph could not be rendered', err);
      status.hidden = false;
      status.replaceChildren(
        h('h3', { text: 'The dependency graph could not be rendered' }),
        h('p', { text: err instanceof Error ? err.message : String(err) }),
      );
    });

  return {
    element: container,
    dispose: () => {
      destroyed = true;
      unsubscribe();
      network?.destroy();
      network = null;
    },
  };
}
