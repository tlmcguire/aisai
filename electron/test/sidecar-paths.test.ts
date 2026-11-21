import path from 'node:path';

import { describe, expect, it } from 'vitest';

import { sidecarCandidates } from '../src/main-process/sidecar-paths';

/**
 * The order in which the app looks for the Go sidecar.
 *
 * The order is part of the contract. `npm run build` copies the Go binary
 * next to the compiled main process, and that copy goes stale the moment
 * the Go code changes. In development the fresh `bin/` build has to win.
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

  it('offers the Windows name next to the override', () => {
    const candidates = sidecarCandidates('/repo/electron', '/res', '/app', '/custom/aisai-server');
    const paths = candidates.map((c) => c.path);
    expect(paths).toContain('/custom/aisai-server.exe');
  });

  it('does not add a second extension to an override that has one', () => {
    const candidates = sidecarCandidates('', '', '/app', 'C:\\tools\\aisai-server.exe');
    const paths = candidates.map((c) => c.path);
    expect(paths.filter((p) => p === 'C:\\tools\\aisai-server.exe')).toHaveLength(1);
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

  it('offers the Windows name in the resources directory as well', () => {
    const candidates = sidecarCandidates('/res/app.asar', '/res', '/app', undefined);
    const paths = candidates.map((c) => c.path);
    expect(paths).toContain(path.join('/res', 'aisai-server.exe'));
  });

  it('puts the resources directory before the copy next to the main process', () => {
    // The copy in the asar archive cannot be executed, so the copy that
    // lives outside it has to be found first.
    const candidates = sidecarCandidates('/res/app.asar', '/res', '/app', undefined);
    const resourcesIndex = candidates.findIndex((c) => c.path === path.join('/res', 'aisai-server'));
    const appIndex = candidates.findIndex((c) => c.path === path.join('/app', 'aisai-server'));
    expect(resourcesIndex).toBeGreaterThanOrEqual(0);
    expect(appIndex).toBeGreaterThanOrEqual(0);
    expect(resourcesIndex).toBeLessThan(appIndex);
  });

  it('always offers the copy next to the main process as the last resort', () => {
    const candidates = sidecarCandidates('', '', '/app', undefined);
    const last = candidates[candidates.length - 1];
    expect(last.path).toBe(path.join('/app', 'aisai-server.exe'));
    expect(last.origin).toBe('application directory');
    expect(candidates[candidates.length - 2].path).toBe(path.join('/app', 'aisai-server'));
  });

  it('does not repeat a path', () => {
    const candidates = sidecarCandidates('/repo', '', '/repo', undefined);
    const paths = candidates.map((c) => c.path);
    expect(new Set(paths).size).toBe(paths.length);
  });
});
