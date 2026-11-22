/**
 * The language picker in Settings.
 *
 * The language is a preference rather than a build option: "system" follows
 * the operating system's language list, and a language picked here is
 * remembered on this computer. Changing it makes every message on screen
 * again in the new language (main.ts), without building the interface a
 * second time.
 *
 * The languages are listed under their own names, because that is how people
 * look for their language, not how they would translate it.
 */

import { h } from '../lib/dom';
import { LOCALES, t, useLocaleStore, type LocalePreference } from '../lib/i18n';

/** The label and the picker, ready to drop into a settings form. */
export function renderLanguageSetting(): HTMLElement[] {
  const select = h(
    'select',
    { id: 'pref-language', 'aria-label': t('settings.language') },
    h('option', { value: 'system' }, t('settings.languageSystem')),
    ...LOCALES.map((locale) => h('option', { value: locale.id }, locale.name)),
  );
  select.value = useLocaleStore.getState().preference;
  select.addEventListener('change', () => {
    useLocaleStore.getState().setPreference(select.value as LocalePreference);
  });
  return [h('label', { for: 'pref-language', text: t('settings.language') }), select];
}
