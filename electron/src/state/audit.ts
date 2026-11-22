import { create } from 'zustand';

import type { Audit } from '../api/types';

/** One package of the running audit, as the download tracker shows it. */
export interface DownloadEntry {
  package: string;
  version: string;
  state: 'downloading' | 'done' | 'warning' | 'failed';
  detail: string;
  /** Size of the downloaded archive in bytes, once known. */
  bytes?: number;
  /** Order of arrival, so the list does not reshuffle while it fills. */
  order: number;
}

export interface AuditState {
  downloads: Record<string, DownloadEntry>;
  downloadTotal: number;
  upsertDownload: (entry: Omit<DownloadEntry, 'order'>, total: number) => void;
  clearDownloads: () => void;
  audits: Audit[];
  current: Audit | null;
  loading: boolean;
  error: string | null;
  /** Live progress of the running job. */
  phase: string;
  /** When the running job and its current phase began, for the ETA. */
  jobStartedAt: number;
  phaseStartedAt: number;
  phaseStartPercent: number;
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
  downloads: {},
  downloadTotal: 0,
  upsertDownload: (entry, total) =>
    set((state) => {
      const key = `${entry.package}@${entry.version}`;
      const previous = state.downloads[key];
      const order = previous?.order ?? Object.keys(state.downloads).length;
      return {
        downloads: { ...state.downloads, [key]: { ...entry, order } },
        downloadTotal: Math.max(total, state.downloadTotal),
      };
    }),
  clearDownloads: () => set({ downloads: {}, downloadTotal: 0 }),
  audits: [],
  current: null,
  loading: false,
  error: null,
  phase: '',
  jobStartedAt: 0,
  phaseStartedAt: 0,
  phaseStartPercent: 0,
  percent: 0,
  message: '',
  progressVisible: false,
  busy: null,
  setAudits: (audits) => set({ audits }),
  setCurrent: (current) => set({ current }),
  setLoading: (loading) => set({ loading }),
  setError: (error) => set({ error }),
  setProgress: (phase, percent, message) =>
    set((state) => {
      const now = Date.now();
      // A new job (the strip was hidden) or a new phase restarts the clocks.
      const newJob = !state.progressVisible || !state.jobStartedAt;
      const newPhase = newJob || phase !== state.phase;
      return {
        phase,
        percent,
        message,
        progressVisible: true,
        jobStartedAt: newJob ? now : state.jobStartedAt,
        phaseStartedAt: newPhase ? now : state.phaseStartedAt,
        phaseStartPercent: newPhase ? percent : state.phaseStartPercent,
      };
    }),
  setProgressVisible: (progressVisible) => set(progressVisible ? { progressVisible } : { progressVisible, jobStartedAt: 0 }),
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
