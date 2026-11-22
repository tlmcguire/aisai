import { describe, expect, it } from 'vitest';

import { diffStats, parseUnifiedDiff, toSideBySide } from '../src/lib/diff';
import { formatDuration, progressStats } from '../src/lib/progress';
import { countCycles, findPath } from '../src/state/graph';
import { maxSeverity, packageURL, purlName, purlVersion, SEVERITY_RANK } from '../src/api/types';

describe('diff parsing', () => {
  const unified = [
    '--- a/flask/security.py',
    '+++ b/flask/security.py',
    '@@ -24,7 +24,7 @@ def safe_str_cmp(a, b):',
    '     """Legacy comparison helper kept for backwards compatibility."""',
    '-    return a == b',
    '+    return hmac.compare_digest(str(a), str(b))',
    ' ',
    ' ',
    ' def generate_token(length=TOKEN_LENGTH):',
  ].join('\n');

  it('parses hunks, line numbers and kinds', () => {
    const hunks = parseUnifiedDiff(unified);
    expect(hunks).toHaveLength(1);
    expect(hunks[0].header).toContain('@@');
    const kinds = hunks[0].lines.map((line) => line.kind);
    expect(kinds).toContain('remove');
    expect(kinds).toContain('add');
    expect(kinds).toContain('context');
    const removed = hunks[0].lines.find((line) => line.kind === 'remove');
    expect(removed?.oldNo).toBe(25);
  });

  it('counts additions and removals', () => {
    const stats = diffStats(parseUnifiedDiff(unified));
    expect(stats.added).toBe(1);
    expect(stats.removed).toBe(1);
  });

  it('aligns removals and additions side by side', () => {
    const rows = toSideBySide(parseUnifiedDiff(unified)[0]);
    const pair = rows.find((row) => row.left?.kind === 'remove');
    expect(pair?.left?.kind).toBe('remove');
    expect(pair?.right?.kind).toBe('add');
  });
});

describe('graph helpers', () => {
  const nodes = [
    { id: 'pkg:pypi/flask@2.0.0', label: 'flask', name: 'flask', version: '2.0.0', direct: true, maxSeverity: 'high' as const, findingCount: 1, vulnCount: 0 },
    { id: 'pkg:pypi/requests@2.25.0', label: 'requests', name: 'requests', version: '2.25.0', direct: false, maxSeverity: 'medium' as const, findingCount: 0, vulnCount: 1 },
    { id: 'pkg:pypi/urllib3@1.26.0', label: 'urllib3', name: 'urllib3', version: '1.26.0', direct: false, maxSeverity: 'low' as const, findingCount: 0, vulnCount: 1 },
  ];
  const edges = [
    { source: 'pkg:pypi/flask@2.0.0', target: 'pkg:pypi/requests@2.25.0' },
    { source: 'pkg:pypi/requests@2.25.0', target: 'pkg:pypi/urllib3@1.26.0' },
  ];

  it('finds the dependency path from the root to a node', () => {
    const path = findPath(nodes, edges, 'pkg:pypi/urllib3@1.26.0', 'pkg:pypi/flask@2.0.0');
    expect(path).toEqual([
      'pkg:pypi/flask@2.0.0',
      'pkg:pypi/requests@2.25.0',
      'pkg:pypi/urllib3@1.26.0',
    ]);
  });

  it('detects dependency cycles', () => {
    expect(countCycles(edges)).toBe(0);
    expect(
      countCycles([...edges, { source: 'pkg:pypi/urllib3@1.26.0', target: 'pkg:pypi/requests@2.25.0' }]),
    ).toBe(1);
  });
});

