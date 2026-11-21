#!/usr/bin/env node
/**
 * Development launcher: waits for the Vite dev server, then starts Electron
 * with VITE_DEV_SERVER_URL pointing at it.
 */

import { spawn } from 'node:child_process';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import electronPath from 'electron';

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(here, '..');
const url = process.env.VITE_DEV_SERVER_URL ?? 'http://localhost:5273';

async function waitForServer(target, timeoutMs = 30000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      const response = await fetch(target);
      if (response.ok || response.status === 404) return true;
    } catch {
      /* not up yet */
    }
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  return false;
}

if (!(await waitForServer(url))) {
  console.error(`the Vite dev server did not come up at ${url}`);
  process.exit(1);
}

const child = spawn(electronPath, ['.'], {
  cwd: root,
  stdio: 'inherit',
  env: { ...process.env, VITE_DEV_SERVER_URL: url },
});

child.on('close', (code) => process.exit(code ?? 0));
