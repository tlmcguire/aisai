/**
 * The typewriter effect: when the language changes, the new text appears one
 * character at a time.
 *
 * One requestAnimationFrame loop walks through the whole list of elements,
 * so a page full of text costs one loop and not one timer per element. The
 * elements are visited in the order they appear on the screen and the wave
 * moves over them, so only a handful of them are typing at any moment.
 *
 * The wave is short on purpose: the interface is speaking a new language,
 * not loading a page, and the text has to be there before the user reads
 * it. People who ask their system for less motion get the text at once.
 */

/** How many elements are typing at the same time. */
const WAVE = 8;
/** How long the wave takes over the elements on screen, in milliseconds. */
const BUDGET = 120;
/** The shortest time an element spends typing, in milliseconds. */
const MIN_MS = 20;
/** The longest time an element spends typing, in milliseconds. */
const MAX_MS = 60;
/** Above this many elements the wave would cost more than it is worth. */
const LIMIT = 300;

/** One element that shows text while it is being typed out. */
export interface TypeTarget {
  /** The text to type out. */
  text: string;
  /** Writes the text to the element. */
  write: (text: string) => void;
}

/** Whether the system asks for less motion. */
export function prefersReducedMotion(): boolean {
  try {
    return typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches;
  } catch {
    return false;
  }
}

let running: TypeTarget[] = [];
let frame = 0;
let watch: ReturnType<typeof setTimeout> | 0 = 0;
let settle: (() => void) | null = null;

/** Stops the wave that is running and leaves the text complete. */
function finish(): void {
  if (frame && typeof cancelAnimationFrame === 'function') cancelAnimationFrame(frame);
  frame = 0;
  if (watch) clearTimeout(watch);
  watch = 0;
  const done = settle;
  settle = null;
  const targets = running;
  running = [];
  for (const target of targets) target.write(target.text);
  if (done) done();
}

/**
 * Types the text out, in a wave over the list.
 *
 * The returned promise settles when the text is complete. With less motion
 * asked for, the text is written at once and the promise settles right away.
 */
export function typewrite(targets: TypeTarget[]): Promise<void> {
  if (!targets.length) return Promise.resolve();
  // Less motion means no wave at all: the text is simply there.
  if (targets.length > LIMIT || prefersReducedMotion() || typeof requestAnimationFrame !== 'function') {
    finish();
    for (const target of targets) target.write(target.text);
    return Promise.resolve();
  }
  finish();
  running = targets;
  const spacing = BUDGET / (targets.length + WAVE);
  const written = targets.map(() => 0);
  const started = Date.now();
  return new Promise<void>((resolve) => {
    settle = resolve;
    // The clock is read here and not taken from the frame: the frame belongs
    // to the display, whose clock is not the clock of the page.
    const step = (): void => {
      const elapsed = Date.now() - started;
      let done = true;
      targets.forEach((target, index) => {
        const local = elapsed - index * spacing;
        if (local < 0) {
          done = false;
          return;
        }
        // Long texts need a moment of their own, but the wave keeps moving.
        const duration = Math.min(MAX_MS, Math.max(MIN_MS, spacing * WAVE, target.text.length * 0.5));
        const progress = Math.min(1, local / duration);
        const count = Math.max(1, Math.round(target.text.length * progress));
        if (count !== written[index]) {
          written[index] = count;
          target.write(target.text.slice(0, count));
        }
        if (progress < 1) done = false;
      });
      if (done) {
        finish();
        return;
      }
      frame = requestAnimationFrame(step);
    };
    frame = requestAnimationFrame(step);
    // A window that is not on screen does not get frames; the text has to be
    // there all the same.
    watch = setTimeout(finish, BUDGET + MAX_MS + 500);
  });
}
