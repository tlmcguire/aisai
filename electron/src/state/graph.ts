/**
 * The dependency graph of the open audit, its filters, and the questions
 * asked of it: the path that pulled a package in (findPath), the packages
 * that depend on one (dependentsOf), and which package a typed or pasted
 * query means (matchPackage). The functions are pure, so they are tested
 * without a canvas (test/units.test.ts); the store is zustand
 * (https://zustand.docs.pmnd.rs/).
 */

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
  if (root === target) return [target];

  // Breadth-first search backwards from the target, along "is depended on
  // by" edges, so the first time the root is reached is by a shortest
  // chain. towardTarget[x] is the package one step closer to the target on
  // the chain that first reached x; each package gets it once, when first
  // seen, so the chain can always be walked back.
  const towardTarget = new Map<string, string>();
  const seen = new Set<string>([target]);
  const queue: string[] = [target];
  while (queue.length) {
    const current = queue.shift() as string;
    if (current === root) break;
    for (const dependent of incoming.get(current) ?? []) {
      if (seen.has(dependent)) continue;
      seen.add(dependent);
      towardTarget.set(dependent, current);
      queue.push(dependent);
    }
  }
  if (!seen.has(root)) return [];

  const path: string[] = [root];
  let cursor = root;
  while (cursor !== target) {
    const next = towardTarget.get(cursor);
    if (!next) return [];
    path.push(next);
    cursor = next;
  }
  return path;
}

/** The packages that depend on this one directly (its parents in the graph). */
export function dependentsOf(edges: GraphEdge[], id: string): string[] {
  return Array.from(new Set(edges.filter((e) => e.target === id && e.source !== id).map((e) => e.source)));
}

/**
 * A package name as typed, reduced to the name alone: what people paste is
 * often a requirement line ("urllib3>=2.0; python_version<'3.12'"), a
 * package URL ("pkg:pypi/urllib3@2.2.1"), an index page
 * ("https://pypi.org/project/urllib3/"), a name with extras
 * ("requests[socks]") or a name@version ("lodash@4.17.21").
 */
export function packageNameFromQuery(raw: string): string {
  let q = raw.trim().replace(/^['"`]+|['"`]+$/g, '');
  // An index or registry page.
  const page = /(?:pypi\.org\/project|npmjs\.com\/package|crates\.io\/crates|pkg\.go\.dev)\/((?:@[^/]+\/)?[^/?#]+)/i.exec(q);
  if (page) q = decodeURIComponent(page[1]);
  // A package URL: pkg:type/namespace/name@version?qualifiers
  const purl = /^pkg:[a-z0-9.+-]+\/(.+)$/i.exec(q);
  if (purl) {
    q = purl[1].split(/[?#]/)[0];
    q = q.replace(/@[^@/]*$/, '');
    const parts = q.split('/');
    q = parts.length > 1 && parts[0].startsWith('%40') ? `@${parts.slice(-2).join('/').replace(/^%40/, '')}` : parts[parts.length - 1];
    q = decodeURIComponent(q);
  }
  q = q.split(';')[0]; // environment markers
  q = q.replace(/\[.*?\]/g, ''); // extras
  q = q.split(/\s*(?:===|==|>=|<=|~=|!=|>|<|\^|~)\s*/)[0]; // version specifiers
  // name@version, but keep the leading @ of an npm scope.
  const at = q.lastIndexOf('@');
  if (at > 0) q = q.slice(0, at);
  return q.trim().split(/\s+/)[0] ?? '';
}

/**
 * The form names are compared in: lower case, with runs of "-", "_" and "."
 * counting as one "-", the way Python package names are normalised
 * (https://packaging.python.org/en/latest/specifications/name-normalization/).
 */
export function canonicalName(name: string): string {
  return name.toLowerCase().replace(/[-_.]+/g, '-');
}

/** Edit distance between two short strings (Levenshtein), for "did you mean". */
function editDistance(a: string, b: string): number {
  if (a === b) return 0;
  const row = Array.from({ length: b.length + 1 }, (_, i) => i);
  for (let i = 1; i <= a.length; i++) {
    let previous = row[0];
    row[0] = i;
    for (let j = 1; j <= b.length; j++) {
      const old = row[j];
      row[j] = Math.min(row[j] + 1, row[j - 1] + 1, previous + (a[i - 1] === b[j - 1] ? 0 : 1));
      previous = old;
    }
  }
  return row[b.length];
}

export interface PackageMatch {
  /** The name the query was reduced to. */
  name: string;
  /** The package meant, when one is clearly meant. */
  match: GraphNode | null;
  /** Packages to offer: the best matches first, or close spellings. */
  suggestions: GraphNode[];
  /** True when the suggestions are only similar spellings ("did you mean"). */
  fuzzy: boolean;
}

/**
 * Finds the package a query means. An exact name wins; then a single
 * prefix or substring match; otherwise the candidates are offered, and when
 * nothing contains the query, names within a couple of typos are.
 */
export function matchPackage(nodes: GraphNode[], query: string, limit = 8): PackageMatch {
  const name = packageNameFromQuery(query);
  const want = canonicalName(name);
  if (!want) return { name, match: null, suggestions: [], fuzzy: false };
  const scored = nodes.map((node) => {
    const have = canonicalName(node.name);
    let score = Infinity;
    if (have === want) score = 0;
    else if (have.startsWith(want)) score = 1 + (have.length - want.length) / 100;
    else if (have.includes(want)) score = 2 + have.indexOf(want) / 100;
    return { node, score };
  });
  const hits = scored.filter((s) => s.score < Infinity).sort((a, b) => a.score - b.score || Number(b.node.direct) - Number(a.node.direct));
  if (hits.length) {
    const exact = hits[0].score === 0 ? hits[0].node : hits.length === 1 ? hits[0].node : null;
    return { name, match: exact, suggestions: hits.slice(0, limit).map((s) => s.node), fuzzy: false };
  }
  const tolerance = want.length <= 4 ? 1 : 2;
  const close = nodes
    .map((node) => ({ node, distance: editDistance(canonicalName(node.name), want) }))
    .filter((c) => c.distance <= tolerance)
    .sort((a, b) => a.distance - b.distance);
  return { name, match: null, suggestions: close.slice(0, limit).map((c) => c.node), fuzzy: close.length > 0 };
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
