/**
 * The message catalogs, and a plain lookup for them.
 *
 * The renderer reads the catalogs through the store in i18n.ts. The Electron
 * main process reads them here: it builds the application menu, which is not
 * part of the renderer, and it has no store of its own.
 */

import { de } from '../locales/de';
import { en } from '../locales/en';
import { es } from '../locales/es';
import { fr } from '../locales/fr';
import { ja } from '../locales/ja';

/** A key of the English catalog: every key the interface can ask for. */
export type MessageKey = keyof typeof en;

/** A catalog of translations: a subset of the English keys is allowed. */
export type Catalog = Partial<Record<MessageKey, string>>;

/** Every catalog, by language. */
export const catalogs: Record<string, Catalog> = { en, es, fr, de, ja };

/**
 * The text of a message in a language.
 *
 * A language that has not translated the message falls back to English, so
 * the text is always there; a key that English does not have either shows the
 * key, which is a bug worth seeing rather than an empty label.
 */
export function message(key: MessageKey, locale: string): string {
  return catalogs[locale]?.[key] ?? catalogs.en[key] ?? key;
}
