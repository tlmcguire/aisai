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

import { ApiClient } from './api/client';
import { AppController } from './app';
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
