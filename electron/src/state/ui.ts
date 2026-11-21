import { create } from 'zustand';

export type MainTab = 'code' | 'ast' | 'diff';

const STORAGE_KEY = 'aisai.ui.v1';

interface PersistedUi {
  graphWidth: number;
  railWidth: number;
  chatHeight: number;
  chatOpen: boolean;
}

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
  graphWidth: persisted.graphWidth ?? 300,
  railWidth: persisted.railWidth ?? 320,
  chatHeight: persisted.chatHeight ?? 220,
  settingsOpen: false,
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
  };
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(payload));
  } catch {
    /* private mode / quota — the UI still works, the size is just not remembered */
  }
});
