/**
 * Locates the Go sidecar binary (`aisai-server`).
 *
 * Search order:
 *   1. AISAI_SERVER_BIN — explicit override (used by CI and by `npm run dev`).
 *   2. <repo>/bin/aisai-server — the `go build` output while developing.
 *   3. <resources>/aisai-server — packaged app (electron-builder extraResources).
 *   4. <__dirname>/aisai-server — next to the compiled main process.
 *
 * The repository's `bin/` comes before the copy next to the main process on
 * purpose. `npm run build` copies the binary into `dist/main/`, and that copy
 * goes stale the moment the Go code changes. In development the fresh build
 * must win. In a packaged app the repository path does not exist, so the
 * bundled copy is used as before.
 *
 * The order itself lives in `sidecar-paths.ts`, which does not import
 * Electron, so that it can be tested directly.
 */

import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

import { app } from 'electron';

import { sidecarCandidates, type SidecarLocation } from './sidecar-paths';

export type { SidecarLocation } from './sidecar-paths';
export { sidecarCandidates } from './sidecar-paths';

function isFile(p: string): boolean {
  try {
    return fs.statSync(p).isFile();
  } catch {
    return false;
  }
}

export function findSidecar(): SidecarLocation | null {
  let appPath = '';
  try {
    appPath = app.getAppPath();
  } catch {
    /* app.getAppPath() can throw in odd launch contexts */
  }

  const candidates = sidecarCandidates(
    appPath,
    process.resourcesPath,
    __dirname,
    process.env.AISAI_SERVER_BIN,
  );

  for (const candidate of candidates) {
    if (isFile(candidate.path)) return candidate;
  }
  return null;
}

/** Default Unix socket path for the sidecar (matches docs/API.md). */
export function defaultSocketPath(): string {
  const uid = typeof process.getuid === 'function' ? process.getuid() : 0;
  return path.join(os.tmpdir(), `aisai-${uid}.sock`);
}
