import { create } from 'zustand';

import type { AST, FileContent, FileNode, Patch } from '../api/types';

export interface WorkspaceState {
  tree: FileNode | null;
  file: FileContent | null;
  ast: AST | null;
  patch: Patch | null;
  /** Expanded directory paths in the file tree. */
  expanded: string[];
  loading: boolean;
  error: string | null;
  /** Transient request to scroll the code pane to a line. */
  jumpLine: number | null;
  /** Increments on every jump so repeated jumps to the same line still fire. */
  jumpToken: number;
  setTree: (tree: FileNode | null) => void;
  setFile: (file: FileContent | null) => void;
  setAst: (ast: AST | null) => void;
  setPatch: (patch: Patch | null) => void;
  toggleExpanded: (path: string) => void;
  expand: (paths: string[]) => void;
  setLoading: (loading: boolean) => void;
  setError: (error: string | null) => void;
  requestJump: (line: number) => void;
  clearJump: () => void;
}

export const useWorkspaceStore = create<WorkspaceState>((set) => ({
  tree: null,
  file: null,
  ast: null,
  patch: null,
  expanded: [],
  loading: false,
  error: null,
  jumpLine: null,
  jumpToken: 0,
  setTree: (tree) => set({ tree }),
  setFile: (file) => set({ file }),
  setAst: (ast) => set({ ast }),
  setPatch: (patch) => set({ patch }),
  toggleExpanded: (path) =>
    set((state) => ({
      expanded: state.expanded.includes(path)
        ? state.expanded.filter((p) => p !== path)
        : [...state.expanded, path],
    })),
  expand: (paths) => set((state) => ({ expanded: Array.from(new Set([...state.expanded, ...paths])) })),
  setLoading: (loading) => set({ loading }),
  setError: (error) => set({ error }),
  requestJump: (line) =>
    set((state) => ({ jumpLine: line, jumpToken: state.jumpToken + 1 })),
  clearJump: () => set({ jumpLine: null }),
}));
