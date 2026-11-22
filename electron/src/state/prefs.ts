/**
 * Editor and accessibility preferences.
 *
 * They are kept apart from the layout (state/ui.ts) because they are applied
 * differently: each one becomes a data attribute or a CSS variable on <html>,
 * and the stylesheets do the rest (see "accessibility" and "editor themes" in
 * styles/app.css). Nothing here needs the backend. Remembered in localStorage.
 */

import { create } from 'zustand';

/* ---------------- editor themes ---------------- */

/** The colours an editor theme sets; each becomes a --code-* variable. */
export interface CodePalette {
  bg: string;
  fg: string;
  gutter: string;
  keyword: string;
  string: string;
  number: string;
  comment: string;
  function: string;
  type: string;
  operator: string;
  property: string;
}

export interface EditorTheme {
  id: string;
  label: string;
  /** Whether the theme is dark (for the caret, selection and scrollbars). */
  dark: boolean;
  palette: CodePalette;
}

/**
 * The editor themes. "Match the app" follows the light or dark interface;
 * the others are fixed. The well-known palettes are published under the MIT
 * licence by their authors (One Dark: atom/one-dark-syntax, Dracula:
 * draculatheme.com, Nord: nordtheme.com, Solarized: ethanschoonover.com/solarized,
 * GitHub: primer/github-vscode-theme); only their colours are used here.
 */
export const EDITOR_THEMES: EditorTheme[] = [
  {
    id: 'verdigris',
    label: 'Verdigris (dark)',
    dark: true,
    palette: { bg: '#0a1112', fg: '#dde8e3', gutter: '#6f8a85', keyword: '#c792ea', string: '#c3e88d', number: '#f78c6c', comment: '#6b8a88', function: '#82aaff', type: '#ffcb6b', operator: '#89ddff', property: '#b2ccd6' },
  },
  {
    id: 'verdigris-light',
    label: 'Verdigris (light)',
    dark: false,
    palette: { bg: '#fbfcfb', fg: '#16201f', gutter: '#6a7b77', keyword: '#8a1c7c', string: '#2f6410', number: '#a14a00', comment: '#5f716d', function: '#1a4fb5', type: '#7a4b00', operator: '#0b5d73', property: '#35474a' },
  },
  {
    id: 'one-dark',
    label: 'One Dark',
    dark: true,
    palette: { bg: '#282c34', fg: '#abb2bf', gutter: '#636d83', keyword: '#c678dd', string: '#98c379', number: '#d19a66', comment: '#7f848e', function: '#61afef', type: '#e5c07b', operator: '#56b6c2', property: '#e06c75' },
  },
  {
    id: 'dracula',
    label: 'Dracula',
    dark: true,
    palette: { bg: '#282a36', fg: '#f8f8f2', gutter: '#6272a4', keyword: '#ff79c6', string: '#f1fa8c', number: '#bd93f9', comment: '#6272a4', function: '#50fa7b', type: '#8be9fd', operator: '#ff79c6', property: '#66d9ef' },
  },
  {
    id: 'nord',
    label: 'Nord',
    dark: true,
    palette: { bg: '#2e3440', fg: '#d8dee9', gutter: '#616e88', keyword: '#81a1c1', string: '#a3be8c', number: '#b48ead', comment: '#7b88a1', function: '#88c0d0', type: '#8fbcbb', operator: '#81a1c1', property: '#d8dee9' },
  },
  {
    id: 'solarized-dark',
    label: 'Solarized Dark',
    dark: true,
    palette: { bg: '#002b36', fg: '#93a1a1', gutter: '#586e75', keyword: '#859900', string: '#2aa198', number: '#d33682', comment: '#657b83', function: '#268bd2', type: '#b58900', operator: '#93a1a1', property: '#839496' },
  },
  {
    id: 'solarized-light',
    label: 'Solarized Light',
    dark: false,
    palette: { bg: '#fdf6e3', fg: '#586e75', gutter: '#93a1a1', keyword: '#859900', string: '#2aa198', number: '#d33682', comment: '#839496', function: '#268bd2', type: '#b58900', operator: '#586e75', property: '#657b83' },
  },
  {
    id: 'github-light',
    label: 'GitHub Light',
    dark: false,
    palette: { bg: '#ffffff', fg: '#1f2328', gutter: '#6e7781', keyword: '#cf222e', string: '#0a3069', number: '#0550ae', comment: '#6e7781', function: '#8250df', type: '#953800', operator: '#1f2328', property: '#0550ae' },
  },
  {
    id: 'contrast-dark',
    label: 'High contrast (dark)',
    dark: true,
    palette: { bg: '#000000', fg: '#ffffff', gutter: '#d0d0d0', keyword: '#ff9eff', string: '#a8ff60', number: '#ffd866', comment: '#c0c0c0', function: '#78dce8', type: '#ffb86c', operator: '#ffffff', property: '#e0e0ff' },
  },
  {
    id: 'contrast-light',
    label: 'High contrast (light)',
    dark: false,
    palette: { bg: '#ffffff', fg: '#000000', gutter: '#333333', keyword: '#7a0070', string: '#0a4d00', number: '#7a3a00', comment: '#3d3d3d', function: '#00308f', type: '#5c3b00', operator: '#000000', property: '#202060' },
  },
];

