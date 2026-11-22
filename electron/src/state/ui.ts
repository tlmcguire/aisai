import { create } from 'zustand';

export type MainTab = 'code' | 'ast' | 'diff';

const STORAGE_KEY = 'aisai.ui.v1';

interface PersistedUi {
  graphWidth: number;
  railWidth: number;
  chatHeight: number;
  chatOpen: boolean;
  filesShare: number;
  uiScale: number;
}

/** The pane sizes a fresh install starts with, in reference pixels. */
export const DEFAULT_LAYOUT = {
  graphWidth: 300,
  railWidth: 340,
  chatHeight: 220,
  filesShare: 0.42,
  uiScale: 1,
} as const;

export type SettingsSection = 'server' | 'models' | 'sources' | 'layout' | 'mcp';

function loadPersisted(): Partial<PersistedUi> {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    return raw ? (JSON.parse(raw) as Partial<PersistedUi>) : {};
  } catch {
    return {};
  }
}

const persisted = loadPersisted();

export interface UiState {
  activeTab: MainTab;
  chatOpen: boolean;
  graphWidth: number;
  railWidth: number;
  chatHeight: number;
  settingsOpen: boolean;
  settingsSection: SettingsSection;
  /** Share of the left column given to the file tree (0.15–0.85). */
  filesShare: number;
  /** Interface size multiplier (0.85–1.3). */
  uiScale: number;
  setSettingsSection: (section: SettingsSection) => void;
  setFilesShare: (share: number) => void;
  setUiScale: (scale: number) => void;
  resetLayout: () => void;
  /** Bumped when the model server changes, so the toolbar re-reads it. */
  providersStamp: number;
  bumpProviders: () => void;
  setActiveTab: (tab: MainTab) => void;
  setChatOpen: (open: boolean) => void;
  setGraphWidth: (width: number) => void;
  setRailWidth: (width: number) => void;
  setChatHeight: (height: number) => void;
  setSettingsOpen: (open: boolean) => void;
}

export const useUiStore = create<UiState>((set) => ({
  activeTab: 'code',
  chatOpen: persisted.chatOpen ?? true,
  graphWidth: persisted.graphWidth ?? DEFAULT_LAYOUT.graphWidth,
  railWidth: persisted.railWidth ?? DEFAULT_LAYOUT.railWidth,
  chatHeight: persisted.chatHeight ?? DEFAULT_LAYOUT.chatHeight,
  filesShare: persisted.filesShare ?? DEFAULT_LAYOUT.filesShare,
  uiScale: persisted.uiScale ?? DEFAULT_LAYOUT.uiScale,
  settingsOpen: false,
  settingsSection: 'server',
  setSettingsSection: (settingsSection) => set({ settingsSection }),
  setFilesShare: (share) => set({ filesShare: Math.max(0.15, Math.min(0.85, share)) }),
  setUiScale: (scale) => set({ uiScale: Math.max(0.85, Math.min(1.3, Math.round(scale * 100) / 100)) }),
  resetLayout: () => set({ ...DEFAULT_LAYOUT }),
  providersStamp: 0,
  bumpProviders: () => set((state) => ({ providersStamp: state.providersStamp + 1 })),
  setActiveTab: (tab) => set({ activeTab: tab }),
  setChatOpen: (open) => set({ chatOpen: open }),
  setGraphWidth: (width) => set({ graphWidth: Math.max(180, Math.min(720, width)) }),
  setRailWidth: (width) => set({ railWidth: Math.max(240, Math.min(720, width)) }),
  setChatHeight: (height) => set({ chatHeight: Math.max(120, Math.min(640, height)) }),
  setSettingsOpen: (open) => set({ settingsOpen: open }),
}));

useUiStore.subscribe((state) => {
  const payload: PersistedUi = {
    graphWidth: state.graphWidth,
    railWidth: state.railWidth,
    chatHeight: state.chatHeight,
    chatOpen: state.chatOpen,
    filesShare: state.filesShare,
    uiScale: state.uiScale,
  };
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(payload));
  } catch {
    /* private mode / quota — the UI still works, the size is just not remembered */
  }
});
