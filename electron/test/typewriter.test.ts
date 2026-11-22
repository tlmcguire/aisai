/**
 * The typewriter effect: the text appears one character at a time.
 */

import { afterEach, describe, expect, it, vi } from 'vitest';

import { typewrite } from '../src/lib/typewriter';

/** A clock that the test can move, with a frame on top of it. */
function useFakeClock(): void {
  vi.useFakeTimers();
  vi.stubGlobal('requestAnimationFrame', (callback: (now: number) => void): number =>
    setTimeout(() => callback(Date.now()), 16) as unknown as number,
  );
  vi.stubGlobal('cancelAnimationFrame', (handle: number): void => {
    clearTimeout(handle as unknown as NodeJS.Timeout);
  });
}

describe('the typewriter', () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it('reveals the text one character at a time', async () => {
    useFakeClock();
    const written: string[] = [];
    const done = typewrite([{ text: 'abcdefgh', write: (text) => written.push(text) }]);
    await vi.advanceTimersByTimeAsync(3000);
    await done;
    expect(written.at(-1)).toBe('abcdefgh');
    // The text was not there all at once.
    expect(written.length).toBeGreaterThan(1);
    expect(written.some((text) => text.length > 0 && text.length < 8)).toBe(true);
  });

  it('keeps the order of the elements and moves the wave over them', async () => {
    useFakeClock();
    const written: string[] = [];
    const done = typewrite([
      { text: 'first', write: (text) => written.push(text) },
      { text: 'second', write: (text) => written.push(text) },
    ]);
    await vi.advanceTimersByTimeAsync(3000);
    await done;
    expect(written.at(-1)).toBe('second');
    expect(written.filter((text) => text === 'first').length).toBeGreaterThan(0);
  });

  it('writes the text at once when less motion is asked for', async () => {
    vi.stubGlobal('matchMedia', (query: string) => ({
      matches: query.includes('prefers-reduced-motion'),
      media: query,
      onchange: null,
      addEventListener: () => undefined,
      removeEventListener: () => undefined,
      addListener: () => undefined,
      removeListener: () => undefined,
      dispatchEvent: () => false,
    }));
    const written: string[] = [];
    await typewrite([{ text: 'abcdefgh', write: (text) => written.push(text) }]);
    expect(written).toEqual(['abcdefgh']);
  });
});
