/**
 * The dependency graph pane, drawn with vis-network
 * (https://visjs.github.io/vis-network/docs/network/).
 *
 * Packages are sized by importance (the audited package, then direct
 * dependencies, then by findings) and coloured by their worst severity;
 * packages with nothing found stay quiet. The layout is forceAtlas2Based
 * physics, stopped once it has settled so the picture does not drift. Above
 * it: search, severity filters, the "Why is this package here?" box, and
 * PNG or SVG export. The colours follow the app's theme.
 */

import type { AppController } from '../app';
import { SEVERITIES, type GraphNode, type Severity } from '../api/types';
import { h } from '../lib/dom';
import { icon } from '../lib/icons';
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
import { packageLabel, sortAdvisories, useAdvisoryStore } from '../state/advisories';
import { useUiStore } from '../state/ui';
import { dependentsOf, findPath, matchPackage, useGraphStore } from '../state/graph';

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
    node.transitiveVulns
      ? h('div', { class: 'graph-tip-meta graph-tip-warn', text: `${node.transitiveVulns} vulnerable package${node.transitiveVulns === 1 ? '' : 's'} further down` })
      : null,
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

  // "Why is this package here?": a combobox that understands what people
  // paste (requirement lines, package URLs, index pages), suggests names as
  // you type, and explains the chain that pulled the package in.
  const pathInput = h('input', {
    type: 'search',
    placeholder: 'Why is this package here? e.g. urllib3',
    'aria-label': 'Why is this package here? Type a package name',
    role: 'combobox',
    'aria-autocomplete': 'list',
    'aria-expanded': 'false',
    'aria-controls': 'why-suggestions',
    autocomplete: 'off',
    spellcheck: 'false',
  });
  const suggestionList = h('ul', { class: 'why-suggestions', id: 'why-suggestions', role: 'listbox', hidden: true, 'aria-label': 'Matching packages' });
  const pathResult = h('div', { class: 'why-result', role: 'status', 'aria-live': 'polite' });
  const whyBox = h('div', { class: 'why-box' }, pathInput, suggestionList);

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
    whyBox,
    pathResult,
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

  // The canvas cannot be reached with a keyboard or read by a screen reader, so
  // the same packages are also offered as a list of buttons. Choosing one does
  // what clicking its node does: selects it, highlights its neighbours and
  // brings it into view.
  /** Selects a package everywhere: the store, the highlight, the canvas. */
  const selectPackage = (id: string): void => {
    useGraphStore.getState().select(id);
    highlightNeighbourhood(id);
    network?.selectNodes([id]);
    network?.focus(id, { scale: 1.2, animation: true });
  };

  /* ---- vulnerable dependencies: what OSV knows about the locked packages,
     and the chain of packages that pulls each one in ---- */
  const vulnBody = h('div', { class: 'vuln-body' });
  const vulnCount = h('span', { class: 'badge neutral vuln-count', text: '' });
  const vulnBox = h('details', { class: 'vuln-box' },
    h('summary', { class: 'vuln-summary' }, 'Vulnerable dependencies ', vulnCount),
    vulnBody,
  );
  let vulnTouched = false;
  vulnBox.addEventListener('toggle', () => {
    vulnTouched = true;
  });

  /** A package name in a chain, as a chip that selects it in the graph. */
  const chainChip = (name: string): HTMLElement => {
    const node = useGraphStore.getState().nodes.find((n) => n.name === name);
    return h('button', {
      class: 'why-chip',
      type: 'button',
      title: node ? `Show ${name} in the graph` : name,
      disabled: !node || undefined,
      onclick: () => node && selectPackage(node.id),
    }, name);
  };

  const renderVulns = (): void => {
    const adv = useAdvisoryStore.getState();
    const audit = useAuditStore.getState().current;
    const nodes = useGraphStore.getState().nodes;
    const local = audit?.source === 'local';
    vulnBox.hidden = !audit || audit.sourcesDeleted === true || nodes.length === 0;
    if (vulnBox.hidden) return;
    const unpinned = nodes.filter((n) => !n.root).length;
    vulnCount.textContent = adv.status === 'ready' ? String(adv.list.length) : '';
    vulnCount.hidden = adv.status !== 'ready';
    vulnCount.className = `badge vuln-count ${adv.list.length ? 'bad' : 'neutral'}`;

    const check = (label: string): HTMLElement =>
      h('button', { class: 'tiny primary', type: 'button', onclick: () => void _controller.checkDependencies() }, label);

    if (adv.status === 'checking') {
      vulnBody.replaceChildren(h('p', { class: 'vuln-note', role: 'status' }, h('span', { class: 'spinner', 'aria-hidden': 'true' }), ' Checking the dependencies against the advisory database…'));
      return;
    }
    if (adv.status === 'error') {
      vulnBody.replaceChildren(h('p', { class: 'vuln-note is-error', role: 'alert', text: `The check failed: ${adv.error ?? 'unknown error'}` }), check('Try again'));
      return;
    }
    if (adv.status === 'unknown') {
      vulnBody.replaceChildren(
        h('p', { class: 'vuln-note', text: local
          ? `Not checked yet. This looks up the ${unpinned} locked package${unpinned === 1 ? '' : 's'} (transitive ones included) in the OSV database. It sends their names and versions to osv.dev; your code is not sent.`
          : 'The advisory lookup runs after the pull. Check again if it did not finish.' }),
        check('Check for vulnerabilities'),
      );
      return;
    }
    if (!adv.list.length) {
      vulnBody.replaceChildren(h('p', { class: 'vuln-note', text: `No known vulnerabilities in ${adv.checked} checked package${adv.checked === 1 ? '' : 's'}. This covers advisories in OSV, not unknown flaws.` }), check('Check again'));
      return;
    }
    if (!vulnTouched) vulnBox.open = true;
    vulnBody.replaceChildren(
      h('ul', { class: 'vuln-list' },
        ...sortAdvisories(adv.list).map((a) => {
          const via = a.via ?? [];
          const transitive = via.length > 1;
          const target = nodes.find((n) => n.id === a.package);
          return h('li', { class: 'vuln-item' },
            h('div', { class: 'vuln-head' },
              h('span', { class: `badge ${severityClass(a.severity)}`, text: severityLabel(a.severity) }),
              a.url && /^https?:\/\//.test(a.url)
                ? h('button', { class: 'link-button vuln-id', type: 'button', title: 'Open the advisory in your browser', onclick: () => void window.aisai.openExternal(a.url as string) }, a.id)
                : h('span', { class: 'vuln-id', text: a.id }),
              h('button', {
                class: 'vuln-pkg',
                type: 'button',
                disabled: !target || undefined,
                title: target ? 'Show it in the graph' : '',
                onclick: () => target && selectPackage(target.id),
              }, packageLabel(a.package)),
            ),
            a.summary ? h('div', { class: 'vuln-summary-text', text: a.summary }) : null,
            via.length
              ? h('div', { class: 'vuln-via' },
                  h('span', { class: 'muted', text: transitive ? 'Pulled in by ' : 'Direct dependency' }),
                  ...(transitive
                    ? via.flatMap((name, i) => [i ? h('span', { class: 'why-arrow', 'aria-label': 'which needs', text: '→' }) : '', chainChip(name)])
                    : []),
                )
              : null,
            transitive ? h('div', { class: 'vuln-fix muted', text: `Update ${via[0]}, which brings it in, to a version that depends on a fixed ${via[via.length - 1]}.` }) : null,
            a.fixedIn?.length ? h('div', { class: 'vuln-fix muted', text: `Fixed in ${a.fixedIn.slice(0, 4).join(', ')}` }) : null,
          );
        }),
      ),
      check('Check again'),
    );
  };
  const unsubscribeVulns = useAdvisoryStore.subscribe(renderVulns);
  const unsubscribeVulnGraph = useGraphStore.subscribe((state, previous) => {
    if (state.nodes !== previous.nodes) renderVulns();
  });
  const unsubscribeVulnAudit = useAuditStore.subscribe((state, previous) => {
    if (state.current !== previous.current) {
      vulnTouched = false;
      vulnBox.open = false;
      renderVulns();
    }
  });

  const listBody = h('ul', { class: 'graph-list', 'aria-label': 'Packages in the dependency graph' });
  const listBox = h('details', { class: 'graph-list-box' },
    h('summary', { class: 'graph-list-summary', text: 'Package list (keyboard and screen reader access)' }),
    listBody,
  );
  const renderPackageList = (): void => {
    if (!listBox.open) return;
    const { nodes, selectedId } = useGraphStore.getState();
    const ranked = [...nodes].sort((a, b) => Number(Boolean(b.root)) - Number(Boolean(a.root)) || Number(b.direct) - Number(a.direct) || b.findingCount - a.findingCount || a.name.localeCompare(b.name));
    listBody.replaceChildren(
      ...ranked.map((node) => {
        const kind = node.root ? 'audited package' : node.direct ? 'direct dependency' : 'indirect dependency';
        const findings = node.findingCount ? `${node.findingCount} finding${node.findingCount === 1 ? '' : 's'}, worst ${node.maxSeverity}` : 'no findings';
        return h('li', {},
          h('button', {
            class: 'graph-list-item',
            type: 'button',
            'aria-pressed': String(node.id === selectedId),
            // Spoken in full, since the row only shows the short form.
            'aria-label': `${node.name}${node.version && node.version !== 'local' ? ` ${node.version}` : ''}, ${kind}, ${findings}`,
            onclick: () => selectPackage(node.id),
          },
            h('span', { class: 'graph-list-name', text: node.name }),
            h('span', { class: 'graph-list-kind', text: node.root ? 'audited' : node.direct ? 'direct' : 'indirect' }),
            node.transitiveVulns && !node.root ? h('span', { class: 'badge warn', title: `${node.transitiveVulns} vulnerable package${node.transitiveVulns === 1 ? '' : 's'} further down`, text: `${node.transitiveVulns} below` }) : null,
            node.findingCount ? h('span', { class: `badge ${severityClass(node.maxSeverity)}`, text: String(node.findingCount) }) : null,
          ),
        );
      }),
    );
  };
  listBox.addEventListener('toggle', renderPackageList);
  const unsubscribeList = useGraphStore.subscribe((state, previous) => {
    if (state.nodes !== previous.nodes || state.selectedId !== previous.selectedId) renderPackageList();
  });

  canvas.dataset.tour = 'graph';
  whyBox.dataset.tour = 'why';
  container.append(controls, canvas, status, vulnBox, listBox, legend);
  renderVulns();

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

  const nameOf = (id: string): string => useGraphStore.getState().nodes.find((n) => n.id === id)?.name ?? id;

  /** A package as a chip that shows it in the graph. */
  const packageChip = (id: string): HTMLElement =>
    h('button', {
      class: 'why-chip',
      type: 'button',
      title: 'Show it in the graph',
      onclick: () => {
        network?.selectNodes([id]);
        network?.focus(id, { scale: 1.2, animation: true });
      },
    }, nameOf(id));

  /** Shows why a package is in the audit: the chain from the root to it. */
  const explain = (target: GraphNode): void => {
    const state = useGraphStore.getState();
    const root = state.nodes.find((node) => node.root) ?? state.nodes.find((node) => node.direct);
    const path = findPath(state.nodes, state.edges, target.id, root?.id);
    state.setPath(target.id, path);
    applyFilters();
    const others = dependentsOf(state.edges, target.id).filter((id) => id !== path[path.length - 2]);
    const also = others.length
      ? [h('p', { class: 'why-also' }, `Also required by `, ...others.slice(0, 6).flatMap((id, i) => [i ? ', ' : '', packageChip(id)]), others.length > 6 ? ` and ${others.length - 6} more.` : '.')]
      : [];
    const clear = h('button', { class: 'ghost tiny icon-button why-clear', type: 'button', title: 'Clear', 'aria-label': 'Clear the answer', onclick: () => resetWhy() }, icon('x'));
    if (target.root || path.length === 1) {
      pathResult.replaceChildren(h('p', {}, h('strong', { text: target.name }), ' is the package this audit is about.'), clear);
    } else if (!path.length) {
      pathResult.replaceChildren(
        h('p', {}, h('strong', { text: target.name }), ` is in this audit, but nothing links it to ${root?.name ?? 'the root package'}. It may be optional, platform-specific or declared by a manifest without a resolved parent.`),
        ...also,
        clear,
      );
    } else {
      const chain = path.flatMap((id, i) => [i ? h('span', { class: 'why-arrow', 'aria-label': 'depends on', text: '→' }) : '', packageChip(id)]);
      pathResult.replaceChildren(
        h('p', {},
          h('strong', { text: target.name }),
          path.length === 2 ? ` is a direct dependency of ${nameOf(path[0])}.` : ` is here because ${nameOf(path[0])} needs it through ${path.length - 2} other package${path.length === 3 ? '' : 's'}:`,
        ),
        h('div', { class: 'why-chain' }, ...chain),
        ...also,
        clear,
      );
    }
    if (network) {
      network.selectNodes([target.id]);
      network.focus(target.id, { scale: 1.2, animation: true });
    }
  };

  let active = -1;
  let offered: GraphNode[] = [];

  const closeList = (): void => {
    suggestionList.hidden = true;
    pathInput.setAttribute('aria-expanded', 'false');
    pathInput.removeAttribute('aria-activedescendant');
    active = -1;
  };

  const choose = (node: GraphNode): void => {
    pathInput.value = node.name;
    closeList();
    explain(node);
  };

  const renderList = (items: GraphNode[], heading: string): void => {
    offered = items;
    active = -1;
    if (!items.length) {
      closeList();
      return;
    }
    suggestionList.replaceChildren(
      h('li', { class: 'why-heading', role: 'presentation', text: heading }),
      ...items.map((node, i) =>
        h('li', {
          class: 'why-option',
          id: `why-option-${i}`,
          role: 'option',
          'aria-selected': 'false',
          // mousedown, so the click lands before the input loses focus
          onmousedown: (event: Event) => {
            event.preventDefault();
            choose(node);
          },
        },
          h('span', { class: 'why-option-name', text: node.name }),
          node.version && node.version !== 'local' ? h('span', { class: 'why-option-version', text: node.version }) : null,
          h('span', { class: 'grow' }),
          node.findingCount ? h('span', { class: `badge ${severityClass(node.maxSeverity)}`, text: `${node.findingCount}` }) : null,
          h('span', { class: 'why-option-kind', text: node.root ? 'audited' : node.direct ? 'direct' : 'indirect' }),
        ),
      ),
    );
    suggestionList.hidden = false;
    pathInput.setAttribute('aria-expanded', 'true');
  };

  const highlight = (index: number): void => {
    const options = Array.from(suggestionList.querySelectorAll<HTMLElement>('.why-option'));
    if (!options.length) return;
    active = (index + options.length) % options.length;
    options.forEach((el, i) => el.setAttribute('aria-selected', String(i === active)));
    pathInput.setAttribute('aria-activedescendant', options[active].id);
    options[active].scrollIntoView({ block: 'nearest' });
  };

  /** With nothing typed: the indirect packages, the usual "why is this here?" ones. */
  const offerIndirect = (): void => {
    const nodes = useGraphStore.getState().nodes.filter((n) => !n.direct && !n.root);
    const ranked = [...nodes].sort((a, b) => b.findingCount - a.findingCount || a.name.localeCompare(b.name)).slice(0, 8);
    renderList(ranked, ranked.length ? 'Pulled in indirectly' : '');
  };

  const onType = (): void => {
    const query = pathInput.value;
    if (!query.trim()) {
      offerIndirect();
      return;
    }
    const result = matchPackage(useGraphStore.getState().nodes, query);
    renderList(result.suggestions, result.fuzzy ? 'Did you mean' : 'Packages');
  };

  const resetWhy = (): void => {
    pathInput.value = '';
    closeList();
    useGraphStore.getState().setPath(null, []);
    pathResult.replaceChildren();
    applyFilters();
  };

  const runPathFinder = (): void => {
    const query = pathInput.value;
    if (!query.trim()) {
      resetWhy();
      return;
    }
    const state = useGraphStore.getState();
    const result = matchPackage(state.nodes, query);
    if (result.match) {
      choose(result.match);
      return;
    }
    state.setPath(null, []);
    applyFilters();
    if (result.suggestions.length && !result.fuzzy) {
      pathResult.replaceChildren(h('p', {}, `Several packages match “${result.name}”. Pick one:`), h('div', { class: 'why-chain' }, ...result.suggestions.map((n) => h('button', { class: 'why-chip', type: 'button', onclick: () => choose(n) }, n.name))));
    } else if (result.fuzzy) {
      pathResult.replaceChildren(h('p', {}, `No package called “${result.name}” in this audit. Did you mean `, ...result.suggestions.slice(0, 4).flatMap((n, i) => [i ? ', ' : '', h('button', { class: 'why-chip', type: 'button', onclick: () => choose(n) }, n.name)]), '?'));
    } else {
      pathResult.replaceChildren(h('p', {}, `No package called “${result.name || query.trim()}” in this audit. It may not be a dependency at all, or it may come from a part of the project AISAI did not resolve. Try part of the name.`));
    }
  };

  pathInput.addEventListener('input', onType);
  pathInput.addEventListener('focus', onType);
  pathInput.addEventListener('blur', () => window.setTimeout(closeList, 120));
  pathInput.addEventListener('keydown', (event) => {
    if (event.key === 'ArrowDown') {
      event.preventDefault();
      if (suggestionList.hidden) onType();
      highlight(active + 1);
    } else if (event.key === 'ArrowUp') {
      event.preventDefault();
      highlight(active - 1);
    } else if (event.key === 'Enter') {
      event.preventDefault();
      if (active >= 0 && offered[active]) choose(offered[active]);
      else runPathFinder();
    } else if (event.key === 'Escape') {
      if (!suggestionList.hidden) {
        event.preventDefault();
        closeList();
      }
    }
  });
  // The search field's own clear button fires "search" with an empty value.
  pathInput.addEventListener('search', () => {
    if (!pathInput.value) resetWhy();
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
      unsubscribeList();
      unsubscribeVulns();
      unsubscribeVulnGraph();
      unsubscribeVulnAudit();
      unsubscribe();
      unsubscribeTheme();
      network?.destroy();
      network = null;
    },
  };
}
