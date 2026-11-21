import { create } from 'zustand';

import type { Finding, FindingState, Severity } from '../api/types';

export type FindingSortKey = 'severity' | 'confidence' | 'cwe' | 'file' | 'tool';

export interface FindingsState {
  findings: Finding[];
  total: number;
  loading: boolean;
  error: string | null;
  search: string;
  severities: Severity[];
  cwes: string[];
  tools: string[];
  packages: string[];
  states: FindingState[];
  showSuppressed: boolean;
  sortKey: FindingSortKey;
  sortDir: 'asc' | 'desc';
  selectedId: string | null;
  setFindings: (findings: Finding[], total: number) => void;
  setLoading: (loading: boolean) => void;
  setError: (error: string | null) => void;
  setSearch: (search: string) => void;
  toggleSeverity: (severity: Severity) => void;
  toggleCwe: (cwe: string) => void;
  toggleTool: (tool: string) => void;
  togglePackage: (pkg: string) => void;
  toggleState: (state: FindingState) => void;
  setShowSuppressed: (show: boolean) => void;
  setSort: (key: FindingSortKey) => void;
  select: (id: string | null) => void;
  upsert: (finding: Finding) => void;
}

function toggle<T>(list: T[], value: T): T[] {
  return list.includes(value) ? list.filter((v) => v !== value) : [...list, value];
}

export const useFindingsStore = create<FindingsState>((set) => ({
  findings: [],
  total: 0,
  loading: false,
  error: null,
  search: '',
  severities: [],
  cwes: [],
  tools: [],
  packages: [],
  states: [],
  showSuppressed: false,
  sortKey: 'severity',
  sortDir: 'desc',
  selectedId: null,
  setFindings: (findings, total) => set({ findings, total }),
  setLoading: (loading) => set({ loading }),
  setError: (error) => set({ error }),
  setSearch: (search) => set({ search }),
  toggleSeverity: (severity) => set((s) => ({ severities: toggle(s.severities, severity) })),
  toggleCwe: (cwe) => set((s) => ({ cwes: toggle(s.cwes, cwe) })),
  toggleTool: (tool) => set((s) => ({ tools: toggle(s.tools, tool) })),
  togglePackage: (pkg) => set((s) => ({ packages: toggle(s.packages, pkg) })),
  toggleState: (state) => set((s) => ({ states: toggle(s.states, state) })),
  setShowSuppressed: (showSuppressed) => set({ showSuppressed }),
  setSort: (key) =>
    set((state) =>
      state.sortKey === key
        ? { sortDir: state.sortDir === 'asc' ? 'desc' : 'asc' }
        : { sortKey: key, sortDir: key === 'severity' || key === 'confidence' ? 'desc' : 'asc' },
    ),
  select: (selectedId) => set({ selectedId }),
  upsert: (finding) =>
    set((state) => {
      const index = state.findings.findIndex((f) => f.id === finding.id);
      const findings =
        index >= 0
          ? state.findings.map((f) => (f.id === finding.id ? finding : f))
          : [finding, ...state.findings];
      return { findings, total: state.total + (index >= 0 ? 0 : 1) };
    }),
}));
