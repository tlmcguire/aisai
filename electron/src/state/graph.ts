import { create } from 'zustand';

import type { Graph, GraphEdge, GraphNode, Severity } from '../api/types';

export interface GraphState {
  nodes: GraphNode[];
  edges: GraphEdge[];
  selectedId: string | null;
  search: string;
  /** Active severity filter. Empty means "show everything". */
  severities: Severity[];
  /** PURL the path finder is looking for. */
  pathTarget: string | null;
  /** PURLs on the highlighted root → target path. */
  path: string[];
  cycleCount: number;
  setGraph: (graph: Graph) => void;
  select: (id: string | null) => void;
  setSearch: (search: string) => void;
  toggleSeverity: (severity: Severity) => void;
  clearSeverities: () => void;
  setPath: (target: string | null, path: string[]) => void;
}

/**
 * Find a path from the root package to `target` following dependency edges
 * (source depends on target). Returns the PURLs on the path, root first.
 */
export function findPath(
  nodes: GraphNode[],
  edges: GraphEdge[],
  target: string,
  rootId?: string,
): string[] {
  const byId = new Map(nodes.map((n) => [n.id, n]));
  if (!byId.has(target)) return [];

  const adjacency = new Map<string, string[]>();
  for (const edge of edges) {
    const list = adjacency.get(edge.source) ?? [];
    list.push(edge.target);
    adjacency.set(edge.source, list);
  }

  const incoming = new Map<string, string[]>();
  for (const edge of edges) {
    const list = incoming.get(edge.target) ?? [];
    list.push(edge.source);
    incoming.set(edge.target, list);
  }

  const root =
    rootId && byId.has(rootId)
      ? rootId
      : (nodes.find((n) => !incoming.has(n.id))?.id ?? nodes[0]?.id);

  if (!root) return [];

  // Walk backwards from the target to the root (dependencies point downwards).
  const queue: string[] = [target];
  const seen = new Set<string>([target]);
  const parent = new Map<string, string>();

  while (queue.length) {
    const current = queue.shift() as string;
    if (current === root) break;
    for (const next of incoming.get(current) ?? []) {
      if (seen.has(next)) continue;
      seen.add(next);
      parent.set(current, next);
      queue.push(next);
    }
  }

  if (!seen.has(root)) return [];

  const path: string[] = [target];
  let cursor = target;
  while (cursor !== root) {
    const next = parent.get(cursor);
    if (!next) return [];
    path.unshift(next);
    cursor = next;
  }
  return path;
}

/** Count dependency cycles (self loops and simple 2-cycles). */
export function countCycles(edges: GraphEdge[]): number {
  let cycles = 0;
  const seenPairs = new Set<string>();
  for (const edge of edges) {
    if (edge.source === edge.target) {
      cycles += 1;
      continue;
    }
    const key = [edge.source, edge.target].sort().join('|');
    if (seenPairs.has(key)) {
      cycles += 1;
    } else {
      seenPairs.add(key);
    }
  }
  return cycles;
}

export const useGraphStore = create<GraphState>((set) => ({
  nodes: [],
  edges: [],
  selectedId: null,
  search: '',
  severities: [],
  pathTarget: null,
  path: [],
  cycleCount: 0,
  setGraph: (graph) =>
    set({
      nodes: graph.nodes ?? [],
      edges: graph.edges ?? [],
      cycleCount: countCycles(graph.edges ?? []),
    }),
  select: (selectedId) => set({ selectedId }),
  setSearch: (search) => set({ search }),
  toggleSeverity: (severity) =>
    set((state) => ({
      severities: state.severities.includes(severity)
        ? state.severities.filter((s) => s !== severity)
        : [...state.severities, severity],
    })),
  clearSeverities: () => set({ severities: [] }),
  setPath: (pathTarget, path) => set({ pathTarget, path }),
}));
