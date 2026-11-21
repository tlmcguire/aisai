import { describe, expect, it, beforeEach } from 'vitest';

import { MockApi, MockApiError } from '../src/api/mock';
import type {
  AuditResponse,
  AstResponse,
  FileResponse,
  FilesResponse,
  FindingsResponse,
  GraphResponse,
  HealthResponse,
  PatchResponse,
} from '../src/api/types';

describe('mock AISAI backend', () => {
  let api: MockApi;

  beforeEach(() => {
    api = new MockApi();
  });

  it('serves the documented health envelope', async () => {
    const health = await api.request<HealthResponse>('GET', '/api/health');
    expect(health.ok).toBe(true);
    expect(typeof health.version).toBe('string');
    expect(typeof health.workspace).toBe('string');
    expect(typeof health.uptimeSeconds).toBe('number');
    expect(Array.isArray(health.providers)).toBe(true);
    expect(health.providers[0]).toMatchObject({ id: expect.any(String), ok: expect.any(Boolean) });
  });

  it('serves the documented audit envelope', async () => {
    const audits = await api.request<{ audits: unknown[] }>('GET', '/api/audits');
    expect(Array.isArray(audits.audits)).toBe(true);
    expect(audits.audits.length).toBeGreaterThan(0);

    const single = await api.request<AuditResponse>('GET', '/api/audits/a1');
    expect(single.audit.id).toBe('a1');
    expect(single.audit.package).toBe('flask');
    expect(single.audit.version).toBe('2.0.0');
    expect(['pending', 'acquiring', 'indexing', 'scanning', 'ready', 'failed']).toContain(
      single.audit.status,
    );
    expect(typeof single.audit.progress).toBe('number');
    expect(typeof single.audit.createdAt).toBe('string');
  });

  it('serves a dependency graph with PURL node ids', async () => {
    const { graph } = await api.request<GraphResponse>('GET', '/api/audits/a1/graph');
    expect(graph.nodes.length).toBeGreaterThanOrEqual(8);
    expect(graph.edges.length).toBeGreaterThan(0);
    for (const node of graph.nodes) {
      expect(node.id).toMatch(/^pkg:pypi\//);
      expect(node.label).toContain('\n');
      expect(['critical', 'high', 'medium', 'low', 'info']).toContain(node.maxSeverity);
      expect(typeof node.direct).toBe('boolean');
    }
    const ids = new Set(graph.nodes.map((node) => node.id));
    for (const edge of graph.edges) {
      expect(ids.has(edge.source)).toBe(true);
      expect(ids.has(edge.target)).toBe(true);
    }
  });

  it('serves a file tree that marks directories with findings', async () => {
    const { tree } = await api.request<FilesResponse>('GET', '/api/audits/a1/files');
    expect(tree.type).toBe('dir');
    expect(tree.children?.length).toBeGreaterThan(0);
    const flaskDir = tree.children?.find((child) => child.name === 'flask');
    expect(flaskDir?.findingCount).toBeGreaterThan(0);
    expect(flaskDir?.maxSeverity).toBeDefined();
  });

  it('serves file content as { lines: string[] }', async () => {
    const { file } = await api.request<FileResponse>(
      'GET',
      '/api/audits/a1/file?path=flask/security.py',
    );
    expect(file.path).toBe('flask/security.py');
    expect(file.language).toBe('python');
    expect(Array.isArray(file.lines)).toBe(true);
    expect(file.lines.length).toBeGreaterThan(10);
    expect(file.lines.every((line) => typeof line === 'string')).toBe(true);
  });

  it('serves an AST with nodes and parent/child edges', async () => {
    const { ast } = await api.request<AstResponse>(
      'GET',
      '/api/audits/a1/ast?path=flask/security.py',
    );
    expect(ast.nodes.length).toBeGreaterThan(5);
    expect(ast.edges.length).toBeGreaterThan(0);
    for (const node of ast.nodes) {
      expect(typeof node.type).toBe('string');
      expect(node.startLine).toBeGreaterThan(0);
    }
  });

  it('serves findings with severity, confidence, CWE and triage state', async () => {
    const { findings, total } = await api.request<FindingsResponse>(
      'GET',
      '/api/audits/a1/findings',
    );
    expect(findings.length).toBe(total);
    expect(findings.length).toBeGreaterThanOrEqual(12);
    const severities = new Set(findings.map((finding) => finding.severity));
    expect(severities.size).toBeGreaterThan(2);
    for (const finding of findings) {
      expect(['critical', 'high', 'medium', 'low', 'info']).toContain(finding.severity);
      expect(finding.confidence).toBeGreaterThanOrEqual(0);
      expect(finding.confidence).toBeLessThanOrEqual(1);
      expect(['open', 'confirmed', 'suppressed', 'fixed']).toContain(finding.state);
      expect(typeof finding.file).toBe('string');
      expect(finding.startLine).toBeGreaterThan(0);
    }
  });

  it('filters findings by severity and cwe', async () => {
    const { findings } = await api.request<FindingsResponse>(
      'GET',
      '/api/audits/a1/findings?severity=critical',
    );
    expect(findings.length).toBeGreaterThan(0);
    expect(findings.every((finding) => finding.severity === 'critical')).toBe(true);

    const byCwe = await api.request<FindingsResponse>(
      'GET',
      '/api/audits/a1/findings?cwe=CWE-798',
    );
    expect(byCwe.findings.every((finding) => finding.cwe === 'CWE-798')).toBe(true);
  });

  it('serves a proposed patch', async () => {
    const { patch } = await api.request<PatchResponse>(
      'POST',
      '/api/audits/a1/propose-fix',
      { findingId: 'cwe-798-3f9a2c1d04b7e' },
    );
    expect(patch.findingId).toBe('cwe-798-3f9a2c1d04b7e');
    expect(patch.valid).toBe(true);
    expect(patch.unified).toContain('---');
    expect(patch.unified).toContain('+++');
    expect(patch.unified).toContain('@@');
    expect(patch.summary.length).toBeGreaterThan(0);
  });

  it('applies a patch and reports the rescan', async () => {
    const response = await api.request<{
      applied: boolean;
      rescan: { resolved: string[]; introduced: string[]; unchanged: number };
    }>('POST', '/api/audits/a1/apply-patch', {
      path: 'flask/security.py',
      unified: '--- a/flask/security.py\n+++ b/flask/security.py\n@@ -1 +1 @@\n-a\n+b\n',
    });
    expect(response.applied).toBe(true);
    expect(Array.isArray(response.rescan.resolved)).toBe(true);
    expect(typeof response.rescan.unchanged).toBe('number');
  });

  it('changes the triage state of a finding', async () => {
    const response = await api.request<{ finding: { state: string; suppressedUntil?: string } }>(
      'POST',
      '/api/audits/a1/findings/cwe-798-3f9a2c1d04b7e/state',
      { state: 'suppressed', suppressedUntil: '2026-12-01T00:00:00Z' },
    );
    expect(response.finding.state).toBe('suppressed');
    expect(response.finding.suppressedUntil).toBe('2026-12-01T00:00:00Z');
  });

  it('rejects a suppression without an expiry date', async () => {
    await expect(
      api.request('POST', '/api/audits/a1/findings/cwe-798-3f9a2c1d04b7e/state', {
        state: 'suppressed',
      }),
    ).rejects.toBeInstanceOf(MockApiError);
  });

  it('reports a missing file as not_found', async () => {
    await expect(
      api.request('GET', '/api/audits/a1/file?path=nope.py'),
    ).rejects.toMatchObject({ status: 404, code: 'not_found' });
  });

  it('creates an audit and streams progress events', async () => {
    const events: string[] = [];
    api.onEvent((event) => events.push(event.type));

    const { audit } = await api.request<AuditResponse>('POST', '/api/audits', {
      package: 'flask',
      version: '2.0.0',
    });
    expect(audit.status).toBe('pending');
    expect(audit.id).toContain('flask');

    await new Promise((resolve) => setTimeout(resolve, 800));
    expect(events).toContain('audit.progress');
    expect(events).toContain('audit.done');
    api.dispose();
  });

  it('streams chat tokens and finishes with citations', async () => {
    const deltas: string[] = [];
    let done: { citations: string[]; chunksUsed: string[] } | null = null;
    api.onEvent((event) => {
      if (event.type === 'chat.token') {
        const data = event.data as { delta: string };
        deltas.push(data.delta);
      }
      if (event.type === 'chat.done') {
        done = event.data as { citations: string[]; chunksUsed: string[] };
      }
    });

    const { jobId } = await api.request<{ jobId: string }>('POST', '/api/chat', {
      auditId: 'a1',
      question: 'how does it authenticate?',
      history: [],
    });
    expect(typeof jobId).toBe('string');

    await new Promise((resolve) => setTimeout(resolve, 1200));
    expect(deltas.join('')).toContain('hmac.compare_digest');
    expect(done).not.toBeNull();
    expect((done as unknown as { citations: string[] }).citations.length).toBeGreaterThan(0);
    api.dispose();
  });
});
