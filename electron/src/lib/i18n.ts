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
 * in localStorage.
 *
 * A message is never glued to other text. Languages put their words in a
 * different order, so a sentence that is put together in code cannot be put
 * back together in another language. Every piece of text therefore keeps the
 * key it came from, the interface remembers where each of them is shown, and
 * a new language only has to fill them all in again.
 */

import { create } from 'zustand';

import { catalogs as CATALOGS, type Catalog, type MessageKey } from './messages';
import { typewrite } from './typewriter';

export type { Catalog, MessageKey };

/** The keys of plural messages, without their ".one" / ".other" suffix. */
export type PluralKey = {
  [K in MessageKey]: K extends `${infer Base}.other` ? Base : never;
}[MessageKey];

export type Params = Record<string, string | number>;

/**
 * One piece of a message: fixed text, a key with its placeholders, or a
 * plural key with the count that chooses its form.
 *
 * A message is kept as its pieces rather than as finished text, because the
 * text has to be made again when the language changes.
 */
type Segment =
  | { kind: 'text'; text: string }
  | { kind: 'message'; key: string; params?: Params }
  | { kind: 'plural'; base: string; count: number; params?: Params };

/**
 * A translated string that remembers where it came from.
 *
 * It behaves like a string everywhere it is used — set as the text of an
 * element, compared, concatenated — but it also carries the pieces it was
 * made of. That is what lets the interface be put in another language
 * without being built again: every element that shows a message is
 * registered, and a new language only has to fill it in again.
 */
export class Translated extends String {
  /** The key of the message, when it is a single one. */
  readonly key?: string;
  /** The placeholders the message was filled with. */
  readonly params?: Params;
  /** The pieces the message is made of. */
  readonly segments?: readonly Segment[];

  constructor(text: string, init: { key?: string; params?: Params; segments?: readonly Segment[] }) {
    super(text);
    this.key = init.key;
    this.params = init.params;
    this.segments = init.segments;
  }
}

/** Whether a value is a translated string that remembers its key. */
export function isTranslated(value: unknown): value is Translated {
  return value instanceof Translated;
}

/** What a registered element shows, and where it comes from. */
interface Entry {
  /** The pieces the text is made of. */
  segments: readonly Segment[];
}

// The elements that show a message, and the messages they show. The weak
// references let an element that is gone be collected: the interface is
// built and thrown away often, and it must not pile up.
const entries = new WeakMap<object, Map<string, Entry>>();
const refs = new WeakMap<object, WeakRef<object>>();
const tracked = new Set<WeakRef<object>>();

function track(node: object): void {
  if (refs.has(node)) return;
  const ref = new WeakRef(node);
  refs.set(node, ref);
  tracked.add(ref);
}

function entryFor(node: object): Map<string, Entry> {
  let map = entries.get(node);
  if (!map) {
    map = new Map();
    entries.set(node, map);
  }
  return map;
}

/** Remembers where the text of an element or of a text node comes from. */
export function registerText(node: object, message: Translated): void {
  entryFor(node).set('', { segments: message.segments ?? [] });
  track(node);
}

/** Remembers where an attribute of an element comes from. */
export function registerAttribute(element: Element, name: string, message: Translated): void {
  entryFor(element).set(name, { segments: message.segments ?? [] });
  track(element);
}

/** Forgets where an attribute came from: it was given a plain text after all. */
function forgetAttribute(element: Element, name: string): void {
  entries.get(element)?.delete(name);
}

/* ---------------------------------------------------------------------------
 * The DOM is patched in a few places, so that a message that is set in any
 * ordinary way is remembered. Everything else goes through the helpers.
 * ------------------------------------------------------------------------ */

/** The original setters, kept aside so that the interface can fill itself in. */
let nativeTextSet: ((this: Node, value: unknown) => void) | null = null;
let nativeSetAttribute: ((this: Element, name: string, value: string) => void) | null = null;

/** A message that is set as the text of an element is remembered as well. */
function patchTextContent(): void {
  const descriptor = Object.getOwnPropertyDescriptor(Node.prototype, 'textContent');
  if (!descriptor?.get || !descriptor.set) return;
  const get = descriptor.get;
  const set = descriptor.set;
  nativeTextSet = set;
  Object.defineProperty(Node.prototype, 'textContent', {
    configurable: true,
    enumerable: descriptor.enumerable,
    get(this: Node): unknown {
      return get.call(this);
    },
    set(this: Node, value: unknown): void {
      if (value instanceof Translated) registerText(this, value);
      else entries.get(this)?.delete('');
      set.call(this, value);
    },
  });
}

/**
 * The children of an element are often set in one go. A message among them
 * has to be remembered as well, or it would keep the language it was made
 * in.
 */