/** "Match the app": Verdigris in the matching brightness. */
export const MATCH_APP = 'app';

/* ---------------- preferences ---------------- */

export interface Prefs {
  /** An EDITOR_THEMES id, or MATCH_APP. */
  editorTheme: string;
  /** The editor's font size in pixels. */
  editorFontSize: number;
  /** Extra space between lines of code. */
  editorLineSpacing: 'compact' | 'normal' | 'relaxed';
  /** The minimap over the right edge of the editor. */
  minimap: boolean;
  /** Animations: follow the system setting, keep them few, or keep them all. */
  motion: 'system' | 'reduce' | 'full';
  /** Stronger borders and text, for low vision or bright rooms. */
  highContrast: boolean;
  /** A thick focus ring on every control, not only after keyboard use. */
  strongFocus: boolean;
  /** Underline links and link-like buttons. */
  underlineLinks: boolean;
  /** Severity colours that stay distinct with red-green colour blindness. */
  colourBlindSeverity: boolean;
  /** Wider letter, word and line spacing in prose (helps some dyslexic readers). */
  readableSpacing: boolean;
  /** A blinking caret in the editor and terminal. */
  cursorBlink: boolean;
}

export const DEFAULT_PREFS: Prefs = {
  editorTheme: MATCH_APP,
  editorFontSize: 13,
  editorLineSpacing: 'normal',
  minimap: true,
  motion: 'system',
  highContrast: false,
  strongFocus: false,
  underlineLinks: false,
  colourBlindSeverity: false,
  readableSpacing: false,
  cursorBlink: true,
};

const STORAGE_KEY = 'aisai.prefs.v1';

function load(): Prefs {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    const saved = raw ? (JSON.parse(raw) as Partial<Prefs>) : {};
    const prefs = { ...DEFAULT_PREFS, ...saved };
    prefs.editorFontSize = Math.max(10, Math.min(24, Number(prefs.editorFontSize) || DEFAULT_PREFS.editorFontSize));
    if (prefs.editorTheme !== MATCH_APP && !EDITOR_THEMES.some((t) => t.id === prefs.editorTheme)) prefs.editorTheme = MATCH_APP;
    return prefs;
  } catch {
    return { ...DEFAULT_PREFS };
  }
}

export interface PrefsState extends Prefs {
  setPref: <K extends keyof Prefs>(key: K, value: Prefs[K]) => void;
  resetAccessibility: () => void;
}

