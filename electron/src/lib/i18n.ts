/**
 * Localization: every piece of text the interface shows comes from a catalog
 * (src/locales), looked up with t().
 *
 * English is the source. Its catalog (locales/en.ts) defines the keys, and
 * the type MessageKey is made from them, so a typo in a key is a compile
 * error. The other catalogs are partial on purpose: a key that a language
 * has not translated yet falls back to the English text, so the interface is
 * never blank or showing a raw key.
 *
 * A message may hold {placeholders}, which t() fills from its second
 * argument: t('findings.count', { n: 3 }). Counts use plural keys: a message
 * written as "findings.count.one" and "findings.count.other" is chosen with
 * tn('findings.count', 3), by the plural rules of the language
 * (https://developer.mozilla.org/docs/Web/JavaScript/Reference/Global_Objects/Intl/PluralRules).
 * English and Spanish have two forms; Japanese has one ("other"); Russian and
 * Arabic would need more, and the catalog format already allows them.
 *
 * The language is a preference: "system" follows the operating system's
 * language list, or a specific language can be chosen in Settings. It is kept
 * in localStorage. Changing it makes the app rebuild its views (main.ts), so
 * no text keeps the old language.
 */

import { create } from 'zustand';

import { catalogs as CATALOGS, type Catalog, type MessageKey } from './messages';

export type { Catalog, MessageKey };

/** The keys of plural messages, without their ".one" / ".other" suffix. */
export type PluralKey = {
  [K in MessageKey]: K extends `${infer Base}.other` ? Base : never;
}[MessageKey];

export type Params = Record<string, string | number>;

/** The languages the interface is available in, with their own names. */
export const LOCALES = [
  { id: 'en', name: 'English' },
  { id: 'es', name: 'Español' },
  { id: 'fr', name: 'Français' },
  { id: 'de', name: 'Deutsch' },
  { id: 'ja', name: '日本語' },
] as const;

export type Locale = (typeof LOCALES)[number]['id'];
export type LocalePreference = Locale | 'system';

/** Picks the first language of a preference list that the app speaks. */
export function resolveLocale(languages: readonly string[]): Locale {
  for (const tag of languages) {
    const lower = tag.toLowerCase();
    const exact = LOCALES.find((l) => l.id.toLowerCase() === lower);
    if (exact) return exact.id;
    // "es-MX" and "es-419" are Spanish; "de-AT" is German.
    const base = lower.split('-')[0];
    const byBase = LOCALES.find((l) => l.id.toLowerCase() === base);
    if (byBase) return byBase.id;
  }
  return 'en';
}

const STORAGE_KEY = 'aisai.locale';

function systemLanguages(): readonly string[] {
  try {
    return navigator.languages?.length ? navigator.languages : [navigator.language];
  } catch {
    return [];
  }
}

function loadPreference(): LocalePreference {
  try {
    const saved = localStorage.getItem(STORAGE_KEY);
    if (saved === 'system' || LOCALES.some((l) => l.id === saved)) return saved as LocalePreference;
  } catch {
    /* no storage: follow the system */
  }
  return 'system';
}

/** The language in effect for a preference. */
export function localeFor(preference: LocalePreference, languages: readonly string[] = systemLanguages()): Locale {
  return preference === 'system' ? resolveLocale(languages) : preference;
}

export interface LocaleState {
  preference: LocalePreference;
  locale: Locale;
  setPreference: (preference: LocalePreference) => void;
}

const initialPreference = loadPreference();
let current: Locale = localeFor(initialPreference);

export const useLocaleStore = create<LocaleState>((set) => ({
  preference: initialPreference,
  locale: current,
  setPreference: (preference) => {
    try {
      localStorage.setItem(STORAGE_KEY, preference);
    } catch {
      /* the choice lasts for this session */
    }
    current = localeFor(preference);
    applyDocumentLanguage();
    set({ preference, locale: current });
  },
}));

/** Tells the browser the page language, which matters for hyphenation, fonts and screen readers. */
function applyDocumentLanguage(): void {
  try {
    document.documentElement.lang = current;
  } catch {
    /* not in a browser (tests) */
  }
}
applyDocumentLanguage();

/** The language in effect now. */
export function currentLocale(): Locale {
  return current;
}

/** Fills {placeholders}. A placeholder with no value is left as written, which shows the gap. */
export function format(template: string, params?: Params): string {
  if (!params) return template;
  return template.replace(/\{(\w+)\}/g, (whole, name: string) => {
    if (!(name in params)) return whole;
    const value = params[name];
    return typeof value === 'number' ? new Intl.NumberFormat(current).format(value) : value;
  });
}

function lookup(key: string, locale: Locale): string | undefined {
  return (CATALOGS[locale] as Record<string, string | undefined>)[key];
}

/** The text for a key in the current language, with its placeholders filled. */
export function t(key: MessageKey, params?: Params): string {
  const text = lookup(key, current) ?? lookup(key, 'en') ?? key;
  return format(text, params);
}

/**
 * The text of a plural message for a count: tn('findings.count', 3) reads
 * "findings.count.other" in English. The count is available as {n}.
 */
export function tn(base: PluralKey, count: number, params?: Params): string {
  const rule = new Intl.PluralRules(current).select(count);
  const withForm = (form: string): string | undefined =>
    lookup(`${base}.${form}`, current) ?? lookup(`${base}.${form}`, 'en');
  const text = withForm(rule) ?? withForm('other') ?? base;
  return format(text, { n: count, ...params });
}

/** The placeholders a message uses, for checking that a translation keeps them. */
export function placeholdersOf(text: string): string[] {
  return Array.from(new Set(Array.from(text.matchAll(/\{(\w+)\}/g), (m) => m[1]))).sort();
}
