#!/usr/bin/env node
/**
 * Bundles the Electron main and preload processes.
 *
 * Both are CommonJS: the preload runs in a sandboxed renderer, which only
 * supports CommonJS, and the main process is loaded by Electron's `main`
 * field. esbuild is used because Vite only builds the renderer.
 */

import { build } from 'esbuild';
import { copyFile, mkdir, rm, stat } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(here, '..');
const outDir = path.join(root, 'dist', 'main');

await rm(outDir, { recursive: true, force: true });
await mkdir(outDir, { recursive: true });

const shared = {
  bundle: true,
  platform: 'node',
  format: 'cjs',
  target: 'node22',
  sourcemap: true,
  external: ['electron'],
  logLevel: 'info',
};

await build({
  ...shared,
  entryPoints: [path.join(root, 'main.ts')],
  outfile: path.join(outDir, 'main.js'),
});

await build({
  ...shared,
  entryPoints: [path.join(root, 'preload.ts')],
  outfile: path.join(outDir, 'preload.js'),
});

// Copy the Go sidecar next to the main process when it has been built.
// `go build -o electron/dist/main/aisai-server ./cmd/aisai-server` also works;
// this keeps `npm run build` self-sufficient when ./bin/aisai-server exists.
const candidates = [
  path.join(root, '..', 'bin', 'aisai-server'),
  path.join(root, 'build', 'sidecar', 'aisai-server'),
];
for (const candidate of candidates) {
  try {
    await stat(candidate);
    await copyFile(candidate, path.join(outDir, 'aisai-server'));
    console.log(`copied sidecar from ${candidate}`);
    break;
  } catch {
    /* not built yet — the app falls back to mock data */
  }
}

console.log('main + preload built into dist/main');
