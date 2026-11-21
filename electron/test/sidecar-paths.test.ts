import path from 'node:path';

import { describe, expect, it } from 'vitest';

import { sidecarCandidates } from '../src/main-process/sidecar-paths';

/**
 * The order in which the app looks for the Go sidecar.
 *
 * The order is part of the contract. `npm run build` copies the Go binary
 * next to the compiled main process, and that copy goes stale the moment the
 * Go code changes. In development the fresh `bin/` build has to win.
 *
 * A regression here is invisible: the app starts, it just runs yesterday's
 * backend. The symptom that showed it up was an event stream that kept
 * dying, because the copy in `dist/main/` predated the fix.
 */
describe('sidecarCandidates', () => {
  it('puts the explicit override first', () => {
    const candidates = sidecarCandidates('/repo/electron', '/res', '/app', '/custom/aisai-server');
    expect(candidates[0].path).toBe('/custom/aisai-server');
    expect(candidates[0].origin).toBe('AISAI_SERVER_BIN');
  });

  it('finds the repository build one level above the electron directory', () => {
    // `electron .` from <repo>/electron reports <repo>/electron as the
    // application path, so the repository is its parent.
    const candidates = sidecarCandidates('/repo/electron', '', '/app', undefined);
    const paths = candidates.map((c) => c.path);
    expect(paths).toContain(path.join('/repo', 'bin', 'aisai-server'));
  });

  it('finds the repository build when the application path is the root', () => {
    const candidates = sidecarCandidates('/repo', '', '/app', undefined);
    const paths = candidates.map((c) => c.path);
    expect(paths).toContain(path.join('/repo', 'bin', 'aisai-server'));
  });

  it('puts the repository build before the copy next to the main process', () => {
    const candidates = sidecarCandidates('/repo/electron', '', '/app', undefined);
    const repoIndex = candidates.findIndex((c) => c.path === path.join('/repo', 'bin', 'aisai-server'));
    const appIndex = candidates.findIndex((c) => c.path === path.join('/app', 'aisai-server'));
    expect(repoIndex).toBeGreaterThanOrEqual(0);
    expect(appIndex).toBeGreaterThanOrEqual(0);
    expect(repoIndex).toBeLessThan(appIndex);
  });

  it('uses the resources directory of a packaged app', () => {
    const candidates = sidecarCandidates('/res/app.asar', '/res', '/app', undefined);
    const paths = candidates.map((c) => c.path);
    expect(paths).toContain(path.join('/res', 'aisai-server'));
  });

  it('always offers the copy next to the main process as the last resort', () => {
    const candidates = sidecarCandidates('', '', '/app', undefined);
    const last = candidates[candidates.length - 1];
    expect(last.path).toBe(path.join('/app', 'aisai-server'));
    expect(last.origin).toBe('application directory');
  });

  it('does not repeat a path', () => {
    const candidates = sidecarCandidates('/repo', '', '/repo', undefined);
    const paths = candidates.map((c) => c.path);
    expect(new Set(paths).size).toBe(paths.length);
  });
});
