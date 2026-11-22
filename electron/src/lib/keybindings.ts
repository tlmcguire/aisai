/**
 * The user's own key bindings.
 *
 * Every rebindable shortcut has a stable id ("file.save", "debug.stepOver").
 * Its default keys live with the shortcut itself (views/shortcuts.ts); what
 * is kept here is only what the user changed, in localStorage, so a new
 * default in a later version still reaches everyone who did not change it.
 *
 * A key spec is the app's platform-neutral notation from lib/shortcuts.ts:
 * "Mod+Shift+S", where Mod is ⌘ on macOS and Ctrl elsewhere. The helpers at
 * the bottom translate it for the two other places keys are written:
 * CodeMirror's keymaps ("Mod-Shift-s", https://codemirror.net/docs/ref/#view.KeyBinding)
 * and Electron's menu accelerators ("CmdOrCtrl+Shift+S",
 * https://www.electronjs.org/docs/latest/api/accelerator).
 */

import { create } from 'zustand';

import { eventKey, isMac, parseKeys } from './shortcuts';

const STORAGE_KEY = 'aisai.keybindings';

/** An empty string means the user removed the binding on purpose. */
type Overrides = Record<string, string>;

function load(): Overrides {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    const parsed: unknown = raw ? JSON.parse(raw) : {};
    if (!parsed || typeof parsed !== 'object') return {};
    return Object.fromEntries(Object.entries(parsed as Record<string, unknown>).filter(([, v]) => typeof v === 'string')) as Overrides;
  } catch {
    return {};
  }
}

function save(overrides: Overrides): void {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(overrides));
  } catch {
    /* private mode or storage full: the change lasts for this session */
  }
}

export interface KeybindingState {
  overrides: Overrides;
  /** Binds an action to keys; "" unbinds it. */
  set: (id: string, keys: string) => void;
  /** Goes back to the default keys of one action. */
  reset: (id: string) => void;
  resetAll: () => void;
}

export const useKeybindingStore = create<KeybindingState>((set) => ({
  overrides: load(),
  set: (id, keys) =>
    set((state) => {
      const overrides = { ...state.overrides, [id]: normalizeSpec(keys) };
      save(overrides);
      return { overrides };
    }),
  reset: (id) =>
    set((state) => {
      const overrides = { ...state.overrides };
      delete overrides[id];
      save(overrides);
      return { overrides };
    }),
  resetAll: () => {
    save({});
    set({ overrides: {} });
  },
}));

/** The keys bound to an action: the user's choice, or the default. */
export function keysFor(id: string, defaults: string): string {
  const custom = useKeybindingStore.getState().overrides[id];
  return custom === undefined ? defaults : custom;
}

/** Writes a spec in a canonical order, so two ways of typing it compare equal. */
export function normalizeSpec(spec: string): string {
  if (!spec.trim()) return '';
  // Modifier names in any case ("shift+mod+z").
  const names: Record<string, string> = { mod: 'Mod', ctrl: 'Ctrl', control: 'Ctrl', alt: 'Alt', option: 'Alt', shift: 'Shift', cmd: 'Mod', cmdorctrl: 'Mod' };
  const parts0 = spec.split('+');
  const key0 = parts0.pop() ?? '';
  const s = parseKeys([...parts0.map((p) => names[p.trim().toLowerCase()] ?? p.trim()), key0].join('+'));
  const parts: string[] = [];
  if (s.mod) parts.push('Mod');
  if (s.ctrl) parts.push('Ctrl');
  if (s.alt) parts.push('Alt');
  if (s.shift) parts.push('Shift');
  parts.push(s.key.length === 1 ? s.key.toUpperCase() : s.key.charAt(0).toUpperCase() + s.key.slice(1));
  return parts.join('+');
}

const MODIFIER_KEYS = new Set(['Shift', 'Control', 'Alt', 'Meta', 'CapsLock', 'Fn', 'Hyper', 'Super', 'OS']);

/**
 * The spec of a key press, for recording a new binding. Returns null while
 * only modifiers are held. On macOS ⌘ is Mod and ⌃ stays Ctrl; elsewhere
 * Ctrl is Mod, so a binding written on one system works on the other.
 */
export function specFromEvent(event: KeyboardEvent, mac = isMac()): string | null {
  if (MODIFIER_KEYS.has(event.key)) return null;
  const parts: string[] = [];
  if (mac ? event.metaKey : event.ctrlKey) parts.push('Mod');
  if (mac && event.ctrlKey) parts.push('Ctrl');
  if (event.altKey) parts.push('Alt');
  if (event.shiftKey) parts.push('Shift');
  // eventKey reads the physical key where Shift or the layout would change
  // the character ("Shift+1" rather than "!").
  let key = eventKey(event);
  if (key === ' ') key = 'space';
  parts.push(key);
  return normalizeSpec(parts.join('+'));
}

/** Whether a binding is a plain character, which would steal typing. */
export function isPlainTypingKey(spec: string): boolean {
  const s = parseKeys(spec);
  if (s.mod || s.ctrl || s.alt) return false;
  return s.key.length === 1 || s.key === 'space' || s.key === 'enter' || s.key === 'backspace' || s.key === 'tab';
}

/** The spec as a CodeMirror key name: "Mod+Shift+Z" → "Mod-Shift-z". */
export function toCodeMirror(spec: string): string {
  if (!spec) return '';
  const s = parseKeys(spec);
  const parts: string[] = [];
  if (s.mod) parts.push('Mod');
  if (s.ctrl) parts.push('Ctrl');
  if (s.alt) parts.push('Alt');
  if (s.shift) parts.push('Shift');
  const named: Record<string, string> = { escape: 'Escape', enter: 'Enter', tab: 'Tab', space: 'Space', backspace: 'Backspace', delete: 'Delete' };
  const key = named[s.key] ?? (/^f\d{1,2}$/.test(s.key) ? s.key.toUpperCase() : s.key);
  parts.push(key);
  return parts.join('-');
}

/** The spec as an Electron accelerator: "Mod+Shift+E" → "CmdOrCtrl+Shift+E". */
export function toAccelerator(spec: string): string {
  if (!spec) return '';
  const s = parseKeys(spec);
  const parts: string[] = [];
  if (s.mod) parts.push('CmdOrCtrl');
  if (s.ctrl) parts.push('Ctrl');
  if (s.alt) parts.push('Alt');
  if (s.shift) parts.push('Shift');
  const named: Record<string, string> = { escape: 'Esc', enter: 'Return', space: 'Space', ',': ',', '`': '`', '/': '/' };
  parts.push(named[s.key] ?? (s.key.length === 1 ? s.key.toUpperCase() : s.key.toUpperCase()));
  return parts.join('+');
}