describe('model helpers mirror the Go implementation', () => {
  it('parses PURLs', () => {
    expect(purlName('pkg:pypi/flask@2.0.0')).toBe('flask');
    expect(purlVersion('pkg:pypi/flask@2.0.0')).toBe('2.0.0');
    expect(packageURL('Flask', '2.0.0')).toBe('pkg:pypi/flask@2.0.0');
  });

  it('ranks severities like model.severityRank', () => {
    expect(SEVERITY_RANK.critical).toBe(5);
    expect(SEVERITY_RANK.info).toBe(1);
    expect(maxSeverity('low', 'critical')).toBe('critical');
    expect(maxSeverity('medium', 'medium')).toBe('medium');
  });
});


describe('progress stats', () => {
  it('formats durations', () => {
    expect(formatDuration(45)).toBe('45s');
    expect(formatDuration(185)).toBe('3m 05s');
  });
  it('estimates downloads from packages done', () => {
    const text = progressStats({
      now: 10_000, startedAt: 0, phaseStartedAt: 0, phaseStartPercent: 0, percent: 0.3,
      downloadsDone: 5, downloadsTotal: 20, downloadedBytes: 10 * 1024 * 1024,
    });
    expect(text).toContain('5/20 packages');
    expect(text).toContain('1.0 MB/s');
    expect(text).toContain('about 30s left');
  });
  it('estimates scans from percent, and waits for enough progress', () => {
    const base = { now: 20_000, startedAt: 0, phaseStartedAt: 0, phaseStartPercent: 0, downloadsDone: 0, downloadsTotal: 0, downloadedBytes: 0 };
    expect(progressStats({ ...base, percent: 0.5 })).toContain('about 20s left');
    expect(progressStats({ ...base, percent: 0.01 })).not.toContain('left');
  });
});

describe('the debug store', () => {
  it('keeps breakpoints sorted and unique, and forgets empty files', async () => {
    const { useDebugStore } = await import('../src/state/debug');
    const store = useDebugStore.getState();
    store.setAudit('a1');
    store.setBreakpoints('pkgs/demo/app.py', [5, 2, 5, 0]);
    expect(useDebugStore.getState().breakpoints['pkgs/demo/app.py']).toEqual([2, 5]);
    store.toggleBreakpoint('pkgs/demo/app.py', 2);
    store.toggleBreakpoint('pkgs/demo/app.py', 9);
    expect(useDebugStore.getState().breakpoints['pkgs/demo/app.py']).toEqual([5, 9]);
    store.setBreakpoints('pkgs/demo/app.py', []);
    expect(useDebugStore.getState().breakpoints).toEqual({});
  });

  it('drops the breakpoints of another audit', async () => {
    const { useDebugStore } = await import('../src/state/debug');
    useDebugStore.getState().setAudit('a1');
    useDebugStore.getState().toggleBreakpoint('x.py', 3);
    useDebugStore.getState().setAudit('a2');
    expect(useDebugStore.getState().breakpoints).toEqual({});
  });

  it('reports the paused line only for the selected frame of that file', async () => {
    const { useDebugStore, pausedLineIn } = await import('../src/state/debug');
    useDebugStore.getState().stopped({
      reason: 'breakpoint',
      frames: [
        { file: '/usr/lib/python3/json/__init__.py', line: 40, name: 'dumps', external: true },
        { file: 'pkgs/demo/app.py', line: 7, name: 'main' },
      ],
      frame: 1,
    });
    expect(pausedLineIn(useDebugStore.getState(), 'pkgs/demo/app.py')).toBe(7);
    useDebugStore.setState({ frame: 0 });
    expect(pausedLineIn(useDebugStore.getState(), 'pkgs/demo/app.py')).toBeNull();
    useDebugStore.getState().continued();
    expect(useDebugStore.getState().status).toBe('running');
  });

  it('joins output chunks and caps the console', async () => {
    const { appendConsole } = await import('../src/state/debug');
    let lines = appendConsole([], { kind: 'stdout', text: 'he' });
    lines = appendConsole(lines, { kind: 'stdout', text: 'llo\n' });
    lines = appendConsole(lines, { kind: 'stderr', text: 'oops' });
    expect(lines).toEqual([{ kind: 'stdout', text: 'hello\n' }, { kind: 'stderr', text: 'oops' }]);
    const big = appendConsole([], { kind: 'stdout', text: 'x'.repeat(250_000) });
    expect(big[0].text.length).toBe(200_000);
  });
});

