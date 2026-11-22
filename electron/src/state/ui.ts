import { create } from 'zustand';

import { t } from '../lib/i18n';

export type MainTab = 'code' | 'ast' | 'diff';

/** The graphs that can be shown in the expanded window. */
export type GraphKind = 'deps' | 'ast';

/** The three columns of the workspace. The chat panel is docked separately. */
export type PaneId = 'deps' | 'main' | 'findings';
export type ChatDock = 'bottom' | 'right';

export const PANE_IDS: readonly PaneId[] = ['deps', 'main', 'findings'];

/**
 * The name of a pane, in the language of the interface.
 *
 * The name is used inside longer sentences — a drag handle, a "show this
 * pane" box — so it has to be the name as the interface writes it.
 */
export const paneName = (id: PaneId): string =>
  id === 'deps' ? t('layout.paneDependenciesFiles') : id === 'main' ? t('layout.paneCodeAstDiff') : t('layout.findings');

/** A named arrangement of the panes, offered in Settings → Layout. */
export interface LayoutPreset {
  id: string;
  /** The name of the preset, in the language of the interface. */
  label: () => string;
  /** What the preset is for, in the language of the interface. */
  description: () => string;
  order: PaneId[];
  hidden: PaneId[];
  chatDock: ChatDock;
}

export const LAYOUT_PRESETS: readonly LayoutPreset[] = [
  {
    id: 'classic',
    label: () => t('preset.classic'),
    description: () => t('preset.classicDescription'),
    order: ['deps', 'main', 'findings'],
    hidden: [],
    chatDock: 'bottom',
  },
  {
    id: 'triage',
    label: () => t('preset.triage'),
    description: () => t('preset.triageDescription'),
    order: ['findings', 'main', 'deps'],
    hidden: [],
    chatDock: 'bottom',
  },
  {
    id: 'reader',
    label: () => t('preset.reader'),
    description: () => t('preset.readerDescription'),
    order: ['main', 'findings', 'deps'],
    hidden: ['deps'],
    chatDock: 'right',
  },
  {
    id: 'focus',
    label: () => t('preset.focus'),
    description: () => t('preset.focusDescription'),
    order: ['main', 'findings', 'deps'],
    hidden: ['deps'],
    chatDock: 'bottom',
  },
  {
    id: 'map',
    label: () => t('preset.map'),
    description: () => t('preset.mapDescription'),
    order: ['deps', 'findings', 'main'],
    hidden: [],
    chatDock: 'right',
  },
];

export type ThemeChoice = 'dark' | 'light' | 'system';

/** Accent colours the user can pick: the signal colour of the interface. */
export const ACCENTS = [
  { id: 'lime', label: () => t('accent.lime'), dark: ['#b4f23c', '#c8ff5a', '#232f16'], light: ['#3f6212', '#2f4c0c', '#d5e6bb'] },
  { id: 'teal', label: () => t('accent.teal'), dark: ['#5fd3c4', '#8ae9dc', '#17302e'], light: ['#0b5f59', '#094845', '#c4e6e1'] },
  { id: 'blue', label: () => t('accent.blue'), dark: ['#6cb2ff', '#9acaff', '#16283d'], light: ['#1e40af', '#172f85', '#cdd9f7'] },
  { id: 'violet', label: () => t('accent.violet'), dark: ['#b79cff', '#d0bcff', '#2a2142'], light: ['#5b21b6', '#46188f', '#dccff5'] },
  { id: 'orange', label: () => t('accent.orange'), dark: ['#ffa24a', '#ffbb7a', '#38261a'], light: ['#9a3412', '#7a280d', '#f3d4bf'] },
] as const;
export type AccentId = (typeof ACCENTS)[number]['id'];

