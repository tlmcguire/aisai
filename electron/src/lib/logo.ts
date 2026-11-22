/**
 * The AISAI mark: the letter A drawn as a syntax tree. A root node, two
 * branches that form the legs, a crossbar edge, and one flagged leaf in the
 * signal colour: the finding. The same geometry is the app icon
 * (resources/brand/icon.svg).
 */

const MARK = `
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64" role="img" aria-label="AISAI" class="brand-mark">
  <g stroke-linecap="round" fill="none" stroke-width="5">
    <path d="M32 12 L16 52" stroke="var(--fg)"/>
    <path d="M32 12 L48 52" stroke="var(--fg)"/>
    <path d="M22 38 H42" stroke="var(--signal)" stroke-width="3"/>
  </g>
  <circle cx="32" cy="12" r="6" fill="var(--fg)"/>
  <circle cx="32" cy="12" r="2.2" fill="var(--bg-surface)"/>
  <circle cx="16" cy="52" r="5" fill="var(--fg)"/>
  <circle class="leaf" cx="48" cy="52" r="5.5" fill="var(--accent)"/>
</svg>`;

/** Build the mark as a detached SVG element. */
export function brandMark(): SVGElement {
  const doc = new DOMParser().parseFromString(MARK, 'image/svg+xml');
  return document.importNode(doc.documentElement, true) as unknown as SVGElement;
}