function patchReplaceChildren(prototype: object): void {
  const descriptor = Object.getOwnPropertyDescriptor(prototype, 'replaceChildren');
  const original = descriptor?.value as ((...nodes: (Node | string)[]) => void) | undefined;
  if (!original) return;
  Object.defineProperty(prototype, 'replaceChildren', {
    configurable: true,
    enumerable: descriptor?.enumerable ?? false,
    writable: true,
    value(this: Node, ...nodes: (Node | string)[]): void {
      const owner = this.ownerDocument ?? document;
      const prepared = nodes.map((node) => {
        if (!isTranslated(node)) return node;
        const text = owner.createTextNode(String(node));
        registerText(text, node);
        return text;
      });
      original.call(this, ...prepared);
    },
  });
}

/**
 * An attribute that holds a message is remembered as well, so that a title
 * or a label is made again when the language changes.
 */
function patchAttributes(): void {
  const descriptor = Object.getOwnPropertyDescriptor(Element.prototype, 'setAttribute');
  const original = descriptor?.value as ((this: Element, name: string, value: string) => void) | undefined;
  if (!original) return;
  nativeSetAttribute = original;
  Object.defineProperty(Element.prototype, 'setAttribute', {
    configurable: true,
    enumerable: descriptor?.enumerable ?? false,
    writable: true,
    value(this: Element, name: string, value: string): void {
      if (isTranslated(value)) registerAttribute(this, name, value);
      else forgetAttribute(this, name);
      original.call(this, name, String(value));
    },
  });

  // The title and the placeholder are attributes with a shorter way to set
  // them, and the shorter way does not go through setAttribute.
  for (const prototype of prototypesFor('title')) patchReflectedAttribute(prototype, 'title');
  for (const prototype of prototypesFor('placeholder')) patchReflectedAttribute(prototype, 'placeholder');
}

/**
 * The prototypes that hold a property with that name.
 *
 * Browsers put the title on Element and the placeholder on HTMLElement, and
 * a test environment may put them somewhere else again; every prototype
 * that has the property is looked for.
 */
function prototypesFor(name: string): object[] {
  const candidates: unknown[] = [];
  if (typeof Element !== 'undefined') candidates.push(Element.prototype);
  if (typeof HTMLElement !== 'undefined') candidates.push(HTMLElement.prototype);
  if (typeof HTMLInputElement !== 'undefined') candidates.push(HTMLInputElement.prototype);
  if (typeof HTMLTextAreaElement !== 'undefined') candidates.push(HTMLTextAreaElement.prototype);
  return candidates.filter(
    (prototype): prototype is object => typeof prototype === 'object' && Boolean(Object.getOwnPropertyDescriptor(prototype, name)),
  );
}

/** Remembers a message that is set through a property that mirrors an attribute. */
function patchReflectedAttribute(prototype: object, name: string): void {
  const descriptor = Object.getOwnPropertyDescriptor(prototype, name);
  if (!descriptor?.get || !descriptor.set) return;
  const get = descriptor.get as (this: unknown) => unknown;
  const set = descriptor.set as (this: unknown, value: unknown) => void;
  Object.defineProperty(prototype, name, {
    configurable: true,
    enumerable: descriptor.enumerable,
    get(this: unknown): unknown {
      return get.call(this);
    },
    set(this: unknown, value: unknown): void {
      if (isTranslated(value)) registerAttribute(this as Element, name, value);
      else forgetAttribute(this as Element, name);
      set.call(this, String(value));
    },
  });
}

if (typeof Node !== 'undefined') {
  patchTextContent();
  patchReplaceChildren(Element.prototype);
  patchReplaceChildren(DocumentFragment.prototype);
  patchAttributes();
}

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

function lookup(key: string, locale: Locale): string | undefined {
  return (CATALOGS[locale] as Record<string, string | undefined>)[key];
}

/** The text of a key in the current language. */
function textOf(key: string): string {
  return lookup(key, current) ?? lookup(key, 'en') ?? key;
}

/**
 * Fills {placeholders}. A placeholder with no value is left as written, which
 * shows the gap. A placeholder that holds a message is made again, so that a
 * sentence built out of messages can be built again too.
 */
export function format(template: string, params?: Params): string {
  if (!params) return template;
  return template.replace(/\{(\w+)\}/g, (whole, name: string) => {
    if (!(name in params)) return whole;
    const value = params[name];
    if (isTranslated(value)) return resolve(value);
    return typeof value === 'number' ? new Intl.NumberFormat(current).format(value) : value;
  });
}

/** The text of one piece of a message, in the language in effect now. */
function resolveSegment(segment: Segment): string {
  if (segment.kind === 'text') return segment.text;
  if (segment.kind === 'plural') {
    const rule = new Intl.PluralRules(current).select(segment.count);
    const form = (name: string): string | undefined =>
      lookup(`${segment.base}.${name}`, current) ?? lookup(`${segment.base}.${name}`, 'en');
    const text = form(rule) ?? form('other') ?? segment.base;
    return format(text, { n: segment.count, ...segment.params });
  }
  return format(textOf(segment.key), segment.params);
}

/** The text of a message, in the language in effect now. */
function resolveSegments(segments: readonly Segment[]): string {
  return segments.map(resolveSegment).join('');
}