/** Apply the theme and accent to the document. */
export function applyAppearance(theme: ThemeChoice, accent: AccentId): void {
  const root = document.documentElement;
  const light = theme === 'light' || (theme === 'system' && window.matchMedia('(prefers-color-scheme: light)').matches);
  root.dataset.theme = light ? 'light' : 'dark';
  const pick = ACCENTS.find((entry) => entry.id === accent) ?? ACCENTS[0];
  const [base, strong, soft] = light ? pick.light : pick.dark;
  root.style.setProperty('--accent', base);
  root.style.setProperty('--accent-strong', strong);
  root.style.setProperty('--accent-soft', soft);
  root.style.setProperty('--fg-on-accent', light ? '#ffffff' : '#0a1112');
}

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
  terminalHeight: number;
  completionEnabled: boolean;
  acceptMarksFixed: boolean;
  theme: ThemeChoice;
  accent: AccentId;
}

/** The pane sizes a fresh install starts with, in reference pixels. */
export const DEFAULT_LAYOUT = {
  graphWidth: 300,
  railWidth: 340,
  chatHeight: 220,
  chatWidth: 380,
  terminalHeight: 260,
  filesShare: 0.42,
  uiScale: 1,
} as const;

export type SettingsSection = 'server' | 'models' | 'sources' | 'layout' | 'mcp' | 'about' | 'appearance' | 'keyboard' | 'accessibility';

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
  /** Which graph is open in the expanded window, if any. */
  expandedGraph: GraphKind | null;
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
  /** The terminal panel is never open at launch: a shell starts only on request. */
  terminalOpen: boolean;
  terminalHeight: number;
  /** What the bottom panel shows: the shells or the debugger. */
  bottomView: BottomView;
  setBottomView: (view: BottomView) => void;
  /** Inline model completion in the editor. */
  theme: ThemeChoice;
  setTheme: (theme: ThemeChoice) => void;
  accent: AccentId;
  setAccent: (accent: AccentId) => void;
  completionEnabled: boolean;
  setCompletionEnabled: (enabled: boolean) => void;
  /** Accepting a proposed fix marks its finding as fixed. */
  acceptMarksFixed: boolean;
  setAcceptMarksFixed: (enabled: boolean) => void;
  setTerminalOpen: (open: boolean) => void;
  setTerminalHeight: (height: number) => void;
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
  /** Opens a graph in the expanded window, or closes it with null. */
  setExpandedGraph: (kind: GraphKind | null) => void;
}

export type BottomView = 'terminal' | 'debug';

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
  terminalOpen: false,
  bottomView: 'terminal',
  setBottomView: (bottomView) => set({ bottomView }),
  theme: persisted.theme ?? 'dark',
  setTheme: (theme) => set({ theme }),
  accent: persisted.accent ?? 'lime',
  setAccent: (accent) => set({ accent }),
  completionEnabled: persisted.completionEnabled ?? true,
  setCompletionEnabled: (completionEnabled) => set({ completionEnabled }),
  acceptMarksFixed: persisted.acceptMarksFixed ?? true,
  setAcceptMarksFixed: (acceptMarksFixed) => set({ acceptMarksFixed }),
  terminalHeight: persisted.terminalHeight ?? DEFAULT_LAYOUT.terminalHeight,
  setTerminalOpen: (terminalOpen) => set({ terminalOpen }),
  setTerminalHeight: (height) => set({ terminalHeight: Math.max(120, Math.min(720, height)) }),
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
  expandedGraph: null,
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
  setExpandedGraph: (expandedGraph) => set({ expandedGraph }),
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
    terminalHeight: state.terminalHeight,
    completionEnabled: state.completionEnabled,
    acceptMarksFixed: state.acceptMarksFixed,
    theme: state.theme,
    accent: state.accent,
  };
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(payload));
  } catch {
    /* private mode / quota — the UI still works, the size is just not remembered */
  }
});

// Apply the saved look at once, and follow the system when it is chosen.
applyAppearance(useUiStore.getState().theme, useUiStore.getState().accent);
useUiStore.subscribe((state, previous) => {
  if (state.theme !== previous.theme || state.accent !== previous.accent) applyAppearance(state.theme, state.accent);
});
try {
  window.matchMedia('(prefers-color-scheme: light)').addEventListener('change', () => {
    const { theme, accent } = useUiStore.getState();
    if (theme === 'system') applyAppearance(theme, accent);
  });
} catch {
  /* no matchMedia: the theme stays as chosen */
}
