/**
 * The grab handles of the terminal and the chat.
 *
 * A press on the pill and a drag has to resize the panel; only a press and
 * release on an arrow steps it.
 */

import { join } from 'node:path';

import { _electron as electron, expect, test, type ElectronApplication, type Page } from '@playwright/test';

import { electronDirOf, STUB_RELATIVE_PATH } from './global-setup';

let app: ElectronApplication;
let window: Page;

test.describe('the grab handles', () => {
  test.beforeAll(async () => {
    const electronDir = electronDirOf(test.info().config);
    app = await electron.launch({
      args: [electronDir],
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
  });

  const height = (selector: string): Promise<number> =>
    window.evaluate((s) => document.querySelector(s)!.getBoundingClientRect().height, selector);

  const dragFromDots = async (_panel: string, handle: string, dy: number, part = '.grab-dots'): Promise<void> => {
    const box = await window.locator(`${handle} ${part}`).first().boundingBox();
    expect(box).not.toBeNull();
    const x = box!.x + box!.width / 2;
    const y = box!.y + box!.height / 2;
    await window.mouse.move(x, y);
    await window.mouse.down();
    await window.mouse.move(x, y + dy / 2, { steps: 6 });
    await window.mouse.move(x, y + dy, { steps: 6 });
    await window.mouse.up();
    await window.waitForTimeout(400);
  };

  test('dragging the terminal handle resizes the terminal', async () => {
    await window.click('button[aria-label="Terminal"]');
    await window.locator('.terminal-panel').waitFor({ state: 'visible' });
    const before = await height('.terminal-panel');
    await dragFromDots('.terminal-panel', '.resizer:has(.grab-handle)', 100);
    const after = await height('.terminal-panel');
    expect(Math.abs(after - before + 100)).toBeLessThan(8);
  });

  test('dragging from an arrow of the terminal handle resizes the terminal', async () => {
    const before = await height('.terminal-panel');
    await dragFromDots('.terminal-panel', '.resizer:has(.grab-handle)', -100, '.grab-arrow');
    const after = await height('.terminal-panel');
    expect(Math.abs(after - before - 100)).toBeLessThan(8);
  });

  test('dragging the chat handle resizes the chat', async () => {
    const before = await height('.chat-panel');
    await dragFromDots('.chat-panel', '.chat-header .grab-handle', -60);
    const after = await height('.chat-panel');
    expect(Math.abs(after - before - 60)).toBeLessThan(8);
  });
});
