/**
 * The tint of the interface.
 *
 * The accent is the one colour the user chooses; the ground is everything
 * else — the surfaces, the borders and the text. The ground carries the
 * hue of the accent at a fraction of its strength, so the app reads as one
 * colour family instead of a green app with a blue button.
 *
 * The hue is set rather than rotated, and the lightness and saturation of
 * every colour are left alone, so the tint can be applied again without
 * drifting. A hue change moves the lightness of a colour a little, so the
 * text is then measured against the surfaces it sits on and moved a shade
 * darker or lighter until it reads on all of them: the tint never costs
 * the palette its contrast.
 *
 * The severity, syntax and status colours are meanings, not decoration,
 * and they stay where they are: a finding does not change its severity
 * because the accent did.
 */

/** The tokens that carry the ground: the surfaces, the borders and the text. */
export const GROUND_TOKENS = [
  '--bg-base',
  '--bg-surface',
  '--bg-raised',
  '--bg-inset',
  '--bg-hover',
  '--bg-selected',
  '--border',
  '--border-strong',
  '--fg',
  '--fg-muted',
  '--fg-subtle',
] as const;

export type GroundToken = (typeof GROUND_TOKENS)[number];

/** The smallest ratio a text colour may have against a surface (WCAG AA). */
export const MINIMUM_CONTRAST = 4.5;

/** A text colour and the surfaces it is read on. */
const READABLE: Array<[GroundToken, GroundToken[]]> = [
  ['--fg', ['--bg-base', '--bg-surface', '--bg-raised', '--bg-inset', '--bg-selected']],
  ['--fg-muted', ['--bg-base', '--bg-surface', '--bg-raised']],
  ['--fg-subtle', ['--bg-surface', '--bg-raised']],
];

interface Rgb {
  r: number;
  g: number;
  b: number;
}

interface Hsl {
  h: number;
  s: number;
  l: number;
}

function parseHex(hex: string): Rgb | null {
  const m = /^#([\da-f]{2})([\da-f]{2})([\da-f]{2})$/i.exec(hex.trim());
  if (!m) return null;
  return { r: parseInt(m[1], 16), g: parseInt(m[2], 16), b: parseInt(m[3], 16) };
}

function formatHex({ r, g, b }: Rgb): string {
  const part = (value: number): string =>
    Math.round(Math.min(255, Math.max(0, value))).toString(16).padStart(2, '0');
  return `#${part(r)}${part(g)}${part(b)}`;
}

function toHsl({ r, g, b }: Rgb): Hsl {
  const rn = r / 255;
  const gn = g / 255;
  const bn = b / 255;
  const max = Math.max(rn, gn, bn);
  const min = Math.min(rn, gn, bn);
  const l = (max + min) / 2;
  const d = max - min;
  if (d === 0) return { h: 0, s: 0, l };
  const s = l > 0.5 ? d / (2 - max - min) : d / (max + min);
  let h: number;
  if (max === rn) h = ((gn - bn) / d) % 6;
  else if (max === gn) h = (bn - rn) / d + 2;
  else h = (rn - gn) / d + 4;
  h *= 60;
  if (h < 0) h += 360;
  return { h, s, l };
}

function fromHsl({ h, s, l }: Hsl): Rgb {
  const c = (1 - Math.abs(2 * l - 1)) * s;
  const hp = (((h % 360) + 360) % 360) / 60;
  const x = c * (1 - Math.abs((hp % 2) - 1));
  let r = 0;
  let g = 0;
  let b = 0;
  if (hp < 1) [r, g, b] = [c, x, 0];
  else if (hp < 2) [r, g, b] = [x, c, 0];
  else if (hp < 3) [r, g, b] = [0, c, x];
  else if (hp < 4) [r, g, b] = [0, x, c];
  else if (hp < 5) [r, g, b] = [x, 0, c];
  else [r, g, b] = [c, 0, x];
  const m = l - c / 2;
  return { r: (r + m) * 255, g: (g + m) * 255, b: (b + m) * 255 };
}

