/**
 * The Playwright global setup.
 *
 * Two things have to happen before the first test:
 *
 *   1. the Electron main process and the renderer have to be built, because
 *      the test starts the app from dist/main/main.js;
 *   2. the stub backend has to be bundled into a single CommonJS file,
 *      because the Electron main process starts a sidecar with
 *      utilityProcess.fork, which runs a Node module and not a bundle of
 *      TypeScript.
 */

import { spawnSync } from 'node:child_process';
import { existsSync, mkdirSync } from 'node:fs';
import { dirname, join } from 'node:path';

import type { FullConfig } from '@playwright/test';
import { build } from 'esbuild';

/**
 * The directory the bundled stub is written to.
 *
 * It is inside `out/`, which is both in .gitignore and in the eslint
 * ignores, so the generated bundle is never committed and never linted.
 */
export const STUB_DIR_NAME = join('out', 'e2e');

/** The bundled stub backend, relative to the Electron directory. */
export const STUB_RELATIVE_PATH = join(STUB_DIR_NAME, 'stub-backend.cjs');

/**
 * The Electron directory.
 *
 * The path of the config file is used rather than import.meta, because
 * Playwright compiles the TypeScript of a test to CommonJS, where
 * import.meta does not exist.
 */
export function electronDirOf(config: FullConfig): string {
  return config.configFile ? dirname(config.configFile) : config.rootDir;
}

/** Builds the Electron app when it has not been built yet. */
function ensureAppIsBuilt(electronDir: string): void {
  const mainBundle = join(electronDir, 'dist', 'main', 'main.js');
  if (existsSync(mainBundle)) {
    return;
  }
  console.log('[e2e] the app is not built yet, running "npm run build"…');
  const result = spawnSync('npm', ['run', 'build'], {
    cwd: electronDir,
    stdio: 'inherit',
    shell: globalThis.process.platform === 'win32',
  });
  if (result.status !== 0) {
    throw new Error('"npm run build" failed; the end-to-end test cannot start.');
  }
}

/** Bundles the stub backend. */
async function buildStub(electronDir: string): Promise<void> {
  const stubDir = join(electronDir, STUB_DIR_NAME);
  mkdirSync(stubDir, { recursive: true });
  await build({
    entryPoints: [join(electronDir, 'e2e', 'fixtures', 'stub-backend.ts')],
    outfile: join(stubDir, 'stub-backend.cjs'),
    bundle: true,
    platform: 'node',
    target: 'node20',
    format: 'cjs',
    sourcemap: 'inline',
    // The bundle is generated code; it must never be linted.
    banner: { js: '/* eslint-disable */' },
    logLevel: 'warning',
  });
}

export default async function globalSetup(config: FullConfig): Promise<void> {
  const electronDir = electronDirOf(config);
  ensureAppIsBuilt(electronDir);
  await buildStub(electronDir);
}
