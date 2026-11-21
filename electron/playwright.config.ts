/**
 * The Playwright configuration of the AISAI desktop app.
 *
 * The test drives the real Electron application: the main process, the
 * preload bridge, the IPC path allowlist and the renderer all run for real.
 * The Go backend is replaced by a stub sidecar that serves a vendored
 * fixture package, so the test never touches the network and never needs a
 * model. See e2e/fixtures/stub-backend.ts.
 */

import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './e2e',
  testMatch: '**/*.spec.ts',
  // The Electron app is a single process with a single window; the tests
  // run one after another so that they cannot fight over the socket.
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  timeout: 90_000,
  expect: {
    timeout: 20_000,
  },
  reporter: process.env.CI
    ? [['list'], ['html', { open: 'never', outputFolder: 'out/playwright-report' }]]
    : [['list']],
  // The traces, the screenshots and the report are build artifacts: they
  // live in out/, which is in .gitignore.
  outputDir: 'out/test-results',
  globalSetup: './e2e/global-setup.ts',
  use: {
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
});
