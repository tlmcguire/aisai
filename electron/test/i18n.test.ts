// @vitest-environment jsdom
/**
 * The interface can be put in another language without being built again.
 *
 * Every piece of text keeps the key it came from, and the interface
 * remembers where each of them is shown, so a new language only has to fill
 * them all in again.
 */

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { joinMessages, relocalize, t, tn, useLocaleStore } from '../src/lib/i18n';

beforeEach(() => {
  document.body.replaceChildren();
  // The typewriter asks the system whether less motion is wanted; here the
  // answer is always yes, so that the text is there at once.
  vi.stubGlobal('matchMedia', (query: string) => ({
    matches: query.includes('prefers-reduced-motion'),
    media: query,
    onchange: null,
    addEventListener: () => undefined,
    removeEventListener: () => undefined,
    addListener: () => undefined,
    removeListener: () => undefined,
    dispatchEvent: () => false,
  }));
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('a language change', () => {
  it('makes the text of an element again', async () => {
    useLocaleStore.getState().setPreference('en');
    const label = document.createElement('span');
    label.textContent = t('toolbar.inspecting');
    document.body.append(label);
    expect(label.textContent).toBe('Inspecting');

    useLocaleStore.getState().setPreference('de');
    await relocalize();
    expect(label.textContent).toBe('Wird untersucht');
  });

  it('makes the title of a button again', async () => {
    useLocaleStore.getState().setPreference('en');
    const button = document.createElement('button');
    button.title = t('toolbar.clickToConfigure');
    document.body.append(button);

    useLocaleStore.getState().setPreference('fr');
    await relocalize();
    expect(button.getAttribute('title')).toBe('cliquez pour configurer');
  });

  it('keeps the parts of a line that is made of several messages', async () => {
    useLocaleStore.getState().setPreference('en');
    const line = document.createElement('span');
    line.textContent = joinMessages([t('toolbar.partialCopy'), t('toolbar.inspecting')], t('toolbar.dotSeparator'));
    document.body.append(line);
    expect(line.textContent).toBe('partial copy · Inspecting');

    useLocaleStore.getState().setPreference('ja');
    await relocalize();
    expect(line.textContent).toBe('部分的なコピー · 解析中');
  });

  it('counts in the language that is in effect', async () => {
    useLocaleStore.getState().setPreference('en');
    const label = document.createElement('span');
    label.textContent = tn('graph.finding', 1);
    document.body.append(label);
    expect(label.textContent).toBe('1 finding');

    useLocaleStore.getState().setPreference('ja');
    await relocalize();
    expect(label.textContent).toBe('1 件の検出事項');
  });

  it('leaves the text that the backend sent alone', async () => {
    useLocaleStore.getState().setPreference('en');
    const label = document.createElement('span');
    label.textContent = 'package-lock.json';
    document.body.append(label);

    useLocaleStore.getState().setPreference('ja');
    await relocalize();
    expect(label.textContent).toBe('package-lock.json');
  });
});
