import { describe, expect, it } from 'vitest';

import { diffStats, parseUnifiedDiff, toSideBySide } from '../src/lib/diff';
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
