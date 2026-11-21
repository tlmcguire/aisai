/**
 * Renderer entry point.
 *
 * Builds the shell, wires the typed client to the Electron bridge and boots
 * the controller once the backend reports it is ready (or reports that it is
 * missing, in which case the mock backend takes over).
 */

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

const client = new ApiClient({ bridge: window.aisai });
const controller = new AppController(client);

const layout = createLayout(controller, root);
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
