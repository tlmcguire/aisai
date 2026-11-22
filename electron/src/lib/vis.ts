/**
 * vis-network integration.
 *
 * The library is vendored (web/vis-9.1.2) — it is NOT installed from npm.
 * `vis-network.min.js` is a UMD bundle, so it is loaded as a classic script
 * (not an ES module) and exposed on `window.vis`. `web/bindings/utils.js` is
 * the matching neighbourhood-highlight helper; it is loaded the same way and
 * expects the globals `nodes`, `edges`, `network` and `nodeColors`.
 */

import '../vendor/vis-network.css';

export interface VisColorSet {
  background: string;
  border: string;
  highlight?: { background: string; border: string };
  hover?: { background: string; border: string };
}

export interface VisNodeData {
  id: string;
  label?: string;
  title?: string;
  shape?: string;
  color?: VisColorSet | string;
  hidden?: boolean;
  borderWidth?: number;
  font?: { color?: string; size?: number; face?: string; multi?: boolean; bold?: boolean };
  value?: number;
  level?: number;
  size?: number;
  widthConstraint?: { maximum?: number; minimum?: number };
  fixed?: boolean;
}

export interface VisEdgeData {
  id?: string;
  from: string;
  to: string;
  title?: string;
  color?: { color?: string; highlight?: string; hover?: string; opacity?: number };
  width?: number;
  dashes?: boolean;
  arrows?: string;
  hidden?: boolean;
  smooth?: boolean | { type?: string; roundness?: number };
}

export interface VisDataSet<T> {
  get(): T[];
  add(items: T[]): void;
  update(items: T[]): void;
  clear(): void;
}

export interface VisNetwork {
  setData(data: { nodes: VisNodeData[]; edges: VisEdgeData[] }): void;
  setOptions(options: Record<string, unknown>): void;
  on(event: string, handler: (params: unknown) => void): void;
  /**
   * Node/edge accessors were removed in vis-network 9 — read from the
   * DataSets instead.
   */
  getPositions(ids: string[]): Record<string, { x: number; y: number }>;
  getConnectedNodes(id: string): string[];
  getSelectedNodes(): Array<{ id: string }>;
  selectNodes(ids: string[]): void;
  stabilize(iterations?: number): void;
  focus(id: string, options?: { scale?: number; animation?: boolean }): void;
  fit(options?: { animation?: boolean }): void;
  redraw(): void;
  destroy(): void;
}

interface VisNamespace {
  Network: new (
    container: HTMLElement,
    data: { nodes: VisDataSet<VisNodeData>; edges: VisDataSet<VisEdgeData> },
    options?: Record<string, unknown>,
  ) => VisNetwork;
  DataSet: new <T>(items?: T[]) => VisDataSet<T>;
}

declare global {
  interface Window {
    vis?: VisNamespace;
    /** Globals expected by the vendored web/bindings/utils.js helper. */
    nodes?: VisDataSet<VisNodeData>;
    edges?: VisDataSet<VisEdgeData>;
    network?: VisNetwork;
    nodeColors?: Record<string, VisColorSet>;
    neighbourhoodHighlight?: (params: { nodes: string[] }) => void;
    selectNode?: (ids: string[]) => string[];
  }
}

let loadPromise: Promise<void> | null = null;

function loadScript(src: string): Promise<void> {
  return new Promise<void>((resolve, reject) => {
    const existing = document.querySelector<HTMLScriptElement>(`script[data-aisai-src="${src}"]`);
    if (existing) {
      if (existing.dataset.loaded === 'true') {
        resolve();
        return;
      }
      existing.addEventListener('load', () => resolve());
      existing.addEventListener('error', () => reject(new Error(`failed to load ${src}`)));
      return;
    }
    const script = document.createElement('script');
    script.src = src;
    script.async = false;
    script.dataset.aisaiSrc = src;
    script.addEventListener('load', () => {
      script.dataset.loaded = 'true';
      resolve();
    });
    script.addEventListener('error', () => reject(new Error(`failed to load ${src}`)));
    document.head.appendChild(script);
  });
}

/** Load the vendored vis-network bundle and the neighbourhood helper. */
export function loadVis(): Promise<void> {
  if (loadPromise) return loadPromise;
  loadPromise = (async () => {
    const visUrl = new URL('../vendor/vis-network.min.js', import.meta.url).href;
    const utilsUrl = new URL('../vendor/utils.js', import.meta.url).href;
    await loadScript(visUrl);
    await loadScript(utilsUrl);
    if (!window.vis?.Network) {
      throw new Error('vis-network did not expose the expected global API');
    }
  })().catch((err: unknown) => {
    loadPromise = null;
    throw err instanceof Error ? err : new Error(String(err));
  });
  return loadPromise;
}

/** Create a network instance backed by DataSets wired to the vendored helper. */
export function createNetwork(
  container: HTMLElement,
  nodes: VisNodeData[],
  edges: VisEdgeData[],
  options: Record<string, unknown>,
): { network: VisNetwork; nodes: VisDataSet<VisNodeData>; edges: VisDataSet<VisEdgeData> } {
  const vis = window.vis;
  if (!vis) throw new Error('vis-network is not loaded');

  const nodeSet = new vis.DataSet<VisNodeData>(nodes);
  const edgeSet = new vis.DataSet<VisEdgeData>(edges);

  // The vendored utils.js helper reads these globals.
  window.nodes = nodeSet;
  window.edges = edgeSet;
  window.nodeColors = {};
  for (const node of nodes) {
    if (node.color && typeof node.color !== 'string') {
      window.nodeColors[node.id] = node.color;
    }
  }

  // The DataSets have to be handed to the network, otherwise the network keeps
  // its own copies and later updates to these sets are ignored.
  const network = new vis.Network(container, { nodes: nodeSet, edges: edgeSet }, options);
  window.network = network;

  return { network, nodes: nodeSet, edges: edgeSet };
}

/**
 * Dim everything except the selected node and its neighbours.
 *
 * Uses the vendored `web/bindings/utils.js` implementation when available and
 * falls back to an equivalent typed implementation otherwise, so a failure in
 * the legacy helper can never leave the graph in a broken state.
 */
export function highlightNeighbourhood(selectedId: string | null): void {
  if (!window.network) return;

  const helper = window.neighbourhoodHighlight;
  if (typeof helper === 'function') {
    try {
      helper({ nodes: selectedId ? [selectedId] : [] });
      return;
    } catch {
      /* fall through to the typed implementation */
    }
  }
  fallbackHighlight(selectedId);
}

function fallbackHighlight(selectedId: string | null): void {
  const network = window.network;
  const nodeSet = window.nodes;
  if (!network || !nodeSet) return;

  const all = nodeSet.get();
  if (!selectedId) {
    for (const node of all) {
      const color = window.nodeColors?.[node.id];
      if (color) node.color = color;
      node.hidden = false;
    }
    nodeSet.update(all);
    return;
  }

  const keep = new Set<string>([selectedId]);
  for (const neighbour of network.getConnectedNodes(selectedId)) keep.add(neighbour);

  for (const node of all) {
    const color = window.nodeColors?.[node.id];
    if (keep.has(node.id)) {
      if (color) node.color = color;
    } else {
      node.color = 'rgba(200,200,200,0.35)';
    }
  }
  nodeSet.update(all);
}
