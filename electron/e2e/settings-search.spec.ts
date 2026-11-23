/**
 * The settings search field.
 *
 * The field carries a magnifier on its left and the text on its right.
 * The two are laid out by the stylesheet alone, so the only way to know
 * that they do not sit on top of each other is to measure them in a real
 * window: the renderer puts the icon over the field and gives the field a
 * left padding, and a rule that sets the padding of every search input
 * can undo the second half of that.
 */

import { rmSync } from 'node:fs';
import { join } from 'node:path';

import { _electron as electron, expect, test, type ElectronApplication, type Page } from '@playwright/test';

import { electronDirOf, freshProfile, STUB_RELATIVE_PATH } from './global-setup';

let app: ElectronApplication;
let window: Page;
let profile: string;

test.describe('the settings search field', () => {
  test.beforeAll(async () => {
    const electronDir = electronDirOf(test.info().config);
    profile = freshProfile();
    app = await electron.launch({
      args: [`--user-data-dir=${profile}`, electronDir],
      cwd: electronDir,
      env: {
        ...process.env,
        AISAI_SERVER_BIN: join(electronDir, STUB_RELATIVE_PATH),
        DISABLE_LLM: '1',
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
   * Opens the settings dialog.
   *
   * The dialog is a modal, and it is not certain to be open at the start
   * of a test, so it is opened when it is not.
   */
  const openSettings = async (): Promise<void> => {
    const isOpen = await window.evaluate(() => {
      const dialog = document.querySelector('dialog.prefs');
      return dialog?.hasAttribute('open') ?? false;
    });
    if (!isOpen) {
      await window.click('button[aria-label="Settings"]');
    }
    await window.locator('.pref-search-input').waitFor({ state: 'visible' });
  };

  test('the magnifier does not sit on top of the text', async () => {
    await openSettings();

    const field = await window.evaluate(() => {
      const input = document.querySelector('.pref-search-input') as HTMLInputElement | null;
      const icon = document.querySelector('.pref-search-icon') as HTMLElement | null;
      if (!input || !icon) return null;

      const inputRect = input.getBoundingClientRect();
      const iconRect = icon.getBoundingClientRect();
      const style = getComputedStyle(input);

      // The text of the field starts at the content edge of the input:
      // its border, then its padding.
      const textLeft = inputRect.left + parseFloat(style.borderLeftWidth) + parseFloat(style.paddingLeft);

      return {
        iconRight: iconRect.right,
        iconTop: iconRect.top,
        iconBottom: iconRect.bottom,
        textLeft,
        inputTop: inputRect.top,
        inputBottom: inputRect.bottom,
        placeholder: input.placeholder,
        paddingLeft: parseFloat(style.paddingLeft),
      };
    });

    expect(field).not.toBeNull();
    const measured = field!;

    // The magnifier ends before the text begins.
    expect(measured.textLeft).toBeGreaterThanOrEqual(measured.iconRight);

    // The magnifier is inside the field, not floating above or below it.
    expect(measured.iconTop).toBeGreaterThanOrEqual(measured.inputTop);
    expect(measured.iconBottom).toBeLessThanOrEqual(measured.inputBottom);

    // The field says what it is.
    expect(measured.placeholder).not.toBe('');
    // And the text is held clear of the icon by a padding of its own.
    expect(measured.paddingLeft).toBeGreaterThan(0);
  });

  test('the field finds a setting and clears itself', async () => {
    await openSettings();

    const input = window.locator('.pref-search-input');
    await input.fill('theme');

    // The results are counted, and the first one is offered.
    await expect(window.locator('.pref-search-item').first()).toBeVisible();

    // The clear button empties the field.
    await window.locator('.pref-search-clear').click();
    await expect(input).toHaveValue('');
    await expect(window.locator('.pref-search-item')).toHaveCount(0);
  });
});
