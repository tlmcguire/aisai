/**
 * The Keyboard section of Settings: every shortcut, and a way to change it.
 *
 * Click a shortcut's keys, then press the new combination. Esc cancels and
 * Backspace removes the shortcut. A combination that another action already
 * uses, that the system keeps (⌘Q, ⌘W…) or that would type a character is
 * refused with the reason, and recording goes on until something usable is
 * pressed. Bindings are kept by lib/keybindings.ts and apply at once.
 */

import type { AppController } from '../app';
import { h } from '../lib/dom';
import { isPlainTypingKey, keysFor, normalizeSpec, specFromEvent, useKeybindingStore } from '../lib/keybindings';
import { displayKeys, isMac, type Shortcut } from '../lib/shortcuts';
import { confirmDialog } from './confirm';
import { appShortcuts, keyCaps } from './shortcuts';

/** Combinations the system or the native menus keep for themselves. */
const RESERVED: Record<string, string> = {
  'Mod+Q': 'quits the app',
  'Mod+W': 'closes the window',
  'Mod+H': isMac() ? 'hides the app' : '',
  'Mod+M': 'minimises the window',
  'Mod+X': 'cuts',
  'Mod+C': 'copies',
  'Mod+V': 'pastes',
  'Mod+A': 'selects all',
};

