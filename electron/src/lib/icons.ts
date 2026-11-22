/**
 * The interface icons, drawn as SVG on a 16-unit grid with a 1.5 stroke in
 * the current text colour, so they follow the theme and every state the
 * text does. DESIGN.md: icons are drawn, never Unicode glyphs or emoji.
 */

const SVG_NS = 'http://www.w3.org/2000/svg';

const PATHS = {
  /** Settings: an eight-tooth gear with a hub. */
  gear: [
    'M12.82 6.67 L14.52 7.01 L14.52 8.99 L12.82 9.33 L12.35 10.46 L13.32 11.91 L11.91 13.32 L10.46 12.35 L9.33 12.82 L8.99 14.52 L7.01 14.52 L6.67 12.82 L5.54 12.35 L4.09 13.32 L2.68 11.91 L3.65 10.46 L3.18 9.33 L1.48 8.99 L1.48 7.01 L3.18 6.67 L3.65 5.54 L2.68 4.09 L4.09 2.68 L5.54 3.65 L6.67 3.18 L7.01 1.48 L8.99 1.48 L9.33 3.18 L10.46 3.65 L11.91 2.68 L13.32 4.09 L12.35 5.54 Z',
    'M10.25 8 A2.25 2.25 0 1 1 5.75 8 A2.25 2.25 0 1 1 10.25 8 Z',
  ],
  'chevron-down': ['M4 6 L8 10 L12 6'],
  'chevron-up': ['M4 10 L8 6 L12 10'],
  'chevron-right': ['M6 4 L10 8 L6 12'],
  'arrow-up': ['M8 13 V3', 'M4 7 L8 3 L12 7'],
  'arrow-down': ['M8 3 V13', 'M4 9 L8 13 L12 9'],
  plus: ['M8 3 V13', 'M3 8 H13'],
  check: ['M3 8.5 L6.5 12 L13 4.5'],
  x: ['M4 4 L12 12', 'M12 4 L4 12'],
  archive: ['M2.5 3.5 H13.5 V6 H2.5 Z', 'M3.5 6 V12.5 H12.5 V6', 'M6.5 9 H9.5'],
  restore: ['M3.5 8 A4.5 4.5 0 1 0 5 4.7', 'M3 2.8 V5.5 H5.7'],
  trash: ['M3 4.5 H13', 'M6 4.5 V3 H10 V4.5', 'M4.5 4.5 L5 13 H11 L11.5 4.5', 'M7 7 V11', 'M9 7 V11'],
  pencil: ['M3 13 L3.5 10.5 L11 3 L13 5 L5.5 12.5 Z', 'M9.5 4.5 L11.5 6.5'],
  terminal: ['M2.5 3.5 H13.5 V12.5 H2.5 Z', 'M5 6.5 L7 8 L5 9.5', 'M8.5 10 H11'],
  branch: ['M5 3 V13', 'M5 9 C5 6 11 7 11 4', 'M11 3 V4.5'],
  globe: ['M8 2.5 A5.5 5.5 0 1 0 8 13.5 A5.5 5.5 0 1 0 8 2.5 Z', 'M2.5 8 H13.5', 'M8 2.5 C5.5 5 5.5 11 8 13.5', 'M8 2.5 C10.5 5 10.5 11 8 13.5'],
  tool: ['M9.5 2.5 A3 3 0 0 0 7 6.5 L2.5 11 L5 13.5 L9.5 9 A3 3 0 0 0 13.5 6.5 L11.5 8 L9 7 L8.5 4.5 Z'],
  /** Debugger: run or continue. */
  play: ['M5 3.5 L12.5 8 L5 12.5 Z'],
  pause: ['M5.5 3.5 V12.5', 'M10.5 3.5 V12.5'],
  stop: ['M4 4 H12 V12 H4 Z'],
  restart: ['M12.5 8 A4.5 4.5 0 1 1 11.2 4.8', 'M11.5 2 V5 H8.5'],
  /** Step over: an arc that jumps the line, landing on a dot. */
  'step-over': ['M3 9 A5 5 0 0 1 13 9', 'M13 5.5 V9 H9.5', 'M8 13 V13.01'],
  'step-into': ['M8 2.5 V10', 'M4.5 6.5 L8 10 L11.5 6.5', 'M8 13.5 V13.51'],
  'step-out': ['M8 10.5 V3', 'M4.5 6.5 L8 3 L11.5 6.5', 'M8 13.5 V13.51'],
  /** Four corners drawn apart: open the graph in a window of its own. */
  expand: [
    'M6 3.5 H3.5 V6',
    'M3.5 3.5 L7 7',
    'M10 3.5 H12.5 V6',
    'M12.5 3.5 L9 7',
    'M3.5 10 V12.5 H6',
    'M3.5 12.5 L7 9',
    'M12.5 10 V12.5 H10',
    'M12.5 12.5 L9 9',
  ],
  /** A speech bubble: the chat. */
  chat: ['M3 3.5 H13 V10.5 H7 L4 13 V10.5 H3 Z'],
  /** An i in a circle: explain. */
  info: ['M8 2.5 A5.5 5.5 0 1 0 8 13.5 A5.5 5.5 0 1 0 8 2.5 Z', 'M8 7.5 V11', 'M8 5 V5.01'],
  'chevron-left': ['M10 3.5 L5.5 8 L10 12.5'],
  bug: ['M5.5 6 A2.5 2.5 0 0 1 10.5 6 V10 A2.5 2.5 0 0 1 5.5 10 Z', 'M6.5 3.5 L5.5 2.5', 'M9.5 3.5 L10.5 2.5', 'M5.5 8 H2.5', 'M10.5 8 H13.5', 'M5.5 10.5 L3 12', 'M10.5 10.5 L13 12', 'M8 6 V12.5'],
} as const;

export type IconName = keyof typeof PATHS;

/** Build an icon. Decorative by default; pass a label to make it announce. */
export function icon(name: IconName, label?: string): SVGSVGElement {
  const svg = document.createElementNS(SVG_NS, 'svg');
  svg.setAttribute('viewBox', '0 0 16 16');
  svg.setAttribute('class', `icon icon-${name}`);
  svg.setAttribute('fill', 'none');
  svg.setAttribute('stroke', 'currentColor');
  svg.setAttribute('stroke-width', '1.5');
  svg.setAttribute('stroke-linecap', 'round');
  svg.setAttribute('stroke-linejoin', 'round');
  if (label) {
    svg.setAttribute('role', 'img');
    svg.setAttribute('aria-label', label);
  } else {
    svg.setAttribute('aria-hidden', 'true');
  }
  for (const d of PATHS[name]) {
    const path = document.createElementNS(SVG_NS, 'path');
    path.setAttribute('d', d);
    svg.append(path);
  }
  return svg;
}