export const usePrefsStore = create<PrefsState>((set) => ({
  ...load(),
  setPref: (key, value) => set({ [key]: value } as Partial<PrefsState>),
  resetAccessibility: () =>
    set({
      motion: DEFAULT_PREFS.motion,
      highContrast: DEFAULT_PREFS.highContrast,
      strongFocus: DEFAULT_PREFS.strongFocus,
      underlineLinks: DEFAULT_PREFS.underlineLinks,
      colourBlindSeverity: DEFAULT_PREFS.colourBlindSeverity,
      readableSpacing: DEFAULT_PREFS.readableSpacing,
      cursorBlink: DEFAULT_PREFS.cursorBlink,
      editorFontSize: DEFAULT_PREFS.editorFontSize,
      editorLineSpacing: DEFAULT_PREFS.editorLineSpacing,
    }),
}));

/** The theme the editor shows now, resolving "Match the app". */
export function resolvedEditorTheme(prefs: Pick<Prefs, 'editorTheme' | 'highContrast'>, appIsLight: boolean): EditorTheme {
  if (prefs.editorTheme === MATCH_APP) {
    const id = prefs.highContrast ? (appIsLight ? 'contrast-light' : 'contrast-dark') : appIsLight ? 'verdigris-light' : 'verdigris';
    return EDITOR_THEMES.find((t) => t.id === id) ?? EDITOR_THEMES[0];
  }
  return EDITOR_THEMES.find((t) => t.id === prefs.editorTheme) ?? EDITOR_THEMES[0];
}

/** Whether animations should be kept to a minimum now. */
export function motionReduced(prefs: Pick<Prefs, 'motion'> = usePrefsStore.getState()): boolean {
  if (prefs.motion === 'reduce') return true;
  if (prefs.motion === 'full') return false;
  try {
    return window.matchMedia('(prefers-reduced-motion: reduce)').matches;
  } catch {
    return false;
  }
}

const LINE_HEIGHT = { compact: 1.35, normal: 1.55, relaxed: 1.85 } as const;

/** Puts the preferences on <html>, where the stylesheets read them. */
export function applyPrefs(prefs: Prefs = usePrefsStore.getState()): void {
  const root = document.documentElement;
  const light = root.dataset.theme === 'light';
  const theme = resolvedEditorTheme(prefs, light);
  for (const [name, value] of Object.entries(theme.palette)) root.style.setProperty(`--code-${name}`, value);
  root.dataset.codeTheme = theme.dark ? 'dark' : 'light';
  root.style.setProperty('--code-font-size', `${prefs.editorFontSize}px`);
  root.style.setProperty('--code-line-height', String(LINE_HEIGHT[prefs.editorLineSpacing]));
  root.dataset.motion = motionReduced(prefs) ? 'reduce' : 'full';
  root.dataset.contrast = prefs.highContrast ? 'high' : 'normal';
  root.dataset.focus = prefs.strongFocus ? 'strong' : 'normal';
  root.dataset.links = prefs.underlineLinks ? 'underline' : 'plain';
  root.dataset.severity = prefs.colourBlindSeverity ? 'colourblind' : 'default';
  root.dataset.spacing = prefs.readableSpacing ? 'readable' : 'normal';
  root.dataset.minimap = prefs.minimap ? 'on' : 'off';
  root.dataset.blink = prefs.cursorBlink ? 'on' : 'off';
}

usePrefsStore.subscribe((state) => {
  const saved: Prefs = {
    editorTheme: state.editorTheme,
    editorFontSize: state.editorFontSize,
    editorLineSpacing: state.editorLineSpacing,
    minimap: state.minimap,
    motion: state.motion,
    highContrast: state.highContrast,
    strongFocus: state.strongFocus,
    underlineLinks: state.underlineLinks,
    colourBlindSeverity: state.colourBlindSeverity,
    readableSpacing: state.readableSpacing,
    cursorBlink: state.cursorBlink,
  };
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(saved));
  } catch {
    /* the preferences last for this session */
  }
  applyPrefs(state);
});

// The app theme (light or dark) decides "Match the app", and the system can
// change its reduced-motion setting while the app runs.
try {
  new MutationObserver(() => applyPrefs()).observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] });
  window.matchMedia('(prefers-reduced-motion: reduce)').addEventListener('change', () => applyPrefs());
} catch {
  /* not in a browser (tests) */
}
if (typeof document !== 'undefined') applyPrefs();
