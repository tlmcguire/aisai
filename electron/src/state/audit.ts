import { create } from 'zustand';

import type { Audit } from '../api/types';

export interface AuditState {
  audits: Audit[];
  current: Audit | null;
  loading: boolean;
  error: string | null;
  /** Live progress of the running job. */
  phase: string;
  percent: number;
  message: string;
  progressVisible: boolean;
  /** Name of the running job ('audit', 'scan', 'index', …) or null. */
  busy: string | null;
  setAudits: (audits: Audit[]) => void;
  setCurrent: (audit: Audit | null) => void;
  setLoading: (loading: boolean) => void;
  setError: (error: string | null) => void;
  setProgress: (phase: string, percent: number, message: string) => void;
  setProgressVisible: (visible: boolean) => void;
  setBusy: (job: string | null) => void;
  reset: () => void;
}

export const useAuditStore = create<AuditState>((set) => ({
  audits: [],
  current: null,
  loading: false,
  error: null,
  phase: '',
  percent: 0,
  message: '',
  progressVisible: false,
  busy: null,
  setAudits: (audits) => set({ audits }),
  setCurrent: (current) => set({ current }),
  setLoading: (loading) => set({ loading }),
  setError: (error) => set({ error }),
  setProgress: (phase, percent, message) =>
    set({ phase, percent, message, progressVisible: true }),
  setProgressVisible: (progressVisible) => set({ progressVisible }),
  setBusy: (busy) => set({ busy }),
  reset: () =>
    set({
      audits: [],
      current: null,
      loading: false,
      error: null,
      phase: '',
      percent: 0,
      message: '',
      progressVisible: false,
      busy: null,
    }),
}));