/** The colour with its hue replaced, its lightness and saturation kept. */
export function withHue(hex: string, hue: number): string {
  const rgb = parseHex(hex);
  if (!rgb) return hex;
  return formatHex(fromHsl({ ...toHsl(rgb), h: hue }));
}

/** The hue of a colour, in degrees. */
export function hueOf(hex: string): number {
  const rgb = parseHex(hex);
  return rgb ? toHsl(rgb).h : 0;
}

/** The relative luminance of a colour (WCAG 2.1). */
export function luminance(hex: string): number {
  const rgb = parseHex(hex);
  if (!rgb) return 0;
  const channels = [rgb.r, rgb.g, rgb.b].map((value) => {
    const v = value / 255;
    return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4);
  });
  return 0.2126 * channels[0] + 0.7152 * channels[1] + 0.0722 * channels[2];
}

/** The contrast ratio of two colours (WCAG 2.1). */
export function contrast(foreground: string, background: string): number {
  const a = luminance(foreground);
  const b = luminance(background);
  return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);
}

/**
 * Move a text colour away from its surfaces until it reads on all of them.
 *
 * Only the depth of the text changes; the hue belongs to the tint.
 */
function ensureReadable(text: string, surfaces: string[], minimum: number): string {
  let colour = text;
  for (let step = 0; step < 60; step++) {
    const worst = Math.min(...surfaces.map((surface) => contrast(colour, surface)));
    if (worst >= minimum) return colour;
    const rgb = parseHex(colour);
    if (!rgb) return colour;
    const hsl = toHsl(rgb);
    // Away from the surfaces: a light text on a dark ground grows lighter,
    // a dark text on a pale ground grows darker.
    const onDark = luminance(surfaces[0]) < luminance(colour);
    const next = Math.min(1, Math.max(0, hsl.l + (onDark ? 0.004 : -0.004)));
    if (next === hsl.l) return colour;
    colour = formatHex(fromHsl({ ...hsl, l: next }));
  }
  return colour;
}

/**
 * Tint a ground palette to the hue of the accent.
 *
 * The palette maps token names to colours; the result has the same names.
 */
export function tintPalette(
  palette: Record<string, string>,
  hue: number,
  minimum: number = MINIMUM_CONTRAST,
): Record<string, string> {
  const tinted: Record<string, string> = { ...palette };
  for (const token of GROUND_TOKENS) {
    const value = palette[token];
    if (value) tinted[token] = withHue(value, hue);
  }
  for (const [text, surfaces] of READABLE) {
    const grounds = surfaces.map((surface) => tinted[surface]).filter(Boolean);
    if (!grounds.length || !tinted[text]) continue;
    tinted[text] = ensureReadable(tinted[text], grounds, minimum);
  }
  return tinted;
}

/**
 * Tint the ground of the document to the hue of the accent.
 *
 * The house values are cleared first, so the stylesheet's values for the
 * current theme show through: the tint is applied to the palette, never to
 * its own output, and switching the theme starts from the house palette
 * again.
 */
export function applyGround(root: HTMLElement, hue: number): void {
  const previous: Record<string, string> = {};
  for (const token of GROUND_TOKENS) {
    previous[token] = root.style.getPropertyValue(token);
    root.style.removeProperty(token);
  }
  const styles = getComputedStyle(root);
  const house: Record<string, string> = {};
  for (const token of GROUND_TOKENS) {
    const value = styles.getPropertyValue(token).trim();
    if (value) house[token] = value;
  }
  if (!house['--bg-base']) {
    // The stylesheet is not in the document yet: keep the ground we have.
    for (const token of GROUND_TOKENS) {
      if (previous[token]) root.style.setProperty(token, previous[token]);
    }
    return;
  }
  const tinted = tintPalette(house, hue);
  for (const token of GROUND_TOKENS) {
    if (tinted[token]) root.style.setProperty(token, tinted[token]);
  }
}
