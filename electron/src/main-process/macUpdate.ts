/**
 * Self-update for macOS builds that are not signed with a Developer ID.
 *
 * electron-updater installs a macOS update through Squirrel.Mac, which only
 * accepts a new app whose code signature satisfies the designated requirement
 * of the running one. An ad-hoc signature's requirement is its own content
 * hash, so no new version can ever satisfy it, and every update would need a
 * fresh download from the release page. This module replaces the install step
 * for those builds:
 *
 *   1. electron-updater still checks the feed (the GitHub release of the
 *      project) and reports the new version and its files.
 *   2. The zip for this machine's architecture is downloaded over HTTPS and
 *      its SHA-512 is checked against the release's latest-mac.yml, the same
 *      check electron-updater makes.
 *   3. It is unpacked with ditto, and the unpacked app must pass
 *      `codesign --verify --deep --strict` and carry this app's bundle id.
 *   4. On "Restart to update" a small script waits for this process to exit,
 *      moves the new app into place (keeping the old one until the new one is
 *      in), and opens it.
 *
 * Nothing here is used on a build with a real signature, nor on Windows or
 * Linux, where electron-updater installs updates itself.
 */

import { createHash } from 'node:crypto';
import { execFile } from 'node:child_process';
import { createWriteStream } from 'node:fs';
import { chmod, mkdir, mkdtemp, readdir, rm, stat, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { Readable } from 'node:stream';
import { pipeline } from 'node:stream/promises';

/** A file of a release, as latest-mac.yml lists it. */
export interface ReleaseFile {
  url: string;
  sha512: string;
  size?: number;
}

/** Runs a command and resolves to its exit code, stdout and stderr. */
export type Runner = (cmd: string, args: string[]) => Promise<{ code: number; stdout: string; stderr: string }>;

export const run: Runner = (cmd, args) =>
  new Promise((resolve) => {
    execFile(cmd, args, { maxBuffer: 4 << 20 }, (error, stdout, stderr) => {
      const code = error ? (typeof (error as { code?: unknown }).code === 'number' ? (error as { code: number }).code : 1) : 0;
      resolve({ code, stdout: String(stdout), stderr: String(stderr) });
    });
  });

/** The .app bundle the running executable belongs to, or null outside one. */
export function bundlePath(exePath: string): string | null {
  // .../AISAI.app/Contents/MacOS/AISAI
  const bundle = path.resolve(exePath, '..', '..', '..');
  return bundle.endsWith('.app') ? bundle : null;
}

/**
 * Whether the app runs from a place it can replace itself in. A quarantined
 * app opened from Downloads is "translocated" to a random read-only path, and
 * a disk image is read-only too.
 */
export function canReplaceAt(bundle: string): { ok: boolean; reason?: string } {
  if (bundle.includes('/AppTranslocation/')) {
    return { ok: false, reason: 'Move AISAI into the Applications folder and open it from there, then it can update itself.' };
  }
  if (bundle.startsWith('/Volumes/')) {
    return { ok: false, reason: 'AISAI is running from a disk image. Copy it into Applications to let it update itself.' };
  }
  return { ok: true };
}

/** Whether the running app needs this module: ad-hoc or not signed at all. */
export async function needsCustomUpdate(bundle: string, runner: Runner = run): Promise<boolean> {
  const result = await runner('/usr/bin/codesign', ['-dv', '--verbose=2', bundle]);
  const out = `${result.stdout}\n${result.stderr}`;
  if (/Signature=adhoc/.test(out) || /not signed at all/.test(out)) return true;
  // A Developer ID signature names its authority; Squirrel.Mac handles that.
  return !/Authority=Developer ID Application/.test(out);
}

/** The zip of the release for this architecture. */
export function pickZip(files: ReleaseFile[], arch: string): ReleaseFile | null {
  const zips = files.filter((f) => f.url.endsWith('.zip'));
  const tag = arch === 'arm64' ? 'arm64' : 'x64';
  return (
    zips.find((f) => f.url.includes(`-${tag}.zip`) || f.url.includes(`-${tag}-`)) ??
    // A universal or single-architecture release.
    (zips.length === 1 ? zips[0] : null)
  );
}

/** The download URL of a release file on GitHub. */
export function releaseFileUrl(owner: string, repo: string, version: string, file: string): string {
  if (/^https:\/\//.test(file)) return file;
  const name = file.replace(/ /g, '-');
  return `https://github.com/${owner}/${repo}/releases/download/v${version}/${encodeURIComponent(name)}`;
}

/** Downloads to a file, reporting progress, and returns the SHA-512 (base64). */
export async function download(
  url: string,
  target: string,
  onProgress: (percent: number) => void,
  fetcher: typeof fetch = fetch,
): Promise<string> {
  const response = await fetcher(url, { redirect: 'follow' });
  if (!response.ok || !response.body) throw new Error(`download failed: HTTP ${response.status}`);
  const total = Number(response.headers.get('content-length') ?? 0);
  const hash = createHash('sha512');
  let received = 0;
  let lastPercent = -1;
  const body = Readable.fromWeb(response.body as unknown as import('node:stream/web').ReadableStream);
  body.on('data', (chunk: Buffer) => {
    hash.update(chunk);
    received += chunk.length;
    if (total > 0) {
      const percent = Math.floor((received / total) * 100);
      if (percent !== lastPercent) {
        lastPercent = percent;
        onProgress(percent);
      }
    }
  });
  await pipeline(body, createWriteStream(target));
  return hash.digest('base64');
}

/**
 * Downloads, checks and unpacks an update. Resolves to the path of the new
 * .app, ready to be installed.
 */
export async function prepareUpdate(options: {
  url: string;
  sha512: string;
  bundleId: string;
  onProgress: (percent: number) => void;
  runner?: Runner;
  fetcher?: typeof fetch;
}): Promise<string> {
  const runner = options.runner ?? run;
  const work = await mkdtemp(path.join(os.tmpdir(), 'aisai-update-'));
  const zip = path.join(work, 'update.zip');
  const digest = await download(options.url, zip, options.onProgress, options.fetcher);
  if (digest !== options.sha512) {
    await rm(work, { recursive: true, force: true });
    throw new Error('the downloaded update does not match the checksum of the release');
  }
  const out = path.join(work, 'unpacked');
  await mkdir(out);
  const unzip = await runner('/usr/bin/ditto', ['-x', '-k', zip, out]);
  if (unzip.code !== 0) throw new Error(`the update could not be unpacked: ${unzip.stderr.trim()}`);
  await rm(zip, { force: true });

  const apps = (await readdir(out)).filter((name) => name.endsWith('.app'));
  if (apps.length !== 1) throw new Error('the update does not contain exactly one app');
  const app = path.join(out, apps[0]);

  // The seal must be intact (an ad-hoc signature still covers every file).
  const verify = await runner('/usr/bin/codesign', ['--verify', '--deep', '--strict', app]);
  if (verify.code !== 0) throw new Error(`the update's code signature is broken: ${verify.stderr.trim()}`);
  // And it must be this app, not something else in a zip.
  const id = await runner('/usr/bin/defaults', ['read', path.join(app, 'Contents', 'Info'), 'CFBundleIdentifier']);
  if (id.code !== 0 || id.stdout.trim() !== options.bundleId) {
    throw new Error(`the update is a different app (${id.stdout.trim() || 'unknown'})`);
  }
  // A file the app downloaded itself is not quarantined, but clear it anyway
  // so Gatekeeper does not call the new app damaged.
  await runner('/usr/bin/xattr', ['-dr', 'com.apple.quarantine', app]);
  return app;
}

/** Quotes a path for /bin/sh. */
function sh(value: string): string {
  return `'${value.replace(/'/g, `'\\''`)}'`;
}

/**
 * The script that swaps the app once this process has exited: wait for the
 * pid, move the old app aside, move the new one in, put the old one back if
 * that failed, then open the result.
 */
export function swapScript(pid: number, newApp: string, bundle: string): string {
  const backup = `${bundle}.previous`;
  return [
    '#!/bin/sh',
    `while kill -0 ${pid} 2>/dev/null; do sleep 0.2; done`,
    `rm -rf ${sh(backup)}`,
    `if mv ${sh(bundle)} ${sh(backup)}; then`,
    `  if mv ${sh(newApp)} ${sh(bundle)}; then`,
    `    rm -rf ${sh(backup)}`,
    '  else',
    `    mv ${sh(backup)} ${sh(bundle)}`,
    '  fi',
    'fi',
    `/usr/bin/open ${sh(bundle)}`,
    '',
  ].join('\n');
}

/** Writes the swap script and starts it detached. The caller then quits. */
export async function startSwap(newApp: string, bundle: string, spawnDetached: (script: string) => void): Promise<void> {
  // The folder holding the app must be writable, or the move would fail after
  // the app had already quit.
  const parent = path.dirname(bundle);
  const probe = path.join(parent, `.aisai-write-test-${process.pid}`);
  try {
    await writeFile(probe, '');
    await rm(probe, { force: true });
  } catch {
    throw new Error(`AISAI cannot write to ${parent}. Install it in your Applications folder, or update it from the release page.`);
  }
  await stat(newApp);
  const script = path.join(path.dirname(newApp), '..', 'swap.sh');
  await writeFile(script, swapScript(process.pid, newApp, bundle));
  await chmod(script, 0o700);
  spawnDetached(script);
}
