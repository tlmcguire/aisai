// @vitest-environment jsdom
/**
 * Tests that need a DOM: the pieces whose bugs showed up on screen. The rest
 * of the renderer's tests run in plain Node (units.test.ts).
 */

import { beforeEach, describe, expect, it } from 'vitest';

import { confirmDialog } from '../src/views/confirm';
import { createErrorScreen } from '../src/views/errorScreen';
import { useChatStore } from '../src/state/chat';

beforeEach(() => {
  document.body.replaceChildren();
  // jsdom implements <dialog> without the modal parts; a stand-in is enough
  // to test what the app does around them.
  const proto = HTMLDialogElement.prototype as unknown as Record<string, unknown>;
  proto.showModal = function (this: HTMLDialogElement) {
    this.setAttribute('open', '');
  };
  proto.close = function (this: HTMLDialogElement) {
    this.removeAttribute('open');
  };
});

describe('confirmDialog', () => {
  it('resolves true on the confirm button and false on cancel', async () => {
    const yes = confirmDialog('Overwrite it?', { confirmLabel: 'Overwrite' });
    const dialog = document.querySelector('dialog') as HTMLDialogElement;
    expect(dialog.open).toBe(true);
    expect(dialog.textContent).toContain('Overwrite it?');
    (Array.from(dialog.querySelectorAll('button')).find((b) => b.textContent === 'Overwrite') as HTMLElement).click();
    expect(await yes).toBe(true);
    expect(document.querySelector('dialog')).toBeNull();

    const no = confirmDialog('Delete it?', { confirmLabel: 'Delete', danger: true });
    (Array.from(document.querySelectorAll('button')).find((b) => b.textContent === 'Cancel') as HTMLElement).click();
    expect(await no).toBe(false);
  });

  it('treats Escape as cancelling and starts on the safe choice', async () => {
    const answer = confirmDialog('Reset everything?', { confirmLabel: 'Reset', danger: true });
    const dialog = document.querySelector('dialog') as HTMLDialogElement;
    expect(document.activeElement?.textContent).toBe('Cancel');
    dialog.dispatchEvent(new Event('cancel', { cancelable: true }));
    expect(await answer).toBe(false);
  });

  it('names itself for screen readers', () => {
    void confirmDialog('Sure?', { confirmLabel: 'Yes', title: 'A title' });
    const dialog = document.querySelector('dialog') as HTMLDialogElement;
    expect(dialog.getAttribute('aria-labelledby')).toBe('confirm-title');
    expect(document.getElementById('confirm-title')?.textContent).toBe('A title');
    expect(document.getElementById('confirm-message')).not.toBeNull();
  });
});

describe('the error screen', () => {
  it('does not un-hide an overlay that was meant to stay hidden', () => {
    let notify: (status: { state: string; message: string }) => void = () => undefined;
    (window as unknown as { aisai: unknown }).aisai = {
      onBackendStatus: (cb: typeof notify) => {
        notify = cb;
        return () => undefined;
      },
    };
    const root = document.createElement('div');
    document.body.append(root);
    const shell = document.createElement('div');
    const tourLayer = document.createElement('div');
    tourLayer.hidden = true; // shown only while a tour runs
    root.append(shell, tourLayer);
    const screen = createErrorScreen(root, [shell, tourLayer]);

    notify({ state: 'error', message: 'no sidecar' });
    expect(screen.element.hidden).toBe(false);
    expect(screen.element.getAttribute('role') ?? screen.element.querySelector('[role=alert]')?.getAttribute('role')).toBe('alert');
    expect(shell.hidden).toBe(true);

    notify({ state: 'ready', message: '' });
    expect(screen.element.hidden).toBe(true);
    expect(shell.hidden).toBe(false);
    expect(tourLayer.hidden).toBe(true); // the bug: this used to become visible
    screen.dispose();
  });
});

describe('a command approval that timed out', () => {
  it('stays "expired" when the plain denied result follows', () => {
    const store = useChatStore.getState();
    store.newThread();
    store.pushUser('q');
    const jobId = 'job1';
    store.startAssistant(jobId);
    store.upsertStep(jobId, { id: 's1', name: 'run_command', args: { command: 'make' }, status: 'waiting', approval: 'pending' });
    store.upsertStep(jobId, { id: 's1', name: 'run_command', args: {}, status: 'error', approval: 'expired' });
    store.upsertStep(jobId, { id: 's1', name: 'run_command', args: {}, status: 'error', approval: 'denied' });
    const steps = useChatStore.getState().threads.flatMap((t) => t.messages).flatMap((m) => m.steps ?? []);
    expect(steps.find((s) => s.id === 's1')?.approval).toBe('expired');
  });
});
