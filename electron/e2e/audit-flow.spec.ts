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

import { rmSync } from 'node:fs';
import { join } from 'node:path';

import { _electron as electron, expect, test, type ElectronApplication, type Page } from '@playwright/test';

import { electronDirOf, freshProfile, STUB_RELATIVE_PATH } from './global-setup';

let app: ElectronApplication;
let window: Page;
let profile: string;

test.describe('audit → scan → finding → patch → re-scan', () => {
  test.beforeAll(async () => {
    // The path of the config file is used rather than import.meta, because
    // Playwright compiles the TypeScript of a test to CommonJS.
    const electronDir = electronDirOf(test.info().config);
    profile = freshProfile();
    app = await electron.launch({
      args: [`--user-data-dir=${profile}`, electronDir],
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
    rmSync(profile, { recursive: true, force: true });
  });

  /**
   * opens the settings dialog.
   *
   * The dialog is a modal, so it stays open between the tests. Opening it
   * again would be harmless, but only when it is not open already.
   */
  const openSettings = async (): Promise<void> => {
    const isOpen = await window.evaluate(() => {
      const dialog = document.querySelector('dialog.prefs');
      return dialog?.hasAttribute('open') ?? false;
    });
    if (!isOpen) {
      await window.click('button[aria-label="Settings"]');
    }
    // Settings opens on the model server; the picker is in its own section.
    await window.locator('.pref-nav-item', { hasText: 'Models' }).click();
    await window.locator('#model-chat').waitFor({ state: 'visible' });
  };

  test('the backend is the stub, not the in-renderer mock', async () => {
    // The mock badge is only shown when the Go sidecar was not found.
    await expect(window.locator('.badge.mock')).toBeHidden();
  });

  test('an audit loads the vendored fixture package', async () => {
    await window.fill('#package-input', 'flask');
    // The version is a dropdown of the versions the index published, not
    // a free text field.
    await window.selectOption('#version-input', '2.0.0');
    await window.getByRole('button', { name: 'Pull', exact: true }).click();
    // Pulling only downloads; the scan is its own step.
    await window.getByRole('button', { name: 'Static scan' }).click();

    // The findings of the audit appear in the rail.
    await expect(window.locator('.finding-card').first()).toBeVisible();

    // The file tree is the vendored package: the code pane shows a file
    // that really exists in the repository.
    await expect(window.locator('.tree-row .name', { hasText: 'security.py' }).first()).toBeVisible();
  });

  test('a scan reports its progress', async () => {
    await window.getByRole('button', { name: 'LLM scan' }).click();
    // The scan runs through the socket and comes back as a job.
    await expect(window.locator('.finding-card').first()).toBeVisible();
  });

  test('opening a finding shows the code of the vendored package', async () => {
    const card = window.locator('.finding-card', { hasText: 'flask/security.py' }).first();
    await card.click();

    // The code pane shows the vendored file.
    await expect(window.locator('.cm-line').first()).toBeVisible();
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
    await window.getByRole('button', { name: 'LLM scan' }).click();
    await expect(window.locator('.finding-card').first()).toBeVisible();
    await expect(window.locator('.patch-toolbar .legend-item')).toContainText('Applied.');
  });

  test('the model picker lists the installed models', async () => {
    await openSettings();
    const chat = window.locator('#model-chat');
    await expect(chat).toBeVisible();

    // Every installed model is offered, with the size that decides
    // whether it fits on the machine.
    const options = await chat.locator('option').allTextContents();
    expect(options.length).toBeGreaterThan(0);
    expect(options.join(' ')).toContain('qwen3.5:4b');
    expect(options.join(' ')).toMatch(/GB|MB/);

    // The keep-alive window is shown, and it is the three minute default.
    await expect(window.locator('#model-keep-alive')).toHaveValue('3m');

    // The table of installed models is rendered.
    await expect(window.locator('.model-table tbody tr').first()).toBeVisible();
  });

  test('a model can be picked and applied', async () => {
    await openSettings();
    const chat = window.locator('#model-chat');
    const before = await chat.inputValue();
    const options = await chat.locator('option').allTextContents();
    const other = options.map((text, index) => ({ text, index })).find((o) => !o.text.startsWith(before));
    expect(other).toBeTruthy();

    await chat.selectOption({ index: other!.index });
    // The diff view has an "Apply" button too, so the click is scoped to
    // the settings dialog.
    await window.locator('dialog.prefs').getByRole('button', { name: 'Apply' }).click();
    await expect(window.locator('.model-status')).toContainText('Applied.');
  });

  test('the memory of the models can be released', async () => {
    await openSettings();
    await window.getByRole('button', { name: 'Release memory' }).click();
    await expect(window.locator('.model-status')).toContainText('Released');
  });

  test('the package picker suggests packages and lists their versions', async () => {
    // The settings dialog is a modal and stays open between the tests.
    const dialog = window.locator('dialog.prefs');
    if (await dialog.evaluate((el) => el.hasAttribute('open'))) {
      await dialog.getByRole('button', { name: 'Close' }).click();
    }

    const input = window.locator('#package-input');
    await input.click();
    await input.fill('');

    // The suggestions are the packages that were audited before and a
    // short list of the packages people install.
    const options = window.locator('.picker-option .picker-option-name');
    await expect(options.first()).toBeVisible();
    expect((await options.allTextContents()).join(' ')).toContain('flask');

    // Typing filters them.
    await input.fill('fla');
    await expect(options).toHaveCount(1);
    await expect(options.first()).toHaveText('flask');

    // Picking the suggestion resolves the package and fills the version
    // dropdown with the versions the index published.
    await options.first().click();
    await expect(window.locator('#version-input option', { hasText: '3.1.0' })).toHaveCount(1);
    await expect(window.locator('.picker-status')).toContainText('flask');
  });

  test('the downloaded sources can be deleted after the inspection', async () => {
    const button = window.getByRole('button', { name: 'Delete sources' });
    await expect(button).toBeVisible();

    // The first click arms the button, the second one deletes.
    await button.click();
    await expect(window.getByRole('button', { name: 'Confirm delete' })).toBeVisible();
    await window.getByRole('button', { name: 'Confirm delete' }).click();

    await expect(window.locator('.pane-header .badge', { hasText: 'sources deleted' })).toBeVisible();
  });

  test('the confidence meter of a finding is filled', async () => {
    // The fill is a span, so it needs an explicit block display; without
    // it the meter renders as an empty box.
    const width = await window
      .locator('.confidence-fill')
      .first()
      .evaluate((el) => el.getBoundingClientRect().width);
    expect(width).toBeGreaterThan(0);
  });
});
