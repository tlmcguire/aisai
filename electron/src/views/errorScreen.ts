import { h } from '../lib/dom';

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
  const detail = h('div', { class: 'detail', text: '' });
  const retry = h('button', { class: 'primary', type: 'button' }, 'Retry');
  const continueWithMock = h('button', { type: 'button' }, 'Continue with mock data');

  const card = h(
    'div',
    { class: 'error-card' },
    h('h1', { text: 'The AISAI backend could not start' }),
    h('p', {
      text:
        'AISAI talks to a Go sidecar over a local socket. The sidecar is not responding, so the ' +
        'app cannot load audits right now.',
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

  const apply = (show: boolean): void => {
    element.hidden = !show;
    for (const target of hide) target.hidden = show;
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