export function renderKeyboardSettings(controller: AppController): HTMLElement {
  const definitions = appShortcuts(controller, () => undefined);
  const filter = h('input', { type: 'search', class: 'keys-filter', placeholder: 'Filter shortcuts, e.g. step or save', 'aria-label': 'Filter shortcuts' });
  const message = h('p', { class: 'keys-message', role: 'status', 'aria-live': 'polite' });
  const list = h('div', { class: 'keys-list' });
  const resetAll = h('button', { class: 'tiny', type: 'button' }, 'Reset all to defaults');

  let recording: { id: string; button: HTMLButtonElement } | null = null;

  const current = (s: Shortcut): string => (s.fixed ? s.keys : keysFor(s.id, s.keys));

  /** Who already uses a combination, other than the action being changed. */
  const conflict = (spec: string, except: string): Shortcut | undefined =>
    definitions.find((s) => s.id !== except && current(s) && normalizeSpec(current(s)) === spec);

  const stopRecording = (): void => {
    document.body.classList.remove('is-recording-keys');
    window.removeEventListener('keydown', onRecordKey, true);
    recording = null;
  };

  const onRecordKey = (event: KeyboardEvent): void => {
    if (!recording) return;
    // Nothing else may see the keys while one is being recorded.
    event.preventDefault();
    event.stopPropagation();
    const { id } = recording;
    const shortcut = definitions.find((s) => s.id === id);
    if (!shortcut) return;
    if (event.key === 'Escape' && !event.metaKey && !event.ctrlKey && !event.altKey && !event.shiftKey) {
      message.textContent = 'Not changed.';
      stopRecording();
      render();
      return;
    }
    if ((event.key === 'Backspace' || event.key === 'Delete') && !event.metaKey && !event.ctrlKey && !event.altKey) {
      useKeybindingStore.getState().set(id, '');
      message.textContent = `“${shortcut.label}” has no shortcut now.`;
      stopRecording();
      render();
      return;
    }
    const spec = specFromEvent(event);
    if (!spec) return; // only modifiers so far
    const reserved = RESERVED[spec];
    if (reserved) {
      message.textContent = `That combination ${reserved}, so it cannot be used. Press other keys, or Esc to cancel.`;
      return;
    }
    if (isPlainTypingKey(spec)) {
      message.textContent = `A key without ${isMac() ? '⌘, ⌃ or ⌥' : 'Ctrl or Alt'} would type instead. Add a modifier, or use a function key.`;
      return;
    }
    const taken = conflict(spec, id);
    if (taken) {
      message.textContent = `Already used by “${taken.label}”${taken.fixed ? ', which cannot be changed' : '; change that one first'}. Press other keys, or Esc to cancel.`;
      return;
    }
    useKeybindingStore.getState().set(id, spec);
    message.textContent = `“${shortcut.label}” is now ${displayKeys(spec).join(isMac() ? '' : '+')}.`;
    stopRecording();
    render();
  };

  const startRecording = (id: string, button: HTMLButtonElement): void => {
    if (recording) stopRecording();
    recording = { id, button };
    document.body.classList.add('is-recording-keys');
    button.classList.add('is-recording');
    button.replaceChildren(h('span', { text: 'Press keys…' }));
    message.textContent = 'Press the new combination. Esc cancels, Backspace removes the shortcut.';
    window.addEventListener('keydown', onRecordKey, true);
  };

  const render = (): void => {
    const query = filter.value.trim().toLowerCase();
    const overrides = useKeybindingStore.getState().overrides;
    const groups = new Map<string, Shortcut[]>();
    for (const s of definitions) {
      if (query && !`${s.label} ${s.group} ${current(s)}`.toLowerCase().includes(query)) continue;
      groups.set(s.group, [...(groups.get(s.group) ?? []), s]);
    }
    resetAll.disabled = Object.keys(overrides).length === 0;
    list.replaceChildren(
      ...(groups.size
        ? Array.from(groups, ([group, items]) =>
            h('section', { class: 'keys-group' },
              h('h3', { class: 'pref-subtitle', text: group }),
              ...items.map((s) => {
                const changed = Object.prototype.hasOwnProperty.call(overrides, s.id);
                const keysButton = h('button', {
                  class: `keys-button${s.fixed ? ' is-fixed' : ''}${changed ? ' is-changed' : ''}`,
                  type: 'button',
                  disabled: s.fixed || undefined,
                  title: s.fixed ? 'A system shortcut; it cannot be changed' : 'Click, then press the new keys',
                  'aria-label': `${s.label}: ${current(s) || 'not set'}. ${s.fixed ? 'Cannot be changed.' : 'Press to change.'}`,
                }, ...keyCaps(current(s)));
                if (!s.fixed) keysButton.addEventListener('click', () => startRecording(s.id, keysButton));
                const reset = changed
                  ? h('button', {
                      class: 'ghost tiny',
                      type: 'button',
                      title: `Back to ${s.keys || 'no shortcut'}`,
                      onclick: () => {
                        useKeybindingStore.getState().reset(s.id);
                        message.textContent = `“${s.label}” is back to its default.`;
                        render();
                      },
                    }, 'Reset')
                  : h('span');
                return h('div', { class: 'keys-row' }, h('span', { class: 'keys-label', text: s.label }), keysButton, reset);
              }),
            ),
          )
        : [h('p', { class: 'muted', text: 'No shortcut matches.' })]),
    );
  };

  filter.addEventListener('input', render);
  resetAll.addEventListener('click', async () => {
    const ok = await confirmDialog('Every shortcut goes back to its default keys. Your custom ones are removed.', { confirmLabel: 'Reset all', danger: true });
    if (!ok) return;
    useKeybindingStore.getState().resetAll();
    message.textContent = 'Every shortcut is back to its default.';
    render();
  });
  render();

  const section = h(
    'section',
    { class: 'pref-section' },
    h('h2', { class: 'pref-title', text: 'Keyboard' }),
    h('p', { class: 'pref-lede', text: 'Click a shortcut’s keys, then press the combination you want. Esc cancels; Backspace removes the shortcut. Changes apply at once, are shown in the menus, and are remembered on this computer.' }),
    h('div', { class: 'keys-toolbar' }, filter, resetAll),
    message,
    list,
  );
  // Leaving the section mid-recording must not leave the keys captured.
  const observer = new MutationObserver(() => {
    if (!section.isConnected) {
      stopRecording();
      observer.disconnect();
    }
  });
  queueMicrotask(() => {
    if (section.parentElement) observer.observe(section.parentElement, { childList: true });
  });
  return section;
}
