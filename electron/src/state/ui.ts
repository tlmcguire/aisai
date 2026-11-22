import { create } from 'zustand';

export type MainTab = 'code' | 'ast' | 'diff';

/** The three columns of the workspace. The chat panel is docked separately. */
export type PaneId = 'deps' | 'main' | 'findings';
export type ChatDock = 'bottom' | 'right';

export const PANE_IDS: readonly PaneId[] = ['deps', 'main', 'findings'];
export const PANE_LABELS: Record<PaneId, string> = {
  deps: 'Dependencies & files',
  main: 'Code, AST & diff',
  findings: 'Findings',
};

/** A named arrangement of the panes, offered in Settings → Layout. */
export interface LayoutPreset {
  id: string;
  label: string;
  description: string;
  order: PaneId[];
  hidden: PaneId[];
  chatDock: ChatDock;
}

export const LAYOUT_PRESETS: readonly LayoutPreset[] = [
  {
    id: 'classic',
    label: 'Classic',
    description: 'Dependencies on the left, code in the middle, findings on the right, chat below.',
    order: ['deps', 'main', 'findings'],
    hidden: [],
    chatDock: 'bottom',
  },
  {
    id: 'triage',
    label: 'Triage',
    description: 'Findings first, then the code, so a finding is one click from its source.',
    order: ['findings', 'main', 'deps'],
    hidden: [],
    chatDock: 'bottom',
  },
  {
    id: 'reader',
    label: 'Reader',
    description: 'Code with the chat beside it, for reading a package and asking about it.',
    order: ['main', 'findings', 'deps'],
    hidden: ['deps'],
    chatDock: 'right',
  },
  {
    id: 'focus',
    label: 'Focus',
    description: 'Only the code and the findings. Everything else is a click away in Settings.',
    order: ['main', 'findings', 'deps'],
    hidden: ['deps'],
    chatDock: 'bottom',
  },
  {
    id: 'map',
    label: 'Supply-chain map',
    description: 'A wide dependency graph on the left, findings on the right, chat docked beside them.',
    order: ['deps', 'findings', 'main'],
    hidden: [],
    chatDock: 'right',
  },
];

const STORAGE_KEY = 'aisai.ui.v1';

interface PersistedUi {
  graphWidth: number;
  railWidth: number;
  chatHeight: number;
  chatOpen: boolean;
  filesShare: number;
  uiScale: number;
  paneOrder: PaneId[];
  hiddenPanes: PaneId[];
  chatDock: ChatDock;
  chatWidth: number;
}

/** The pane sizes a fresh install starts with, in reference pixels. */
export const DEFAULT_LAYOUT = {
  graphWidth: 300,
  railWidth: 340,
  chatHeight: 220,
  chatWidth: 380,
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

/** A stored order is trusted only when it is a permutation of the panes. */
function validOrder(order: unknown): PaneId[] {
  if (
    Array.isArray(order) &&
    order.length === PANE_IDS.length &&
    PANE_IDS.every((id) => order.includes(id))
  ) {
    return order as PaneId[];
  }
  return [...PANE_IDS];
}

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
  /** Left-to-right order of the three columns. */
  paneOrder: PaneId[];
  /** Columns the user hid. The main column can never be hidden. */
  hiddenPanes: PaneId[];
  chatDock: ChatDock;
  chatWidth: number;
  /** The id of the preset last applied, or '' once the layout was changed by hand. */
  layoutPreset: string;
  movePane: (id: PaneId, toIndex: number) => void;
  nudgePane: (id: PaneId, delta: -1 | 1) => void;
  setPaneHidden: (id: PaneId, hidden: boolean) => void;
  setChatDock: (dock: ChatDock) => void;
  setChatWidth: (width: number) => void;
  applyPreset: (id: string) => void;
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
  paneOrder: validOrder(persisted.paneOrder),
  hiddenPanes: (persisted.hiddenPanes ?? []).filter((id) => id !== 'main' && PANE_IDS.includes(id)),
  chatDock: persisted.chatDock === 'right' ? 'right' : 'bottom',
  chatWidth: persisted.chatWidth ?? DEFAULT_LAYOUT.chatWidth,
  layoutPreset: persisted.paneOrder ? '' : 'classic',
  movePane: (id, toIndex) =>
    set((state) => {
      const order = state.paneOrder.filter((pane) => pane !== id);
      order.splice(Math.max(0, Math.min(order.length, toIndex)), 0, id);
      return { paneOrder: order, layoutPreset: '' };
    }),
  nudgePane: (id, delta) =>
    set((state) => {
      const from = state.paneOrder.indexOf(id);
      const to = from + delta;
      if (from < 0 || to < 0 || to >= state.paneOrder.length) return state;
      const order = state.paneOrder.slice();
      [order[from], order[to]] = [order[to], order[from]];
      return { paneOrder: order, layoutPreset: '' };
    }),
  setPaneHidden: (id, hidden) =>
    set((state) => {
      if (id === 'main') return state;
      const rest = state.hiddenPanes.filter((pane) => pane !== id);
      return { hiddenPanes: hidden ? [...rest, id] : rest, layoutPreset: '' };
    }),
  setChatDock: (chatDock) => set({ chatDock, layoutPreset: '' }),
  setChatWidth: (width) => set({ chatWidth: Math.max(260, Math.min(720, width)) }),
  applyPreset: (id) =>
    set((state) => {
      const preset = LAYOUT_PRESETS.find((candidate) => candidate.id === id);
      if (!preset) return state;
      return {
        paneOrder: [...preset.order],
        hiddenPanes: [...preset.hidden],
        chatDock: preset.chatDock,
        chatOpen: true,
        layoutPreset: preset.id,
      };
    }),
  settingsOpen: false,
  settingsSection: 'server',
  setSettingsSection: (settingsSection) => set({ settingsSection }),
  setFilesShare: (share) => set({ filesShare: Math.max(0.15, Math.min(0.85, share)) }),
  setUiScale: (scale) => set({ uiScale: Math.max(0.85, Math.min(1.3, Math.round(scale * 100) / 100)) }),
  resetLayout: () =>
    set({ ...DEFAULT_LAYOUT, paneOrder: [...PANE_IDS], hiddenPanes: [], chatDock: 'bottom', layoutPreset: 'classic' }),
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
    paneOrder: state.paneOrder,
    hiddenPanes: state.hiddenPanes,
    chatDock: state.chatDock,
    chatWidth: state.chatWidth,
  };
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(payload));
  } catch {
    /* private mode / quota — the UI still works, the size is just not remembered */
  }
});
