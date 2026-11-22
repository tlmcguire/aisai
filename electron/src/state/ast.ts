/**
 * The AST view's own settings: which of its two views is shown, how deep
 * the tree goes and how many nodes the graph draws.
 *
 * They live in a store so the pane and the expanded graph window show the
 * same tree: one depth, one drawing limit, whichever is looking at it.
 */

import { create } from 'zustand';

export type AstMode = 'tree' | 'graph';

/** How many more nodes the graph draws each time the limit is raised. */
export const AST_CAP_STEP = 500;
/** The most nodes the graph ever draws. */
export const AST_CAP_MAX = 3000;

export interface AstState {
  mode: AstMode;
  /** How deep the tree goes; Infinity shows every node. */
  depthLimit: number;
  /** The most nodes the graph draws. */
  nodeCap: number;
  setMode: (mode: AstMode) => void;
  setDepthLimit: (depth: number) => void;
  /** Draws more nodes, up to the maximum. */
  raiseCap: () => void;
}

export const useAstStore = create<AstState>((set) => ({
  mode: 'tree',
  depthLimit: 4,
  nodeCap: AST_CAP_STEP,
  setMode: (mode) => set({ mode }),
  setDepthLimit: (depthLimit) => set({ depthLimit }),
  raiseCap: () =>
    set((state) => ({ nodeCap: Math.min(AST_CAP_MAX, state.nodeCap + AST_CAP_STEP) })),
}));
