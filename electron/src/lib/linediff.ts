/**
 * A line diff of two short texts, for showing a proposed change.
 *
 * It is the classic longest-common-subsequence diff
 * (https://en.wikipedia.org/wiki/Longest_common_subsequence#Code_for_the_dynamic_programming_solution):
 * a table of how many lines the two texts share from each pair of positions
 * on, then a walk through it that keeps shared lines and marks the rest as
 * removed or added. It is quadratic, which is fine for a selection; anything
 * over MAX_LINES on either side is shown as a whole replacement instead.
 */

export interface DiffLine {
  kind: 'same' | 'add' | 'remove';
  text: string;
}

const MAX_LINES = 800;

/** Splits a text into lines, without a phantom empty line after a final newline. */
function lines(text: string): string[] {
  const out = text.replace(/\r\n/g, '\n').split('\n');
  if (out.length > 1 && out[out.length - 1] === '') out.pop();
  return out;
}

export function diffLines(before: string, after: string): DiffLine[] {
  const a = lines(before);
  const b = lines(after);
  if (a.length > MAX_LINES || b.length > MAX_LINES) {
    return [...a.map((text) => ({ kind: 'remove' as const, text })), ...b.map((text) => ({ kind: 'add' as const, text }))];
  }
  // shared[i][j]: how many lines a[i..] and b[j..] have in common, in order.
  const shared: Uint16Array[] = Array.from({ length: a.length + 1 }, () => new Uint16Array(b.length + 1));
  for (let i = a.length - 1; i >= 0; i--) {
    for (let j = b.length - 1; j >= 0; j--) {
      shared[i][j] = a[i] === b[j] ? shared[i + 1][j + 1] + 1 : Math.max(shared[i + 1][j], shared[i][j + 1]);
    }
  }
  const out: DiffLine[] = [];
  let i = 0;
  let j = 0;
  while (i < a.length && j < b.length) {
    if (a[i] === b[j]) {
      out.push({ kind: 'same', text: a[i] });
      i++;
      j++;
    } else if (shared[i + 1][j] >= shared[i][j + 1]) {
      out.push({ kind: 'remove', text: a[i++] });
    } else {
      out.push({ kind: 'add', text: b[j++] });
    }
  }
  while (i < a.length) out.push({ kind: 'remove', text: a[i++] });
  while (j < b.length) out.push({ kind: 'add', text: b[j++] });
  return out;
}

/** How many lines were added and removed. */
export function diffCounts(diff: DiffLine[]): { added: number; removed: number } {
  let added = 0;
  let removed = 0;
  for (const line of diff) {
    if (line.kind === 'add') added++;
    else if (line.kind === 'remove') removed++;
  }
  return { added, removed };
}
