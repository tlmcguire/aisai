import { describe, expect, it } from 'vitest';

import { displayKeys, matches, parseKeys } from '../src/lib/shortcuts';

const key = (init: Partial<KeyboardEventInit> & { key: string; code?: string }): KeyboardEvent =>
  ({ ctrlKey: false, metaKey: false, shiftKey: false, altKey: false, code: '', ...init }) as unknown as KeyboardEvent;

describe('shortcuts', () => {
  it('maps Mod to Cmd on macOS and Ctrl elsewhere', () => {
    const spec = parseKeys('Mod+,');
    expect(matches(spec, key({ key: ',', code: 'Comma', metaKey: true }), true)).toBe(true);
    expect(matches(spec, key({ key: ',', code: 'Comma', ctrlKey: true }), true)).toBe(false);
    expect(matches(spec, key({ key: ',', code: 'Comma', ctrlKey: true }), false)).toBe(true);
  });

  it('needs the exact modifiers', () => {
    const spec = parseKeys('Mod+Shift+S');
    expect(matches(spec, key({ key: 'S', metaKey: true, shiftKey: true }), true)).toBe(true);
    expect(matches(spec, key({ key: 's', metaKey: true }), true)).toBe(false);
    expect(matches(spec, key({ key: 's', metaKey: true, shiftKey: true, altKey: true }), true)).toBe(false);
  });

  it('reads Shift+/ and Shift+digit by their physical key', () => {
    expect(matches(parseKeys('Mod+Shift+/'), key({ key: '?', code: 'Slash', metaKey: true, shiftKey: true }), true)).toBe(true);
    expect(matches(parseKeys('Mod+1'), key({ key: '1', code: 'Digit1', ctrlKey: true }), false)).toBe(true);
  });

  it('treats a literal Ctrl as Ctrl on every platform', () => {
    expect(matches(parseKeys('Ctrl+`'), key({ key: '`', code: 'Backquote', ctrlKey: true }), true)).toBe(true);
  });

  it('writes the keys for the platform', () => {
    expect(displayKeys('Mod+Shift+S', true)).toEqual(['⇧', '⌘', 'S']);
    expect(displayKeys('Mod+Shift+S', false)).toEqual(['Ctrl', 'Shift', 'S']);
  });
});
