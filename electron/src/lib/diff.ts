/**
 * Unified diff parsing and rendering.
 *
 * The server returns a plain unified diff (`patch.unified`). We parse it into
 * hunks so the UI can show per-hunk finding metadata and render the diff
 * either side-by-side or unified.
 */

export type DiffLineKind = 'context' | 'add' | 'remove' | 'meta' | 'hunk';

export interface DiffLine {
  kind: DiffLineKind;
  text: string;
  oldNo?: number;
  newNo?: number;
}

export interface DiffHunk {
  /** The `@@ -a,b +c,d @@` header line. */
  header: string;
  lines: DiffLine[];
}

export function parseUnifiedDiff(unified: string): DiffHunk[] {
  const hunks: DiffHunk[] = [];
  const preamble: DiffLine[] = [];
  if (!unified) return hunks;

  let current: DiffHunk | null = null;
  let oldNo = 0;
  let newNo = 0;

  for (const raw of unified.split('\n')) {
    if (raw.startsWith('@@')) {
      current = { header: raw, lines: [] };
      hunks.push(current);
      const match = /@@\s+-(\d+)(?:,\d+)?\s+\+(\d+)(?:,\d+)?\s+@@/.exec(raw);
      oldNo = match ? Number(match[1]) : 1;
      newNo = match ? Number(match[2]) : 1;
      continue;
    }
    if (!current) {
      // File header lines (--- / +++ / diff --git / index) precede the first hunk.
      if (
        raw.startsWith('---') ||
        raw.startsWith('+++') ||
        raw.startsWith('diff ') ||
        raw.startsWith('index ')
      ) {
        preamble.push({ kind: 'meta', text: raw });
        continue;
      }
      if (!raw.trim()) continue;
      current = { header: '', lines: [] };
      hunks.push(current);
    }

    const marker = raw.charAt(0);
    const body = raw.slice(1);
    if (marker === '+') {
      current.lines.push({ kind: 'add', text: body, newNo });
      newNo += 1;
    } else if (marker === '-') {
      current.lines.push({ kind: 'remove', text: body, oldNo });
      oldNo += 1;
    } else if (marker === ' ') {
      current.lines.push({ kind: 'context', text: body, oldNo, newNo });
      oldNo += 1;
      newNo += 1;
    } else if (raw.startsWith('\\')) {
      current.lines.push({ kind: 'meta', text: raw });
    } else {
      current.lines.push({ kind: 'meta', text: raw });
    }
  }

  if (preamble.length) {
    if (hunks.length) {
      hunks[0].lines.unshift(...preamble);
    } else {
      hunks.push({ header: '', lines: preamble });
    }
  }

  return hunks.filter((h) => h.lines.length > 0 || h.header.startsWith('@@'));
}

/** A left/right pair of lines for the side-by-side renderer. */
export interface DiffSideRow {
  left?: DiffLine;
  right?: DiffLine;
}

/**
 * Pair removals with additions so the side-by-side view lines them up.
 * Runs of removals are matched with runs of additions, in order.
 */
export function toSideBySide(hunk: DiffHunk): DiffSideRow[] {
  const rows: DiffSideRow[] = [];
  let i = 0;
  const lines = hunk.lines;
  while (i < lines.length) {
    const line = lines[i];
    if (line.kind === 'meta') {
      rows.push({ left: line });
      i += 1;
      continue;
    }
    if (line.kind === 'add') {
      rows.push({ right: line });
      i += 1;
      continue;
    }
    if (line.kind !== 'remove') {
      rows.push({ left: line, right: line });
      i += 1;
      continue;
    }
    const removes: DiffLine[] = [];
    while (i < lines.length && lines[i].kind === 'remove') {
      removes.push(lines[i]);
      i += 1;
    }
    const adds: DiffLine[] = [];
    while (i < lines.length && lines[i].kind === 'add') {
      adds.push(lines[i]);
      i += 1;
    }
    const max = Math.max(removes.length, adds.length);
    for (let k = 0; k < max; k += 1) {
      rows.push({ left: removes[k], right: adds[k] });
    }
  }
  return rows;
}

/** Number of added and removed lines in a diff. */
export function diffStats(hunks: DiffHunk[]): { added: number; removed: number } {
  let added = 0;
  let removed = 0;
  for (const hunk of hunks) {
    for (const line of hunk.lines) {
      if (line.kind === 'add') added += 1;
      if (line.kind === 'remove') removed += 1;
    }
  }
  return { added, removed };
}
