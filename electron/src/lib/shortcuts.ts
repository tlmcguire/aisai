/**
 * Keyboard shortcuts: one place that says which keys do what, used both to
 * handle the keys and to list them for the user.
 *
 * A key spec is written once, platform-neutral: "Mod" is ⌘ on macOS and Ctrl
 * elsewhere ("Mod+,", "Mod+Shift+S", "Ctrl+`").
 */

export interface KeySpec {
  mod: boolean;
  ctrl: boolean;
  shift: boolean;
  alt: boolean;
  key: string;
}

export const isMac = (): boolean =>
  typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent);

export function parseKeys(spec: string): KeySpec {
  const parts = spec.split('+');
  // "Mod++" would be a plus key; nobody uses it here, so a plain split is enough.
  const key = parts.pop() ?? '';
  const has = (name: string): boolean => parts.includes(name);
  return { mod: has('Mod'), ctrl: has('Ctrl'), shift: has('Shift'), alt: has('Alt'), key: key.toLowerCase() };
}

/** The physical key of an event, independent of Shift and of the keyboard layout where it matters. */
function eventKey(event: KeyboardEvent): string {
  // With Shift held, "/" arrives as "?" and "1" as "!"; the code is stable.
  if (/^Digit\d$/.test(event.code)) return event.code.slice(5);
  if (event.code === 'Slash') return '/';
  if (event.code === 'Comma') return ',';
  if (event.code === 'Backquote') return '`';
  return event.key.toLowerCase();
}

export function matches(spec: KeySpec, event: KeyboardEvent, mac = isMac()): boolean {
  const modDown = mac ? event.metaKey : event.ctrlKey;
  const wantCtrl = spec.ctrl || (spec.mod && !mac);
  const wantMeta = spec.mod && mac;
  return (
    event.ctrlKey === wantCtrl &&
    event.metaKey === wantMeta &&
    (!spec.mod || modDown) &&
    event.shiftKey === spec.shift &&
    event.altKey === spec.alt &&
    eventKey(event) === spec.key
  );
}

/** How a key spec is written for the user on this platform. */
export function displayKeys(spec: string, mac = isMac()): string[] {
  const s = parseKeys(spec);
  const out: string[] = [];
  if (mac) {
    if (s.ctrl) out.push('⌃');
    if (s.alt) out.push('⌥');
    if (s.shift) out.push('⇧');
    if (s.mod) out.push('⌘');
  } else {
    if (s.ctrl || s.mod) out.push('Ctrl');
    if (s.alt) out.push('Alt');
    if (s.shift) out.push('Shift');
  }
  const names: Record<string, string> = { enter: '↵', escape: 'Esc', ',': ',', '`': '`', '/': '/' };
  out.push(names[s.key] ?? s.key.toUpperCase());
  return out;
}

export interface Shortcut {
  keys: string;
  label: string;
  group: string;
  /** Runs the action. Leave it out for keys handled elsewhere (the editor's own). */
  run?: () => void;
  /** Whether the key still works while the focus is in a text field. */
  inFields?: boolean;
}
