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
import { useAuditStore } from '../state/audit';
import { useUiStore } from '../state/ui';
import { findPath, useGraphStore } from '../state/graph';

/** The theme colours, read from the page so the graph follows light and dark. */
function themeColors(): { fg: string; muted: string; bg: string; raised: string; border: string; borderStrong: string; accent: string } {
  const css = getComputedStyle(document.documentElement);
  const v = (name: string, fallback: string): string => css.getPropertyValue(name).trim() || fallback;
  return {
    fg: v('--fg', '#dde8e3'),
    muted: v('--fg-muted', '#8ea5a0'),
    bg: v('--bg-base', '#0a1112'),
    raised: v('--bg-raised', '#152123'),
    border: v('--border', '#223336'),
    borderStrong: v('--border-strong', '#35494c'),
    accent: v('--accent', '#b4f23c'),
  };
}

/** Whether a package has anything to report. */
const hasIssues = (node: GraphNode): boolean => node.findingCount > 0 || node.vulnCount > 0;

function nodeColor(node: GraphNode): VisColorSet {
  const t = themeColors();
  // Clean packages are quiet; only the ones with findings or advisories carry colour.
  const ring = hasIssues(node) ? severityColor(node.maxSeverity) : node.root ? t.accent : t.borderStrong;
  const fill = hasIssues(node) ? severityFill(node.maxSeverity) : t.raised;
  return {
    background: fill,
    border: ring,
    highlight: { background: fill, border: t.accent },
    hover: { background: fill, border: t.accent },
  };
}

/** A small card shown on hover. */
function nodeTooltip(node: GraphNode): HTMLElement {
  return h(
    'div',
    { class: 'graph-tip' },
    h('strong', { text: node.name }),
    h('span', { class: 'graph-tip-version', text: node.version }),
    h('div', { class: 'graph-tip-meta', text: `${node.root ? 'package studied' : node.direct ? 'direct dependency' : 'transitive dependency'}` }),
    h('div', { class: 'graph-tip-meta', text: `${node.findingCount} finding${node.findingCount === 1 ? '' : 's'} · ${node.vulnCount} advisor${node.vulnCount === 1 ? 'y' : 'ies'}` }),
  );
}

function visNode(node: GraphNode): VisNodeData {
  const t = themeColors();
  // Size says how much a package matters: the studied one, then direct
  // dependencies, then by how much was found in it.
  const size = node.root ? 18 : (node.direct ? 10 : 6) + Math.min(6, node.findingCount + node.vulnCount);
  return {
    id: node.id,
    label: node.name,
    title: nodeTooltip(node) as unknown as string,
    shape: 'dot',
    size,
    color: nodeColor(node),
    borderWidth: node.root ? 3 : hasIssues(node) ? 2 : 1,
    font: {
      color: node.root || hasIssues(node) ? t.fg : t.muted,
      size: node.root ? 13 : 11,
      face: 'JetBrains Mono Variable, monospace',
      bold: node.root,
      strokeWidth: 4,
      strokeColor: t.bg,
    },
  };
}

function visEdge(from: string, to: string): VisEdgeData {
  const t = themeColors();
  return {
    id: `${from}->${to}`,
    from,
    to,
    color: { color: t.borderStrong, highlight: t.accent, hover: t.accent, opacity: 0.55 },
    width: 0.8,
    arrows: 'to',
    smooth: { type: 'curvedCW', roundness: 0.12 },
  };
}

