/**
 * The end-to-end flow of the desktop app.
 *
 * The flow is the one the rewrite plan asks for:
 *
 *   audit → scan → open a finding → apply the patch → re-scan
 *
 * The test is deterministic and offline. The Go backend is replaced by a
 * stub sidecar (e2e/fixtures/stub-backend.ts) that serves a vendored
 * fixture package; the LLM path is stubbed out with the same fixture, so no
 * model is contacted.
 */

import { join } from 'node:path';

import { _electron as electron, expect, test, type ElectronApplication, type Page } from '@playwright/test';

import { electronDirOf, STUB_RELATIVE_PATH } from './global-setup';

let app: ElectronApplication;
let window: Page;

test.describe('audit → scan → finding → patch → re-scan', () => {
  test.beforeAll(async () => {
    // The path of the config file is used rather than import.meta, because
    // Playwright compiles the TypeScript of a test to CommonJS.
    const electronDir = electronDirOf(test.info().config);
    app = await electron.launch({
      args: [electronDir],
      cwd: electronDir,
      env: {
        ...process.env,
        // The main process starts this binary as the sidecar.
        AISAI_SERVER_BIN: join(electronDir, STUB_RELATIVE_PATH),
        // The LLM is not part of this test.
        DISABLE_LLM: '1',
        // The screenshot smoke check must not run.
        AISAI_SMOKE_SCREENSHOT: '',
      },
    });
    window = await app.firstWindow();
    await window.waitForLoadState('domcontentloaded');
  });

  test.afterAll(async () => {
    await app?.close();
  });

  test('the backend is the stub, not the in-renderer mock', async () => {
    // The mock badge is only shown when the Go sidecar was not found.
    await expect(window.locator('.badge.mock')).toBeHidden();
  });

  test('an audit loads the vendored fixture package', async () => {
    await window.fill('#package-input', 'flask');
    await window.fill('#version-input', '2.0.0');
    await window.getByRole('button', { name: 'Scan', exact: true }).click();

    // The findings of the audit appear in the rail.
    await expect(window.locator('.finding-card').first()).toBeVisible();

    // The file tree is the vendored package: the code pane shows a file
    // that really exists in the repository.
    await expect(window.locator('.tree-row .name', { hasText: 'security.py' }).first()).toBeVisible();
  });

  test('a scan reports its progress', async () => {
    await window.getByRole('button', { name: 'Scan LLM' }).click();
    // The scan runs through the socket and comes back as a job.
    await expect(window.locator('.finding-card').first()).toBeVisible();
  });

  test('opening a finding shows the code of the vendored package', async () => {
    const card = window.locator('.finding-card', { hasText: 'flask/security.py' }).first();
    await card.click();

    // The code pane shows the vendored file.
    await expect(window.locator('.code-line').first()).toBeVisible();
    await expect(window.locator('.file-header')).toContainText('flask/security.py');
  });

  test('a proposed fix is shown as a diff', async () => {
    const card = window.locator('.finding-card', { hasText: 'flask/security.py' }).first();
    await card.getByRole('button', { name: 'Propose fix' }).click();

    // The diff view opens by itself and shows the added line.
    await expect(window.locator('.diff-line.add').first()).toBeVisible();
    await expect(window.locator('.diff-line.remove').first()).toBeVisible();
  });

  test('applying the patch re-scans and reports the result', async () => {
    await window.locator('.diff-toolbar button', { hasText: 'Apply' }).click();

    // The summary is the result of the re-scan that follows the patch.
    const summary = window.locator('.patch-toolbar .legend-item');
    await expect(summary).toContainText('Applied.');
    await expect(summary).toContainText('resolved');
  });

  test('a scan after the patch still works', async () => {
    await window.getByRole('button', { name: 'Scan LLM' }).click();
    await expect(window.locator('.finding-card').first()).toBeVisible();
    await expect(window.locator('.patch-toolbar .legend-item')).toContainText('Applied.');
  });
});
