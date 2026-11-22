/**
 * Renderer entry point.
 *
 * Builds the shell, wires the typed client to the Electron bridge and boots
 * the controller once the backend reports it is ready (or reports that it is
 * missing, in which case the mock backend takes over).
 */

import '@fontsource-variable/jetbrains-mono';
import './styles/tokens.css';
import './styles/app.css';
// Editor theme and accessibility settings are applied before the first paint.
import './state/prefs';

import { ApiClient } from './api/client';
import { AppController } from './app';
import { relocalize, useLocaleStore } from './lib/i18n';
import { createErrorScreen } from './views/errorScreen';
import { createLayout } from './views/layout';

const root = document.getElementById('app');
if (!root) {
  throw new Error('the #app container is missing from index.html');
}

// The platform decides the window chrome: where the window controls sit and
// which system font the interface uses.
const ua = navigator.userAgent;
document.documentElement.classList.add(
  /Macintosh|Mac OS X/.test(ua) ? 'platform-mac' : /Windows/.test(ua) ? 'platform-win' : 'platform-linux',
);

const client = new ApiClient({ bridge: window.aisai });
const controller = new AppController(client);

const layout = createLayout(controller, root);

// One authored entrance, then the class is removed so later renders are calm.
root.classList.add('is-booting');
window.setTimeout(() => root.classList.remove('is-booting'), 1100);
const errorScreen = createErrorScreen(root, Array.from(root.children) as HTMLElement[]);

useLocaleStore.subscribe((state, previous) => {
  if (state.locale === previous.locale) return;
  window.aisai.setLocale(state.locale);
  // Every message on screen is made again in the new language. The
  // interface is not built a second time, so the open file, the terminal,
  // the chat and the scroll positions all stay as they are.
  void relocalize().then(() => {
    // A title that was made up from a button's own text can only be filled
    // in once the new text has arrived.
    refreshTooltips();
  });
});

// The application menu follows the language of the interface.
window.aisai.setLocale(useLocaleStore.getState().locale);

let booted = false;
const boot = async (): Promise<void> => {
  if (booted) return;
  booted = true;
  try {
    await controller.bootstrap();
  } catch (err) {
    // The controller already surfaces per-request errors; this is the last resort.
    console.error('bootstrap failed', err);
  }
};

window.aisai.onBackendStatus((status) => {
  if (status.state === 'ready' || status.state === 'mock') {
    void boot();
  }
});

// The backend may already be up by the time the renderer boots.
if (client.ready) {
  void boot();
}

window.addEventListener('beforeunload', () => {
  controller.dispose();
  layout.dispose();
  errorScreen.dispose();
});

// Every button explains itself on hover: one that has no tooltip of its own
// gets its accessible name (or its text) as one.
const addTooltips = (root: ParentNode): void => {
  root.querySelectorAll<HTMLElement>('button:not([title])').forEach((button) => {
    const text = button.getAttribute('aria-label') || button.textContent?.trim() || '';
    if (text && text.length <= 60) {
      button.setAttribute('title', text);
      // The title was made up from the button's own text, so it has to be
      // filled again when the language changes.
      button.dataset.autoTitle = 'true';
    }
  });
};

/** Fills the titles that were made up from the button's own text again. */
const refreshTooltips = (): void => {
  document.querySelectorAll<HTMLElement>('button[data-auto-title]').forEach((button) => {
    const text = button.getAttribute('aria-label') || button.textContent?.trim() || '';
    if (text && text.length <= 60) button.setAttribute('title', text);
  });
};
addTooltips(document);
new MutationObserver((records) => {
  for (const record of records) {
    record.addedNodes.forEach((node) => {
      if (node instanceof HTMLElement) {
        if (node.matches('button:not([title])')) addTooltips(node.parentElement ?? document);
        else addTooltips(node);
      }
    });
  }
}).observe(document.body, { childList: true, subtree: true });
