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

  test('dragging the handle of a collapsed chat opens and resizes it', async () => {
    await window.click('.chat-header button[data-role="chat-toggle"]');
    await expect(window.locator('.chat-panel.collapsed')).toHaveCount(1);
    // The panel glides down; the handle is pressed where it ends up.
    await window.waitForTimeout(600);
    await dragFromDots('.chat-panel', '.chat-header .grab-handle', -90);
    await expect(window.locator('.chat-panel.collapsed')).toHaveCount(0);
    expect(await height('.chat-panel')).toBeGreaterThan(150);
  });

  test('dragging the dot grid of a pane moves the pane', async () => {
    const order = (): Promise<string[]> =>
      window.evaluate(() => [...document.querySelectorAll('.pane')].map((el) => el.className.split(' ').find((c) => c.startsWith('pane-') && c !== 'pane-grip')!));
    const before = await order();
    const grip = (await window.locator('.pane-graph .pane-grip').boundingBox())!;
    const rail = (await window.locator('.pane-rail').boundingBox())!;
    await window.mouse.move(grip.x + grip.width / 2, grip.y + grip.height / 2);
    await window.mouse.down();
    await window.mouse.move(rail.x + rail.width * 0.8, rail.y + 100, { steps: 10 });
    await window.mouse.up();
    await window.waitForTimeout(300);
    const after = await order();
    expect(after).not.toEqual(before);
    expect(after.indexOf('pane-graph')).toBeGreaterThan(after.indexOf('pane-rail'));
    // Put the pane back, as the order is saved between runs.
    await window.evaluate(() => localStorage.clear());
  });
});
