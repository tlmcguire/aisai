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

/** The wave is for the eye, so it needs motion to be seen at all. */
function allowMotion(): void {
  vi.stubGlobal('matchMedia', (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addEventListener: () => undefined,
    removeEventListener: () => undefined,
    addListener: () => undefined,
    removeListener: () => undefined,
    dispatchEvent: () => false,
  }));
}

/**
 * Puts only the given element on the screen.
 *
 * A test browser cannot measure a range, so the measuring is done here: an
 * element that is not the one has no box at all.
 */
function showOnly(element: Element): void {
  const box = (visible: boolean): DOMRect =>
    ({
      x: 0,
      y: 0,
      width: visible ? 40 : 0,
      height: visible ? 12 : 0,
      top: 0,
      left: 0,
      right: visible ? 40 : 0,
      bottom: visible ? 12 : 0,
      toJSON: () => '',
    }) as DOMRect;
  Object.defineProperty(Range.prototype, 'getBoundingClientRect', {
    configurable: true,
    value(this: Range): DOMRect {
      const holder =
        this.startContainer.nodeType === Node.ELEMENT_NODE
          ? (this.startContainer as Element)
          : this.startContainer.parentElement;
      return box(holder === element);
    },
  });
}

afterEach(() => {
  vi.unstubAllGlobals();
  // The measuring of a range is put back as it was.
  delete (Range.prototype as unknown as { getBoundingClientRect?: unknown }).getBoundingClientRect;
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

  it('says that it is translating while it does', async () => {
    useLocaleStore.getState().setPreference('en');
    const label = document.createElement('span');
    label.textContent = t('toolbar.inspecting');
    document.body.append(label);

    useLocaleStore.getState().setPreference('de');
    const promise = relocalize();
    expect(useLocaleStore.getState().translating).toBe(true);
    await promise;
    expect(useLocaleStore.getState().translating).toBe(false);
  });

  it('keeps saying that it is translating while the last wave runs', async () => {
    allowMotion();
    useLocaleStore.getState().setPreference('en');
    const label = document.createElement('span');
    label.textContent = t('toolbar.inspecting');
    document.body.append(label);
    showOnly(label);

    useLocaleStore.getState().setPreference('de');
    const first = relocalize();
    const second = relocalize();
    // The first wave is stopped by the second one, but the work is not done
    // until the last one is.
    await first;
    expect(useLocaleStore.getState().translating).toBe(true);
    await second;
    expect(useLocaleStore.getState().translating).toBe(false);
  });

  it('types the text that is on the screen and puts the rest there at once', async () => {
    allowMotion();
    useLocaleStore.getState().setPreference('en');
    const onScreen = document.createElement('span');
    onScreen.textContent = t('toolbar.inspecting');
    const away = document.createElement('span');
    away.textContent = t('toolbar.inspecting');
    document.body.append(onScreen, away);
    showOnly(onScreen);

    useLocaleStore.getState().setPreference('de');
    const promise = relocalize();
    // The text that is out of sight is there at once, and the text that is
    // on the screen is still the old text: it is about to be typed out.
    expect(away.textContent).toBe('Wird untersucht');
    expect(onScreen.textContent).toBe('Inspecting');
    await promise;
    expect(onScreen.textContent).toBe('Wird untersucht');
  });

  it('animates elements inside an open dialog before elements in the background', async () => {
    allowMotion();
    useLocaleStore.getState().setPreference('en');

    const backgroundLabel = document.createElement('span');
    backgroundLabel.textContent = t('toolbar.inspecting');
    document.body.append(backgroundLabel);

    const dialog = document.createElement('dialog');
    dialog.setAttribute('open', '');
    const dialogLabel = document.createElement('span');
    dialogLabel.textContent = t('settings.appearanceTitle');
    dialog.append(dialogLabel);
    document.body.append(dialog);

    showOnly(dialogLabel);

    useLocaleStore.getState().setPreference('de');
    const promise = relocalize();
    await promise;
    expect(dialogLabel.textContent).toBe('Darstellung');
  });
});
