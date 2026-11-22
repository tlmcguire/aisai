/**
 * The ground of the interface follows the accent.
 *
 * The accent is the one colour the user picks; the ground is everything
 * else — the surfaces, the borders and the text. When the accent changes,
 * the ground is re-tinted to its hue, so the app reads as one colour
 * family instead of a green app with a blue button.
 *
 * A hue change moves the lightness of a colour a little, so this test
 * measures every text colour against the surfaces it is read on, for
 * every accent and in both themes: the tint may not cost the palette its
 * contrast.
 */

import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

import { ACCENTS } from '../src/lib/accents';
import { contrast, hueOf, tintPalette, withHue } from '../src/lib/tint';

const tokensPath = fileURLToPath(new URL('../src/styles/tokens.css', import.meta.url));

/** The colours of the stylesheet, one map per theme. */
function housePalettes(): Record<string, Record<string, string>> {
  const css = readFileSync(tokensPath, 'utf8');
  const palettes: Record<string, Record<string, string>> = {};
  let selector = '';
  let body: string[] = [];
  const flush = (): void => {
    if (!selector.startsWith(':root')) return;
    const theme = selector.includes('light') ? 'light' : 'dark';
    const values: Record<string, string> = {};
    for (const line of body) {
      const m = /^\s*(--[a-z0-9-]+):\s*([^;]+);/.exec(line);
      if (m) values[m[1]] = m[2].trim();
    }
    palettes[theme] = { ...palettes[theme], ...values };
  };
  for (const line of css.split('\n')) {
    const trimmed = line.trim();
    if (trimmed.endsWith('{')) {
      selector = trimmed.slice(0, -1).trim();
      body = [];
      continue;
    }
    if (trimmed === '}') {
      flush();
      selector = '';
      body = [];
      continue;
    }
    body.push(line);
  }
  return palettes;
}

/** The text colours and the surfaces they are read on. */
const READABLE: Array<[string, string[]]> = [
  ['--fg', ['--bg-base', '--bg-surface', '--bg-raised', '--bg-inset', '--bg-selected']],
  ['--fg-muted', ['--bg-base', '--bg-surface', '--bg-raised']],
  ['--fg-subtle', ['--bg-surface', '--bg-raised']],
];

/** The ground of a theme with the accent of the day in it. */
function paletteFor(theme: string, accent: (typeof ACCENTS)[number]): Record<string, string> {
  const house = housePalettes()[theme] ?? {};
  const [base, strong, soft] = theme === 'light' ? accent.light : accent.dark;
  return { ...house, '--accent': base, '--accent-strong': strong, '--accent-soft': soft };
}

/** Whether a colour carries a hue of its own, or is a plain white or black. */
function hasHue(hex: string): boolean {
  const m = /^#([\da-f]{2})([\da-f]{2})([\da-f]{2})$/i.exec(hex);
  if (!m) return false;
  const channels = [1, 2, 3].map((index) => parseInt(m[index], 16));
  return Math.max(...channels) - Math.min(...channels) > 3;
}

describe('the tint of the ground', () => {
  it('gives every accent the hue of its own ground', () => {
    for (const accent of ACCENTS) {
      for (const theme of ['dark', 'light']) {
        const tinted = tintPalette(paletteFor(theme, accent), accent.ground);
        const hue = hueOf(tinted['--bg-base']);
        expect(Math.abs(hue - accent.ground), `${theme}/${accent.id} ground is at ${hue.toFixed(1)}°`).toBeLessThan(6);
      }
    }
  });

  it('holds the ground to one hue, so the app reads as one colour', () => {
    for (const accent of ACCENTS) {
      for (const theme of ['dark', 'light']) {
        const tinted = tintPalette(paletteFor(theme, accent), accent.ground);
        const hues = Object.entries(tinted)
          .filter(([token]) => /^--(bg|border|fg)-(base|surface|raised|inset|hover|selected|strong|muted|subtle)$/.test(token))
          .map(([, value]) => value)
          .filter(hasHue)
          .map(hueOf);
        const spread = Math.max(...hues) - Math.min(...hues);
        expect(spread, `${theme}/${accent.id} ground spreads over ${spread.toFixed(1)}°`).toBeLessThan(12);
      }
    }
  });

  it('keeps every text colour readable, for every accent and theme', () => {
    for (const accent of ACCENTS) {
      for (const theme of ['dark', 'light']) {
        const tinted = tintPalette(paletteFor(theme, accent), accent.ground);
        for (const [text, surfaces] of READABLE) {
          for (const surface of surfaces) {
            const ratio = contrast(tinted[text], tinted[surface]);
            expect(
              ratio,
              `${theme}/${accent.id}: ${text} on ${surface} is ${ratio.toFixed(2)}:1`,
            ).toBeGreaterThanOrEqual(4.5);
          }
        }
      }
    }
  });

  it('keeps the accent readable on its own ground', () => {
    for (const accent of ACCENTS) {
      for (const theme of ['dark', 'light']) {
        const tinted = tintPalette(paletteFor(theme, accent), accent.ground);
        const onAccent = theme === 'light' ? '#ffffff' : '#0a1112';
        expect(contrast(tinted['--accent'], tinted['--bg-base'])).toBeGreaterThanOrEqual(4.5);
        expect(contrast(onAccent, tinted['--accent'])).toBeGreaterThanOrEqual(4.5);
        expect(contrast(tinted['--fg'], tinted['--accent-soft'])).toBeGreaterThanOrEqual(4.5);
      }
    }
  });

  it('is stable: the same ground tinted twice is the same ground', () => {
    for (const accent of ACCENTS) {
      const once = tintPalette(paletteFor('dark', accent), accent.ground);
      const twice = tintPalette(once, accent.ground);
      for (const [token, value] of Object.entries(once)) {
        expect(twice[token], token).toBe(value);
      }
    }
  });
});

describe('the hue of a colour', () => {
  it('is read and set without touching the lightness', () => {
    expect(hueOf('#b4f23c')).toBeCloseTo(80, 0);
    expect(hueOf('#8ea5a0')).toBeCloseTo(167, 0);
    const tinted = withHue('#8ea5a0', 256);
    expect(hueOf(tinted)).toBeCloseTo(256, 0);
    expect(tinted).toMatch(/^#[0-9a-f]{6}$/);
  });

  it('leaves a colour without a hue of its own alone', () => {
    expect(withHue('#ffffff', 256)).toBe('#ffffff');
    expect(withHue('#000000', 256)).toBe('#000000');
  });
});
