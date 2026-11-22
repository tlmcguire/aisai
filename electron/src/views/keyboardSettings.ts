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
import { t, type MessageKey } from '../lib/i18n';
import { isPlainTypingKey, keysFor, normalizeSpec, specFromEvent, useKeybindingStore } from '../lib/keybindings';
import { displayKeys, isMac, type Shortcut } from '../lib/shortcuts';
import { confirmDialog } from './confirm';
import { appShortcuts, keyCaps } from './shortcuts';

/**
 * Combinations the system or the native menus keep for themselves.
 *
 * The value is the message key that says what the combination is for, so
 * the reason can be written in the user's language.
 */
const RESERVED: Record<string, MessageKey> = {
  'Mod+Q': 'settings.keysReservedQuitsApp',
  'Mod+W': 'settings.keysReservedClosesWindow',
  'Mod+H': 'settings.keysReservedHidesApp',
  'Mod+M': 'settings.keysReservedMinimisesWindow',
  'Mod+X': 'settings.keysReservedCuts',
  'Mod+C': 'settings.keysReservedCopies',
  'Mod+V': 'settings.keysReservedPastes',
  'Mod+A': 'settings.keysReservedSelectsAll',
};

export function renderKeyboardSettings(controller: AppController): HTMLElement {
  const definitions = appShortcuts(controller, () => undefined);
  const filter = h('input', { type: 'search', class: 'keys-filter', placeholder: t('settings.keysFilterPlaceholder'), 'aria-label': t('settings.keysFilterAria') });
  const message = h('p', { class: 'keys-message', role: 'status', 'aria-live': 'polite' });
  const list = h('div', { class: 'keys-list' });
  const resetAll = h('button', { class: 'tiny', type: 'button' }, t('settings.keysResetAll'));

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
      message.textContent = t('settings.keysNotChanged');
      stopRecording();
      render();
      return;
    }
    if ((event.key === 'Backspace' || event.key === 'Delete') && !event.metaKey && !event.ctrlKey && !event.altKey) {
      useKeybindingStore.getState().set(id, '');
      message.textContent = t('settings.keysShortcutRemoved', { name: shortcut.label });
      stopRecording();
      render();
      return;
    }
    const spec = specFromEvent(event);
    if (!spec) return; // only modifiers so far
    const reserved = RESERVED[spec];
    // The system keeps Mod+H on macOS only; elsewhere it is free.
    if (reserved && !(spec === 'Mod+H' && !isMac())) {
      message.textContent = t('settings.keysCombinationReserved', { reason: t(reserved) });
      return;
    }
    if (isPlainTypingKey(spec)) {
      message.textContent = isMac() ? t('settings.keysTypingWarningMac') : t('settings.keysTypingWarningOther');
      return;
    }
    const taken = conflict(spec, id);
    if (taken) {
      message.textContent = taken.fixed
        ? t('settings.keysTakenFixed', { name: taken.label })
        : t('settings.keysTaken', { name: taken.label });
      return;
    }
    useKeybindingStore.getState().set(id, spec);
    message.textContent = t('settings.keysNow', { name: shortcut.label, keys: displayKeys(spec).join(isMac() ? '' : '+') });
    stopRecording();
    render();
  };

  const startRecording = (id: string, button: HTMLButtonElement): void => {
    if (recording) stopRecording();
    recording = { id, button };
    document.body.classList.add('is-recording-keys');
    button.classList.add('is-recording');
    button.replaceChildren(h('span', { text: t('settings.keysPressKeys') }));
    message.textContent = t('settings.keysPressNew');
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
                  title: s.fixed ? t('settings.keysFixedTitle') : t('settings.keysChangeTitle'),
                  'aria-label': t('settings.keysAriaLabel', {
                    name: s.label,
                    keys: current(s) || t('settings.keysNotSet'),
                    state: s.fixed ? t('settings.keysCannotBeChanged') : t('settings.keysPressToChange'),
                  }),
                }, ...keyCaps(current(s)));
                if (!s.fixed) keysButton.addEventListener('click', () => startRecording(s.id, keysButton));
                const reset = changed
                  ? h('button', {
                      class: 'ghost tiny',
                      type: 'button',
                      title: t('settings.keysBackTo', { keys: s.keys || t('settings.keysNoShortcut') }),
                      onclick: () => {
                        useKeybindingStore.getState().reset(s.id);
                        message.textContent = t('settings.keysRestored', { name: s.label });
                        render();
                      },
                    }, t('settings.keysReset'))
                  : h('span');
                return h('div', { class: 'keys-row' }, h('span', { class: 'keys-label', text: s.label }), keysButton, reset);
              }),
            ),
          )
        : [h('p', { class: 'muted', text: t('settings.keysNoMatches') })]),
    );
  };

  filter.addEventListener('input', render);
  resetAll.addEventListener('click', async () => {
    const ok = await confirmDialog(t('settings.keysResetAllConfirm'), { confirmLabel: t('settings.keysResetAllLabel'), danger: true });
    if (!ok) return;
    useKeybindingStore.getState().resetAll();
    message.textContent = t('settings.keysAllReset');
    render();
  });
  render();

  const section = h(
    'section',
    { class: 'pref-section' },
    h('h2', { class: 'pref-title', text: t('settings.keyboardTitle') }),
    h('p', { class: 'pref-lede', text: t('settings.keyboardLede') }),
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
