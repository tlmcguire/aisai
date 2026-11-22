/**
 * Electron preload.
 *
 * Runs in a sandboxed, context-isolated world. It exposes exactly one object,
 * `window.aisai`, and nothing else. The renderer never sees the socket path,
 * the filesystem, or any credential.
 */

import { contextBridge, ipcRenderer, webUtils } from 'electron';

import type {
  AisaiBridge,
  BackendStatus,
  ClientMessage,
  HttpMethod,
  HttpResponse,
} from './src/api/bridge';
import type { Event } from './src/api/types';

const eventListeners = new Set<(event: Event) => void>();
const statusListeners = new Set<(status: BackendStatus) => void>();

let currentStatus: BackendStatus = {
  state: 'starting',
  mode: 'mock',
  message: 'Connecting to the AISAI backend…',
};

ipcRenderer.on('aisai:event', (_event, payload: Event) => {
  for (const listener of Array.from(eventListeners)) {
    try {
      listener(payload);
    } catch (err) {
      console.error('aisai event listener failed', err);
    }
  }
});

ipcRenderer.on('aisai:status', (_event, payload: BackendStatus) => {
  currentStatus = payload;
  for (const listener of Array.from(statusListeners)) {
    try {
      listener(payload);
    } catch (err) {
      console.error('aisai status listener failed', err);
    }
  }
});

// Pull the current status synchronously so `getMode()` is correct even before
// the first status broadcast arrives.
try {
  const snapshot = ipcRenderer.sendSync('aisai:status:get') as BackendStatus | undefined;
  if (snapshot) currentStatus = snapshot;
} catch {
  /* main process not ready yet — the broadcast will arrive shortly */
}

const bridge: AisaiBridge = {
  getMode: () => currentStatus.mode,
  getStatus: () => currentStatus,
  http: (method: HttpMethod, path: string, body?: unknown): Promise<HttpResponse> =>
    ipcRenderer.invoke('aisai:http', method, path, body ?? null) as Promise<HttpResponse>,
  connectEvents: (cb: (event: Event) => void) => {
    eventListeners.add(cb);
  },
  disconnect: () => {
    eventListeners.clear();
    void ipcRenderer.invoke('aisai:disconnect');
  },
  onBackendStatus: (cb: (status: BackendStatus) => void) => {
    statusListeners.add(cb);
    cb(currentStatus);
    return () => {
      statusListeners.delete(cb);
    };
  },
  send: (message: ClientMessage) => {
    void ipcRenderer.invoke('aisai:ws:send', message);
  },
  chooseFolder: () => ipcRenderer.invoke('aisai:folder:choose') as Promise<string | null>,
  providerKeyStatus: (kind: string) =>
    ipcRenderer.invoke('aisai:secret:has', kind) as Promise<{ saved: boolean; available: boolean }>,
  forgetProviderKey: (kind: string) => ipcRenderer.invoke('aisai:secret:forget', kind) as Promise<void>,
  folderFromDrop: async (file: File): Promise<string | null> => {
    // getPathForFile only answers for a real file the user dropped, so a
    // page cannot make one up.
    let dropped = '';
    try {
      dropped = webUtils.getPathForFile(file);
    } catch {
      return null;
    }
    if (!dropped) return null;
    return (await ipcRenderer.invoke('aisai:folder:approve', dropped)) as string | null;
  },
};

contextBridge.exposeInMainWorld('aisai', bridge);