describe('the dependency path and package queries', () => {
  const node = (id: string, name: string, extra: Partial<{ direct: boolean; root: boolean }> = {}) =>
    ({ id, label: name, name, version: '1', direct: false, maxSeverity: 'info', findingCount: 0, vulnCount: 0, ...extra }) as never;

  it('finds a path even when another dependent of the target cannot reach the root', async () => {
    const { findPath } = await import('../src/state/graph');
    // root → a → t, and an orphan b → t seen later.
    const nodes = [node('root', 'root', { root: true }), node('a', 'a'), node('b', 'b'), node('t', 't')];
    const edges = [{ source: 'root', target: 'a' }, { source: 'a', target: 't' }, { source: 'b', target: 't' }];
    expect(findPath(nodes, edges, 't', 'root')).toEqual(['root', 'a', 't']);
    expect(findPath(nodes, edges, 'root', 'root')).toEqual(['root']);
    expect(findPath(nodes, [{ source: 'b', target: 't' }], 't', 'root')).toEqual([]);
  });

  it('reduces what people paste to a package name', async () => {
    const { packageNameFromQuery } = await import('../src/state/graph');
    expect(packageNameFromQuery('urllib3>=2.0; python_version<"3.12"')).toBe('urllib3');
    expect(packageNameFromQuery('requests[socks]==2.31.0')).toBe('requests');
    expect(packageNameFromQuery('pkg:pypi/urllib3@2.2.1')).toBe('urllib3');
    expect(packageNameFromQuery('https://pypi.org/project/Jinja2/')).toBe('Jinja2');
    expect(packageNameFromQuery('lodash@4.17.21')).toBe('lodash');
    expect(packageNameFromQuery('@types/node@20.1.0')).toBe('@types/node');
    expect(packageNameFromQuery('  "MarkupSafe"  ')).toBe('MarkupSafe');
  });

  it('matches names the way Python normalises them, and suggests close spellings', async () => {
    const { matchPackage } = await import('../src/state/graph');
    const nodes = [node('1', 'typing_extensions'), node('2', 'urllib3'), node('3', 'charset-normalizer'), node('4', 'certifi')];
    expect(matchPackage(nodes, 'Typing-Extensions').match?.name).toBe('typing_extensions');
    expect(matchPackage(nodes, 'charset').match?.name).toBe('charset-normalizer');
    const typo = matchPackage(nodes, 'urlib3');
    expect(typo.match).toBeNull();
    expect(typo.fuzzy).toBe(true);
    expect(typo.suggestions[0].name).toBe('urllib3');
    expect(matchPackage(nodes, 'zzzz').suggestions).toEqual([]);
  });
});

describe('the line diff of a proposed change', () => {
  it('keeps shared lines and marks the rest', async () => {
    const { diffCounts, diffLines } = await import('../src/lib/linediff');
    const diff = diffLines('a\nb\nc\n', 'a\nB\nc\nd\n');
    expect(diff.map((l) => `${l.kind[0]}${l.text}`)).toEqual(['sa', 'rb', 'aB', 'sc', 'ad']);
    expect(diffCounts(diff)).toEqual({ added: 2, removed: 1 });
  });
});

