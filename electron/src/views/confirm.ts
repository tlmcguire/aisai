/**
 * An in-app confirmation dialog, used in place of window.confirm.
 *
 * window.confirm is a native sheet that does not follow the app's theme or
 * text size, and screen readers announce it poorly. This one is a modal
 * <dialog> (https://developer.mozilla.org/docs/Web/HTML/Element/dialog): the
 * browser traps focus inside it while it is open, Escape cancels, and the
 * safe choice is focused first so that Enter does not confirm a destructive
 * action by accident.
 */

import { h } from '../lib/dom';
import { t } from '../lib/i18n';

export interface ConfirmOptions {
  /** The button that says yes, e.g. "Save anyway". */
  confirmLabel: string;
  /** The button that says no. */
  cancelLabel?: string;
  /** Marks the confirm button as destructive. */
  danger?: boolean;
  /** A short title; the message is the body. */
  title?: string;
}

/** Shows the dialog and resolves to true when the user confirms. */
export function confirmDialog(message: string, options: ConfirmOptions): Promise<boolean> {
  return new Promise((resolve) => {
    const dialog = h('dialog', { class: 'confirm-dialog', 'aria-labelledby': 'confirm-title', 'aria-describedby': 'confirm-message' });
    let settled = false;
    const finish = (answer: boolean): void => {
      if (settled) return;
      settled = true;
      dialog.close();
      dialog.remove();
      resolve(answer);
    };

    const cancel = h('button', { type: 'button', class: 'tiny' }, options.cancelLabel ?? t('confirm.cancel'));
    const confirm = h('button', { type: 'button', class: options.danger ? 'danger tiny' : 'primary tiny' }, options.confirmLabel);
    cancel.addEventListener('click', () => finish(false));
    confirm.addEventListener('click', () => finish(true));
    // Closing by Escape (or any other route) counts as cancelling.
    dialog.addEventListener('cancel', (event) => {
      event.preventDefault();
      finish(false);
    });

    dialog.append(
      h('h2', { id: 'confirm-title', class: 'confirm-title', text: options.title ?? t('confirm.title') }),
      h('p', { id: 'confirm-message', class: 'confirm-message', text: message }),
      h('div', { class: 'confirm-actions' }, cancel, confirm),
    );
    document.body.append(dialog);
    dialog.showModal();
    // The safe choice has focus, so Enter cancels unless the user moves to the other button.
    cancel.focus();
  });
}