/** The text of a translated string, made again in the language in effect now. */
function resolve(message: Translated): string {
  return message.segments ? resolveSegments(message.segments) : String(message);
}

/** The pieces of a part of a message. */
function segmentsOf(part: string | number | Translated): Segment[] {
  if (isTranslated(part)) return part.segments ? [...part.segments] : [{ kind: 'text', text: String(part) }];
  return [{ kind: 'text', text: String(part) }];
}

/** The text for a key in the current language, with its placeholders filled. */
export function t(key: MessageKey, params?: Params): string {
  const segments: Segment[] = [{ kind: 'message', key, params }];
  return new Translated(resolveSegments(segments), { key, params, segments }) as unknown as string;
}

/**
 * The text of a plural message for a count: tn('findings.count', 3) reads
 * "findings.count.other" in English. The count is available as {n}.
 */
export function tn(base: PluralKey, count: number, params?: Params): string {
  const rule = new Intl.PluralRules(current).select(count);
  const form = (name: string): string | undefined =>
    lookup(`${base}.${name}`, current) ?? lookup(`${base}.${name}`, 'en');
  const chosen = form(rule) ? rule : form('other') ? 'other' : '';
  const key = (chosen ? `${base}.${chosen}` : base) as MessageKey;
  const filled = { n: count, ...params };
  const segments: Segment[] = [{ kind: 'plural', base, count, params }];
  return new Translated(resolveSegments(segments), { key, params: filled, segments }) as unknown as string;
}

/**
 * Puts several messages one after another, separated by a sign.
 *
 * The pieces are kept, so that the whole line can be made again when the
 * language changes: a language may want another sign, or another word
 * between the pieces. Empty pieces are left out.
 */
export function joinMessages(
  parts: readonly (string | number | Translated | null | undefined | false)[],
  separator: string | Translated = ' ',
): string {
  const segments: Segment[] = [];
  const kept = parts.filter(
    (part): part is string | number | Translated =>
      part !== null && part !== undefined && part !== false && String(part) !== '',
  );
  kept.forEach((part, index) => {
    if (index > 0) segments.push(...segmentsOf(separator));
    segments.push(...segmentsOf(part));
  });
  return new Translated(resolveSegments(segments), { segments }) as unknown as string;
}

/** The text an element shows right now. */
function readText(node: Node): string {
  if (node.nodeType === Node.TEXT_NODE) return (node as Text).nodeValue ?? '';
  const element = node as Element;
  const first = element.firstChild;
  if (first && first.nodeType === Node.TEXT_NODE) return first.nodeValue ?? '';
  return element.textContent ?? '';
}

/** Writes text to an element, without touching the children it holds. */
function writeText(node: Node, text: string): void {
  if (node.nodeType === Node.TEXT_NODE) {
    (node as Text).nodeValue = text;
    return;
  }
  const element = node as Element;
  const first = element.firstChild;
  if (first && first.nodeType === Node.TEXT_NODE) {
    (first as Text).nodeValue = text;
    return;
  }
  // The native setter is used on purpose: the patched one would take the
  // message away, and the message is still needed for the next language.
  if (nativeTextSet) nativeTextSet.call(node, text);
  else element.textContent = text;
}

/** Where two elements stand on the screen, so that the wave can follow. */
function domOrder(a: Node, b: Node): number {
  if (a === b) return 0;
  const position = a.compareDocumentPosition(b);
  if (position & Node.DOCUMENT_POSITION_FOLLOWING) return -1;
  if (position & Node.DOCUMENT_POSITION_PRECEDING) return 1;
  return 0;
}

/**
 * Puts the interface in the language that is in effect now.
 *
 * Every message that is on screen is made again, so the interface speaks the
 * new language without being built again — and nothing is lost while it
 * changes: the open file, the terminal, the chat and the scroll positions
 * stay as they are. The new text is typed out, unless the system asks for
 * less motion.
 */
export function relocalize(): Promise<void> {
  const changed: { node: Node; text: string }[] = [];
  for (const ref of [...tracked]) {
    const node = ref.deref() as Node | undefined;
    if (!node) {
      tracked.delete(ref);
      continue;
    }
    const map = entries.get(node);
    if (!map) continue;
    for (const [attr, entry] of map) {
      const text = resolveSegments(entry.segments);
      if (attr) {
        // A title is not part of the page: it changes at once.
        const element = node as Element;
        if (element.getAttribute(attr) !== text) {
          if (nativeSetAttribute) nativeSetAttribute.call(element, attr, text);
          else element.setAttribute(attr, text);
        }
        continue;
      }
      if (readText(node) !== text) changed.push({ node, text });
    }
  }
  changed.sort((a, b) => domOrder(a.node, b.node));
  return typewrite(changed.map((item) => ({ text: item.text, write: (text) => writeText(item.node, text) })));
}

/** The placeholders a message uses, for checking that a translation keeps them. */
export function placeholdersOf(text: string): string[] {
  return Array.from(new Set(Array.from(text.matchAll(/\{(\w+)\}/g), (m) => m[1]))).sort();
}