describe('key bindings', () => {
  it('writes specs canonically and translates them for CodeMirror and menus', async () => {
    const { normalizeSpec, toAccelerator, toCodeMirror, isPlainTypingKey } = await import('../src/lib/keybindings');
    expect(normalizeSpec('shift+mod+z')).toBe('Mod+Shift+Z');
    expect(normalizeSpec('f9')).toBe('F9');
    expect(toCodeMirror('Mod+Shift+Z')).toBe('Mod-Shift-z');
    expect(toCodeMirror('F9')).toBe('F9');
    expect(toAccelerator('Mod+Shift+E')).toBe('CmdOrCtrl+Shift+E');
    expect(toAccelerator('Mod+,')).toBe('CmdOrCtrl+,');
    expect(isPlainTypingKey('K')).toBe(true);
    expect(isPlainTypingKey('Shift+K')).toBe(true);
    expect(isPlainTypingKey('F5')).toBe(false);
    expect(isPlainTypingKey('Mod+K')).toBe(false);
  });

  it('records a key press as a spec', async () => {
    const { specFromEvent } = await import('../src/lib/keybindings');
    const press = (init: Partial<KeyboardEvent>) => ({ key: 'k', code: 'KeyK', metaKey: false, ctrlKey: false, altKey: false, shiftKey: false, ...init }) as KeyboardEvent;
    expect(specFromEvent(press({ metaKey: true }), true)).toBe('Mod+K');
    expect(specFromEvent(press({ ctrlKey: true }), false)).toBe('Mod+K');
    expect(specFromEvent(press({ ctrlKey: true }), true)).toBe('Ctrl+K');
    expect(specFromEvent(press({ key: 'Shift', code: 'ShiftLeft', shiftKey: true }), true)).toBeNull();
    expect(specFromEvent(press({ key: '!', code: 'Digit1', shiftKey: true, metaKey: true }), true)).toBe('Mod+Shift+1');
  });
});

describe('reading keys on macOS', () => {
  it('takes the letter from the physical key when Option changes the character', async () => {
    const { specFromEvent } = await import('../src/lib/keybindings');
    const press = { key: 'ß', code: 'KeyS', metaKey: true, altKey: true, ctrlKey: false, shiftKey: false } as KeyboardEvent;
    expect(specFromEvent(press, true)).toBe('Mod+Alt+S');
  });
});

describe('the error screen hider', () => {
  it('restores only what it hid, so an element meant to stay hidden stays hidden', async () => {
    const { createHider } = await import('../src/views/errorScreen');
    const shell = { hidden: false };
    const tourOverlay = { hidden: true }; // only shows while a tour runs
    const hider = createHider([shell, tourOverlay]);
    hider.hide();
    expect(shell.hidden).toBe(true);
    hider.restore();
    expect(shell.hidden).toBe(false);
    expect(tourOverlay.hidden).toBe(true);
    // Restoring when nothing was hidden changes nothing.
    hider.restore();
    expect(tourOverlay.hidden).toBe(true);
  });
});

describe('vulnerable dependencies', () => {
  it('lists the worst first and reads a package url', async () => {
    const { packageLabel, sortAdvisories } = await import('../src/state/advisories');
    const sorted = sortAdvisories([
      { id: 'B', source: 'osv', summary: '', severity: 'low', package: 'pkg:cargo/b@1' },
      { id: 'A2', source: 'osv', summary: '', severity: 'critical', package: 'pkg:cargo/z@1' },
      { id: 'A1', source: 'osv', summary: '', severity: 'critical', package: 'pkg:cargo/a@1' },
    ]);
    expect(sorted.map((a) => a.id)).toEqual(['A1', 'A2', 'B']);
    expect(packageLabel('pkg:cargo/time@0.1.43')).toBe('time 0.1.43');
    expect(packageLabel('pkg:npm/@types/node@20.1.0')).toBe('@types/node 20.1.0');
    expect(packageLabel(undefined)).toBe('');
  });

  it('tells "not checked" from "nothing found"', async () => {
    const { useAdvisoryStore } = await import('../src/state/advisories');
    const store = useAdvisoryStore.getState();
    store.setList([], 0, false);
    expect(useAdvisoryStore.getState().status).toBe('unknown');
    store.setList([], 40, true);
    expect(useAdvisoryStore.getState().status).toBe('ready');
    expect(useAdvisoryStore.getState().checked).toBe(40);
    store.setChecking();
    expect(useAdvisoryStore.getState().status).toBe('checking');
    store.reset();
    expect(useAdvisoryStore.getState().status).toBe('unknown');
  });
});
