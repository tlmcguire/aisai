import { createHash } from 'node:crypto';
import { describe, expect, it } from 'vitest';

import {
  bundlePath,
  canReplaceAt,
  needsCustomUpdate,
  pickZip,
  prepareUpdate,
  releaseFileUrl,
  swapScript,
  type Runner,
} from '../src/main-process/macUpdate';

const files = [
  { url: 'AISAI-0.5.0-mac-x64.zip', sha512: 'a' },
  { url: 'AISAI-0.5.0-mac-arm64.zip', sha512: 'b' },
  { url: 'AISAI-0.5.0-mac-arm64.dmg', sha512: 'c' },
];

const runner = (outputs: Record<string, { code?: number; stdout?: string; stderr?: string }>): Runner => async (cmd) => {
  const o = outputs[cmd] ?? {};
  return { code: o.code ?? 0, stdout: o.stdout ?? '', stderr: o.stderr ?? '' };
};

describe('macOS self-update', () => {
  it('finds the app bundle of the executable', () => {
    expect(bundlePath('/Applications/AISAI.app/Contents/MacOS/AISAI')).toBe('/Applications/AISAI.app');
    expect(bundlePath('/usr/local/bin/electron')).toBeNull();
  });

  it('refuses places the app cannot replace itself in', () => {
    expect(canReplaceAt('/Applications/AISAI.app').ok).toBe(true);
    expect(canReplaceAt('/private/var/folders/x/AppTranslocation/ABC/d/AISAI.app').ok).toBe(false);
    expect(canReplaceAt('/Volumes/AISAI 0.5.0/AISAI.app').ok).toBe(false);
  });

  it('picks the zip for the architecture', () => {
    expect(pickZip(files, 'arm64')?.url).toBe('AISAI-0.5.0-mac-arm64.zip');
    expect(pickZip(files, 'x64')?.url).toBe('AISAI-0.5.0-mac-x64.zip');
    expect(pickZip([{ url: 'a.dmg', sha512: '' }], 'arm64')).toBeNull();
  });

  it('builds the GitHub download URL', () => {
    expect(releaseFileUrl('o', 'r', '0.5.0', 'AISAI 0.5.0.zip')).toBe(
      'https://github.com/o/r/releases/download/v0.5.0/AISAI-0.5.0.zip',
    );
  });

  it('only takes over for ad-hoc or unsigned builds', async () => {
    expect(await needsCustomUpdate('/A.app', runner({ '/usr/bin/codesign': { stderr: 'Signature=adhoc' } }))).toBe(true);
    expect(await needsCustomUpdate('/A.app', runner({ '/usr/bin/codesign': { stderr: 'code object is not signed at all', code: 1 } }))).toBe(true);
    expect(
      await needsCustomUpdate('/A.app', runner({ '/usr/bin/codesign': { stderr: 'Authority=Developer ID Application: X (T)' } })),
    ).toBe(false);
  });

  it('refuses a download whose checksum does not match the release', async () => {
    const body = new TextEncoder().encode('not the real zip');
    const fetcher = (async () => new Response(body, { headers: { 'content-length': String(body.length) } })) as typeof fetch;
    await expect(
      prepareUpdate({ url: 'https://x/u.zip', sha512: 'wrong', bundleId: 'dev.aisai.app', onProgress: () => {}, fetcher, runner: runner({}) }),
    ).rejects.toThrow(/checksum/);
  });

  it('refuses an archive that holds a different app', async () => {
    const body = new TextEncoder().encode('zip');
    const sha = createHash('sha512').update(body).digest('base64');
    const fetcher = (async () => new Response(body)) as typeof fetch;
    // ditto "unpacks" nothing, so no .app is found.
    await expect(
      prepareUpdate({ url: 'https://x/u.zip', sha512: sha, bundleId: 'dev.aisai.app', onProgress: () => {}, fetcher, runner: runner({}) }),
    ).rejects.toThrow(/exactly one app/);
  });

  it('swaps the app only after the old process exits, and restores it on failure', () => {
    const script = swapScript(42, "/tmp/u/new/AISAI.app", "/Applications/AISAI's.app");
    expect(script).toContain('while kill -0 42');
    expect(script).toContain("'/Applications/AISAI'\\''s.app'");
    expect(script.indexOf('mv')).toBeGreaterThan(script.indexOf('kill -0'));
    expect(script).toMatch(/else\n\s+mv .*previous.*AISAI/);
  });
});
