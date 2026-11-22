import { h } from '../lib/dom';
import { t } from '../lib/i18n';

/** Anything with a `hidden` flag: an element, or a stand-in in a test. */
export interface Hideable {
  hidden: boolean;
}

/**
 * Hides a set of things and later puts back exactly the ones it hid.
 *
 * The error screen used to set `hidden = false` on every element it was given
 * when the error cleared. Anything in that list that was meant to stay hidden
 * (the tour overlay, say, which only shows while a tour runs) was un-hidden by
 * accident. Remembering what was visible before keeps those untouched.
 */
export function createHider(targets: Hideable[]): { hide: () => void; restore: () => void } {
  const hiddenByUs = new Set<Hideable>();
  return {
    hide: () => {
      for (const target of targets) {
        if (target.hidden) continue; // already hidden by someone else: not ours to restore
        target.hidden = true;
        hiddenByUs.add(target);
      }
    },
    restore: () => {
      for (const target of hiddenByUs) target.hidden = false;
      hiddenByUs.clear();
    },
  };
}

export interface ErrorScreen {
  element: HTMLElement;
  dispose: () => void;
}

/**
 * Full-screen, styled error state.
 *
 * The window must never be blank: when the Go backend cannot start we show
 * what went wrong (the backend's own message, never a stack trace) and offer
 * a retry or a fallback to mock data.
 */
export function createErrorScreen(
  root: HTMLElement,
  hide: HTMLElement[],
): ErrorScreen {
  const detail = h('div', { class: 'detail', id: 'error-detail', text: '' });
  const retry = h('button', { class: 'primary', type: 'button' }, t('error.retry'));
  const continueWithMock = h('button', { type: 'button' }, t('error.continueWithMock'));

  // role=alert makes a screen reader announce the failure the moment the
  // screen appears; aria-labelledby names the dialog by its heading.
  const card = h(
    'div',
    { class: 'error-card', role: 'alert', 'aria-labelledby': 'error-heading', 'aria-describedby': 'error-detail' },
    h('h1', { id: 'error-heading', text: t('error.title') }),
    h('p', {
      text: t('error.description'),
    }),
    detail,
    h('div', { class: 'row' }, retry, continueWithMock),
  );

  const element = h('div', { class: 'error-screen', hidden: true }, card);
  root.append(element);

  retry.addEventListener('click', () => {
    window.location.reload();
  });

  let dismissed = false;
  continueWithMock.addEventListener('click', () => {
    dismissed = true;
    apply(false);
  });

  const hider = createHider(hide);
  const apply = (show: boolean): void => {
    element.hidden = !show;
    if (show) {
      hider.hide();
      // Keyboard users land on the main action instead of on a hidden page.
      retry.focus();
    } else {
      hider.restore();
    }
  };

  const unsubscribe = window.aisai.onBackendStatus((status) => {
    if (dismissed) return;
    if (status.state === 'error') {
      detail.textContent = status.message;
      apply(true);
    } else {
      apply(false);
    }
  });

  return {
    element,
    dispose: () => {
      unsubscribe();
    },
  };
}