export function download(filename: string, href: string): void {
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
export function exportPng(container: HTMLElement): string | null {
  const canvas = container.querySelector('canvas');
  if (!canvas) return null;
  const output = document.createElement('canvas');
  output.width = canvas.width;
  output.height = canvas.height;
  const context = output.getContext('2d');
  if (!context) return null;
  context.fillStyle = '#0a1112';
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
    '<defs><marker id="arrow" markerWidth="8" markerHeight="8" refX="8" refY="3" orient="auto"><path d="M0,0 L0,6 L8,3 z" fill="#8ea5a0"/></marker></defs>',
    '<rect x="-100000" y="-100000" width="200000" height="200000" fill="#0a1112"/>',
  ];

  for (const edge of edges) {
    const from = positions[edge.source];
    const to = positions[edge.target];
    if (!from || !to) continue;
    parts.push(
      `<line x1="${from.x}" y1="${from.y}" x2="${to.x}" y2="${to.y}" stroke="#35494c" stroke-width="1" marker-end="url(#arrow)"/>`,
    );
  }

  for (const node of nodes) {
    const point = positions[node.id];
    if (!point) continue;
    const radius = node.root ? 15 : node.direct ? 11 : 7;
    // The studied package is drawn as a five-pointed star.
    const star = Array.from({ length: 10 }, (_, i) => {
      const r = i % 2 === 0 ? radius : radius * 0.45;
      const angle = (Math.PI / 5) * i - Math.PI / 2;
      return `${(point.x + r * Math.cos(angle)).toFixed(1)},${(point.y + r * Math.sin(angle)).toFixed(1)}`;
    }).join(' ');
    parts.push(
      node.root
        ? `<polygon points="${star}" fill="${severityFill(node.maxSeverity)}" stroke="${severityColor(node.maxSeverity)}" stroke-width="3"/>`
        : `<circle cx="${point.x}" cy="${point.y}" r="${radius}" fill="${severityFill(node.maxSeverity)}" stroke="${severityColor(node.maxSeverity)}" stroke-width="${node.direct ? 2 : 1}"/>`,
      `<text x="${point.x}" y="${point.y - radius - 5}" fill="#dde8e3" font-family="monospace" font-size="11" text-anchor="middle">${node.name}</text>`,
      `<text x="${point.x}" y="${point.y - radius - 16}" fill="#8ea5a0" font-family="monospace" font-size="10" text-anchor="middle">v${node.version}</text>`,
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
    ...SEVERITIES.map((severity) =>
      h('span', { class: 'legend-item' }, h('span', { class: `legend-swatch ${severityClass(severity)}`, style: `background:${severityColor(severity)}` }), severity),
    ),
    h('span', { class: 'legend-item' }, h('span', { class: 'legend-swatch is-clean' }), 'nothing found'),
    h('span', { class: 'legend-item muted', text: 'bigger = studied, direct, or more findings' }),
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
        color: inPath ? nodeColor(node) : 'rgba(140,160,155,0.18)',
        borderWidth: inPath && hasPath ? 3 : node.root ? 3 : hasIssues(node) ? 2 : 1,
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
    // A folder's graph is only what its manifests declare. Say so, and say
    // when there is nothing to draw, instead of leaving a lone node unexplained.
    const local = useAuditStore.getState().current?.source === 'local';
    status.hidden = !(local && state.nodes.length <= 1);
    if (!status.hidden) {
      status.replaceChildren(
        h('p', {
          text:
            'No dependencies found in this folder. The graph reads requirements.txt, pyproject.toml, ' +
            'package.json, go.mod and Cargo.toml at the top level of the folder; other layouts are not supported yet.',
        }),
      );
    }

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
    network?.setOptions({ physics: { enabled: true } });
    network?.stabilize(300);

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
    const rootId = (state.nodes.find((node) => node.root) ?? state.nodes.find((node) => node.direct))?.id;
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

  // Colours come from the theme: redraw when it changes.
  const unsubscribeTheme = useUiStore.subscribe((state, previous) => {
    if (state.theme !== previous.theme || state.accent !== previous.accent) requestAnimationFrame(() => renderData());
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
          nodes: {
            scaling: { label: { enabled: false } },
            shape: 'dot',
            font: { size: 11, face: 'JetBrains Mono Variable, monospace' },
            borderWidth: 1,
            chosen: { label: false },
          },
          edges: {
            smooth: { type: 'curvedCW', roundness: 0.12 },
            arrows: { to: { enabled: true, scaleFactor: 0.35 } },
            selectionWidth: 1.5,
            hoverWidth: 0.6,
          },
          interaction: { hover: true, tooltipDelay: 80, navigationButtons: false, keyboard: false, zoomSpeed: 0.6 },
          // A calm force layout that settles and then stops, so the graph does
          // not keep drifting while it is read.
          physics: {
            enabled: true,
            solver: 'forceAtlas2Based',
            forceAtlas2Based: { gravitationalConstant: -38, centralGravity: 0.012, springLength: 85, springConstant: 0.07, damping: 0.6, avoidOverlap: 0.5 },
            stabilization: { enabled: true, iterations: 400, fit: true, updateInterval: 40 },
            minVelocity: 0.8,
          },
        },
      );
      network = created.network;
      // Once the layout has settled, stop the simulation and glide the view to fit.
      network.on('stabilizationIterationsDone', () => {
        network?.setOptions({ physics: { enabled: false } });
        network?.fit({ animation: { duration: 650, easingFunction: 'easeInOutQuad' } });
      });
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
      unsubscribeTheme();
      network?.destroy();
      network = null;
    },
  };
}
