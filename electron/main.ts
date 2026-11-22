/**
 * AISAI Electron main process.
 *
 * Responsibilities:
 *   - create the BrowserWindow with a locked-down webPreferences block;
 *   - start the Go backend as a sidecar and read its "listening" line;
 *   - proxy HTTP requests and WebSocket events to the renderer over IPC.
 *
 * The renderer never receives a socket path, a file handle or a credential.
 */

import path from 'node:path';
import { writeFile } from 'node:fs/promises';

import { app, BrowserWindow, dialog, ipcMain, shell, type OpenDialogOptions } from 'electron';
import { autoUpdater } from 'electron-updater';

import type { BackendStatus, ClientMessage, HttpMethod, HttpResponse, UpdateStatus } from './src/api/bridge';
import type { Event } from './src/api/types';
import { FolderApprovals } from './src/main-process/folders';
import { activeSecret, canStoreSecrets, forgetSecret, hasSecret, readSecret, saveSecret, setActiveProvider } from './src/main-process/secrets';
import { backendRequest } from './src/main-process/http';
import { defaultSocketPath, findSidecar } from './src/main-process/paths';
import { startSidecar, type SidecarHandle } from './src/main-process/sidecar';
import { WsClient } from './src/main-process/ws';

const isDev = Boolean(process.env.VITE_DEV_SERVER_URL);

let mainWindow: BrowserWindow | null = null;
let sidecar: SidecarHandle | null = null;
let wsClient: WsClient | null = null;

/** The project folders the user picked. Only these can be audited. */
const folders = new FolderApprovals();

/** Where the backend is reachable. Never handed to the renderer. */
let backendAddress: { socketPath?: string; host?: string; port?: number } | null = null;

let status: BackendStatus = {
  state: 'starting',
  mode: 'mock',
  message: 'Starting the AISAI backend…',
};

function setStatus(next: BackendStatus): void {
  status = next;
  if (mainWindow && !mainWindow.isDestroyed()) {
    mainWindow.webContents.send('aisai:status', status);
  }
}

/** Turn an unknown throwable into a message that is safe to display. */
export function readableError(err: unknown): string {
  if (err instanceof Error) return err.message;
  if (typeof err === 'string') return err;
  return 'Unknown error';
}

/**
 * Development-only smoke test: drives the file tree, the code pane, the AST
 * tree and the proposed diff, then writes a screenshot of each step.
 */
async function runSmoke(win: BrowserWindow, smokePath: string): Promise<void> {
  const base = smokePath.replace(/\.png$/, '');
  const wait = (ms: number): Promise<void> =>
    new Promise((resolve) => setTimeout(resolve, ms));

  const shoot = async (name: string): Promise<void> => {
    const image = await win.webContents.capturePage();
    await writeFile(`${base}-${name}.png`, image.toPNG());
  };

  try {
    await wait(2500);
    await shoot('1-shell');

    const codeReport = (await win.webContents.executeJavaScript(`
      (async () => {
        const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
        const byText = (sel, text) => Array.from(document.querySelectorAll(sel))
          .find((el) => (el.textContent || '').trim().startsWith(text));
        const file = byText('.tree-row .name', 'security.py');
        if (file) file.click();
        await sleep(700);
        return [
          'clicked=' + !!file,
          'lines=' + document.querySelectorAll('.code-line').length,
          'marked=' + document.querySelectorAll('.code-line.has-finding').length,
          'header=' + (document.querySelector('.file-header')?.textContent || '').trim(),
        ].join(' ');
      })()
    `)) as string;
    console.log(`[smoke] code pane: ${codeReport}`);
    await shoot('2-code');

    const astReport = (await win.webContents.executeJavaScript(`
      (async () => {
        const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
        const tab = Array.from(document.querySelectorAll('.tab')).find((t) => t.textContent.trim() === 'AST');
        if (tab) tab.click();
        await sleep(500);
        return 'rows=' + document.querySelectorAll('.ast-row').length
          + ' typed=' + document.querySelectorAll('.ast-row[class*="type-"]').length
          + ' findings=' + document.querySelectorAll('.ast-row.has-finding').length;
      })()
    `)) as string;
    console.log(`[smoke] ast pane: ${astReport}`);
    await shoot('3-ast');

    const diffReport = (await win.webContents.executeJavaScript(`
      (async () => {
        const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
        const button = Array.from(document.querySelectorAll('.finding-card button'))
          .find((b) => b.textContent.trim() === 'Propose fix');
        if (button) button.click();
        await sleep(900);
        const tab = Array.from(document.querySelectorAll('.tab')).find((t) => t.textContent.trim() === 'Diff');
        if (tab) tab.click();
        await sleep(400);
        return 'added=' + document.querySelectorAll('.diff-line.add').length
          + ' removed=' + document.querySelectorAll('.diff-line.remove').length
          + ' hunks=' + document.querySelectorAll('.hunk-header').length;
      })()
    `)) as string;
    console.log(`[smoke] diff pane: ${diffReport}`);
    await shoot('4-diff');

    const applyReport = (await win.webContents.executeJavaScript(`
      (async () => {
        const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
        const apply = Array.from(document.querySelectorAll('.diff-toolbar button'))
          .find((b) => b.textContent.trim() === 'Apply');
        if (!apply) return 'no apply button';
        apply.click();
        await sleep(900);
        return 'summary=' + (document.querySelector('.patch-toolbar .legend-item')?.textContent || '').trim();
      })()
    `)) as string;
    console.log(`[smoke] apply patch: ${applyReport}`);

    const chatReport = (await win.webContents.executeJavaScript(`
      (async () => {
        const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
        const input = document.querySelector('.chat-footer input');
        if (!input) return 'no chat input';
        input.value = 'where is the secret used?';
        input.dispatchEvent(new Event('input', { bubbles: true }));
        const ask = Array.from(document.querySelectorAll('.chat-footer button'))
          .find((b) => b.textContent.trim() === 'Ask');
        if (ask) ask.click();
        await sleep(2500);
        return 'messages=' + document.querySelectorAll('.chat-message').length
          + ' citations=' + document.querySelectorAll('.citation').length;
      })()
    `)) as string;
    console.log(`[smoke] chat: ${chatReport}`);
    await shoot('5-chat');

    const click = async (script: string): Promise<string> =>
      (await win.webContents.executeJavaScript(`(async () => {
        const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
        const byText = (sel, text) => Array.from(document.querySelectorAll(sel)).find((el) => (el.textContent || '').trim().startsWith(text));
        ${script}
      })()`)) as string;

    // The syntax tree as a graph.
    await click(`
      byText('.tab', 'AST')?.click(); await sleep(300);
      const depth = document.querySelector('select[aria-label="Maximum AST depth to show"]');
      if (depth) { depth.value = "3"; depth.dispatchEvent(new Event('change', { bubbles: true })); }
      byText('.segmented button', 'Graph')?.click();
      await sleep(1200); return 'ok';`);
    await shoot('6-ast-graph');

    // Settings → Layout, with the presets.
    await click(`
      document.querySelector('button[aria-label="Settings"]')?.click(); await sleep(400);
      byText('.pref-nav button, .pref-nav a, nav button', 'Layout')?.click(); await sleep(500); return 'ok';`);
    await shoot('7-layout');

    // The Reader preset: code with the chat docked on the right.
    await click(`
      byText('.preset-card', 'Reader')?.click(); await sleep(300);
      document.querySelector('dialog')?.close(); await sleep(600); return 'ok';`);
    await shoot('8-reader');

    // The package picker with another package manager.
    await click(`
      byText('.preset-card', 'Classic');
      const sel = document.getElementById('ecosystem-input');
      if (sel) { sel.value = 'npm'; sel.dispatchEvent(new Event('change', { bubbles: true })); }
      const input = document.getElementById('package-input');
      if (input) { input.focus(); input.value = 'expr'; input.dispatchEvent(new Event('input', { bubbles: true })); }
      await sleep(900); return 'ok';`);
    await shoot('9-npm');
  } catch (err) {
    console.error('[smoke] failed', err);
  } finally {
    app.quit();
  }
}

function createWindow(): BrowserWindow {
  const win = new BrowserWindow({
    width: 1440,
    height: 900,
    minWidth: 960,
    minHeight: 600,
    show: false,
    backgroundColor: '#0a1112',
    title: 'AISAI',
    // Native window chrome: the toolbar is the title bar. macOS keeps its
    // traffic lights inset in the toolbar; Windows and Linux draw their
    // window controls over the right end of it.
    titleBarStyle: process.platform === 'darwin' ? 'hiddenInset' : 'hidden',
    ...(process.platform === 'darwin'
      ? { trafficLightPosition: { x: 16, y: 17 } }
      : { titleBarOverlay: { color: '#0f1819', symbolColor: '#dde8e3', height: 48 } }),
    webPreferences: {
      preload: path.join(__dirname, 'preload.js'),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
      webSecurity: true,
      spellcheck: false,
    },
  });

  win.once('ready-to-show', () => win.show());

  // The renderer only ever shows local content. Nothing in the app should
  // be able to navigate it to a remote page or open a browser window: the
  // package under audit is untrusted input, and a finding's text or a
  // file's contents must never become a navigation target.
  win.webContents.setWindowOpenHandler(() => ({ action: 'deny' }));
  win.webContents.on('will-navigate', (event, url) => {
    if (url !== win.webContents.getURL()) {
      event.preventDefault();
    }
  });
  win.webContents.on('will-attach-webview', (event) => {
    event.preventDefault();
  });

  // Development smoke check: render the app, drive the main flows, save
  // screenshots and exit. Only active when AISAI_SMOKE_SCREENSHOT is set.
  const smokePath = process.env.AISAI_SMOKE_SCREENSHOT;
  if (smokePath) {
    win.webContents.on('console-message', (event: unknown) => {
      const detail = event as { message?: string };
      console.log(`[renderer] ${detail.message ?? ''}`);
    });
    win.webContents.once('did-finish-load', () => {
      void runSmoke(win, smokePath);
    });
  }

  if (isDev) {
    void win.loadURL(process.env.VITE_DEV_SERVER_URL as string);
  } else {
    void win.loadFile(path.join(__dirname, '..', 'renderer', 'index.html'));
  }

  win.on('closed', () => {
    if (mainWindow === win) mainWindow = null;
  });

  return win;
}

async function startBackend(): Promise<void> {
  const found = findSidecar();

  if (!found) {
    backendAddress = null;
    setStatus({
      state: 'mock',
      mode: 'mock',
      message:
        'The Go backend (aisai-server) was not found next to the app or in ./bin. ' +
        'Running with mock data — build it with `go build -o bin/aisai-server ./cmd/aisai-server` ' +
        'or set AISAI_SERVER_BIN.',
    });
    return;
  }

  setStatus({
    state: 'starting',
    mode: 'live',
    message: `Starting the AISAI backend (${found.origin})…`,
  });

  // The API key of a hosted model service lives in the OS keychain; the
  // backend reads it from its environment and never writes it to disk.
  const savedKey = activeSecret();
  if (savedKey && !process.env.AISAI_BYOK_API_KEY) process.env.AISAI_BYOK_API_KEY = savedKey;

  try {
    sidecar = await startSidecar({
      serverPath: found.path,
      socketPath: defaultSocketPath(),
      onStderr: (text) => console.error('[aisai-server]', text.trimEnd()),
      onExit: (code, signal) => {
        if (mainWindow && mainWindow.isDestroyed()) return;
        setStatus({
          state: 'error',
          mode: 'mock',
          message: `The AISAI backend stopped unexpectedly (exit code ${code ?? signal ?? 'unknown'}).`,
        });
      },
    });

    const address = sidecar.listening.address;
    if (sidecar.listening.transport === 'unix') {
      backendAddress = { socketPath: address };
    } else {
      const [host, portText] = address.split(':');
      backendAddress = { host: host || '127.0.0.1', port: Number(portText ?? 0) };
    }

    connectEventStream();

    setStatus({
      state: 'ready',
      mode: 'live',
      message: `Connected to the AISAI backend over ${sidecar.listening.transport}.`,
    });
  } catch (err) {
    sidecar = null;
    backendAddress = null;
    setStatus({
      state: 'error',
      mode: 'mock',
      message: readableError(err),
    });
  }
}

function connectEventStream(): void {
  if (!backendAddress) return;
  wsClient = new WsClient({
    ...backendAddress,
    path: '/ws',
    onOpen: () => {
      /* the renderer subscribes to an audit once it has one */
    },
    onMessage: (text) => {
      let event: Event;
      try {
        event = JSON.parse(text) as Event;
      } catch {
        return;
      }
      if (mainWindow && !mainWindow.isDestroyed()) {
        mainWindow.webContents.send('aisai:event', event);
      }
    },
    onClose: (reason) => {
      console.error('[aisai] event stream closed:', reason);
    },
    onError: (err) => {
      console.error('[aisai] event stream error:', err.message);
    },
  });
  wsClient.connect();
}

/**
 * The API paths the renderer is allowed to reach.
 *
 * The renderer talks to the backend through this bridge, so it is the one
 * place where a request from the page can be checked against what the app
 * actually needs. Anything not on this list is refused rather than
 * forwarded: the audited package is untrusted input, and its contents end
 * up in the page.
 *
 * The pattern is a path prefix; the audit id is substituted by the caller.
 */
const ALLOWED_API_PREFIXES = [
  '/api/health',
  '/api/config',
  '/api/providers',
  '/api/models',
  '/api/packages',
  '/api/audits',
  '/api/advisories',
  '/api/chat',
] as const;

/** Reports whether an API path is one the renderer may call. */
function isAllowedApiPath(apiPath: string): boolean {
  if (!apiPath.startsWith('/api/')) return false;
  // A path traversal in the request would defeat the prefix check.
  if (apiPath.includes('..')) return false;
  // The query string is not part of the path: "/api/packages?name=flask"
  // has to be allowed by the "/api/packages" prefix, the same way
  // "/api/audits/a1/file?path=main.py" is allowed by "/api/audits".
  const path = apiPath.split('?')[0].split('#')[0];
  return ALLOWED_API_PREFIXES.some(
    (prefix) => path === prefix || path.startsWith(`${prefix}/`),
  );
}

function registerIpc(): void {
  ipcMain.on('aisai:status:get', (event) => {
    event.returnValue = status;
  });

  ipcMain.handle('aisai:http', async (_event, method: HttpMethod, apiPath: string, body: unknown) => {
    if (!isAllowedApiPath(apiPath)) {
      const message = `The API path ${apiPath} is not available to the renderer.`;
      console.error('[aisai]', message);
      return {
        status: 403,
        ok: false,
        body: { error: { code: 'forbidden', message } },
        errorCode: 'forbidden',
        errorMessage: message,
      } satisfies HttpResponse;
    }

    const refusal = folders.check(method, apiPath, body);
    if (refusal) {
      console.error('[aisai]', refusal);
      return {
        status: 403,
        ok: false,
        body: { error: { code: 'forbidden', message: refusal } },
        errorCode: 'forbidden',
        errorMessage: refusal,
      } satisfies HttpResponse;
    }

    if (!backendAddress) {
      const error: HttpResponse = {
        status: 503,
        ok: false,
        body: {
          error: {
            code: 'upstream_unavailable',
            message: 'The AISAI backend is not connected. The UI is showing mock data.',
          },
        },
        errorCode: 'upstream_unavailable',
        errorMessage: 'The AISAI backend is not connected. The UI is showing mock data.',
      };
      return error;
    }

    // Provider requests: a typed key is stored in the keychain, and a request
    // without one is completed with the saved key. The renderer never reads
    // a saved key.
    const providerPath = apiPath === '/api/providers' || apiPath === '/api/providers/test';
    let outgoing = body;
    let providerKind = '';
    if (method === 'POST' && providerPath && body && typeof body === 'object') {
      const request = body as { kind?: unknown; apiKey?: unknown };
      providerKind = typeof request.kind === 'string' ? request.kind : '';
      const typed = typeof request.apiKey === 'string' ? request.apiKey.trim() : '';
      if (!typed && providerKind) {
        const saved = readSecret(providerKind);
        if (saved) outgoing = { ...request, apiKey: saved };
      }
    }

    try {
      const result = await backendRequest({
        ...backendAddress,
        method,
        path: apiPath,
        body: outgoing ?? undefined,
        timeoutMs: 120000,
      });

      const contentType = result.headers['content-type'] ?? '';
      let parsed: unknown;
      if (contentType.includes('application/json')) {
        try {
          parsed = JSON.parse(result.body);
        } catch {
          parsed = undefined;
        }
      }

      const envelope = parsed as { error?: { code?: string; message?: string } } | undefined;
      const ok = result.status >= 200 && result.status < 300;

      // A provider the backend accepted becomes the active one; a key typed
      // for it is kept in the keychain.
      if (ok && apiPath === '/api/providers' && providerKind) {
        const typed = (body as { apiKey?: unknown }).apiKey;
        if (typeof typed === 'string' && typed.trim()) saveSecret(providerKind, typed.trim());
        setActiveProvider(providerKind);
      }

      return {
        status: result.status,
        ok,
        body: parsed,
        text: parsed === undefined ? result.body : undefined,
        contentType,
        errorCode: envelope?.error?.code,
        errorMessage: envelope?.error?.message,
      } satisfies HttpResponse;
    } catch (err) {
      const message = readableError(err);
      return {
        status: 0,
        ok: false,
        body: { error: { code: 'internal', message } },
        errorCode: 'internal',
        errorMessage: message,
      } satisfies HttpResponse;
    }
  });

  ipcMain.handle('aisai:secret:has', (_event, kind: unknown) =>
    typeof kind === 'string' ? { saved: hasSecret(kind), available: canStoreSecrets() } : { saved: false, available: false },
  );
  ipcMain.handle('aisai:secret:forget', (_event, kind: unknown) => {
    if (typeof kind === 'string') forgetSecret(kind);
  });

  ipcMain.handle('aisai:folder:choose', async () => {
    const options: OpenDialogOptions = {
      title: 'Choose a project folder to inspect',
      buttonLabel: 'Inspect',
      properties: ['openDirectory'],
    };
    const result =
      mainWindow && !mainWindow.isDestroyed()
        ? await dialog.showOpenDialog(mainWindow, options)
        : await dialog.showOpenDialog(options);
    if (result.canceled || result.filePaths.length === 0) return null;
    return folders.approve(result.filePaths[0]);
  });

  ipcMain.handle('aisai:folder:approve', (_event, folder: unknown) => folders.approve(folder));

  ipcMain.handle('aisai:disconnect', () => {
    wsClient?.close();
    wsClient = null;
  });

  ipcMain.handle('aisai:ws:send', (_event, message: ClientMessage) => {
    wsClient?.send(JSON.stringify(message));
  });
}

app.whenReady().then(() => {
  registerIpc();
  mainWindow = createWindow();
  void startBackend();
  configureAutoUpdate();
});

/** The releases page the app was published to. */
const RELEASES_URL = 'https://github.com/tlmcguire/aisai/releases/latest';

/** How often a running app looks for a new version. */
const UPDATE_INTERVAL_MS = 6 * 60 * 60 * 1000;

let updateStatus: UpdateStatus = { state: 'idle' };

function publishUpdate(status: UpdateStatus): void {
  updateStatus = status;
  if (mainWindow && !mainWindow.isDestroyed()) {
    mainWindow.webContents.send('aisai:update', status);
  }
}

/**
 * Keeps the installed app up to date without a reinstall.
 *
 * Every packaged build carries the update feed in its own `app-update.yml`
 * (the GitHub release of this project, written by electron-builder), so an
 * installed copy needs no configuration. `AISAI_UPDATE_FEED_URL` overrides
 * it for a private signed feed. The update downloads in the background and
 * is installed when the app quits, or at once from the "Restart to update"
 * button; the app never restarts by itself.
 *
 * electron-updater verifies the code signature of a downloaded macOS app and
 * the publisher name of a Windows installer, and refuses a mismatch. macOS
 * cannot swap in an unsigned app, so an unsigned build reports a `manual`
 * update that opens the release page instead.
 */
function configureAutoUpdate(): void {
  ipcMain.handle('aisai:update:check', () => checkForUpdate());
  ipcMain.handle('aisai:update:install', () => {
    if (updateStatus.state === 'ready') autoUpdater.quitAndInstall();
  });
  ipcMain.handle('aisai:update:page', () => shell.openExternal(updateStatus.url ?? RELEASES_URL));

  // An unpackaged checkout has nothing to update.
  if (!app.isPackaged) return;

  const feed = process.env.AISAI_UPDATE_FEED_URL?.trim();
  if (feed) autoUpdater.setFeedURL({ provider: 'generic', url: feed });
  autoUpdater.autoDownload = true;
  autoUpdater.autoInstallOnAppQuit = true;

  autoUpdater.on('checking-for-update', () => publishUpdate({ state: 'checking' }));
  autoUpdater.on('update-not-available', () => publishUpdate({ state: 'idle' }));
  autoUpdater.on('update-available', (info) =>
    publishUpdate({ state: 'downloading', version: info.version, percent: 0 }),
  );
  autoUpdater.on('download-progress', (progress) =>
    publishUpdate({ state: 'downloading', version: updateStatus.version, percent: Math.round(progress.percent) }),
  );
  autoUpdater.on('update-downloaded', (info) => publishUpdate({ state: 'ready', version: info.version }));
  autoUpdater.on('error', (error: Error) => {
    console.error('[aisai] updater:', error.message);
    // An update that is known but cannot be applied here (an unsigned macOS
    // build) is offered as a download; a failed check is just reported.
    if (updateStatus.version) {
      publishUpdate({
        state: 'manual',
        version: updateStatus.version,
        url: RELEASES_URL,
        message: 'This build cannot update itself. Download the new version.',
      });
    } else {
      publishUpdate({ state: 'error', message: error.message });
    }
  });

  void checkForUpdate();
  setInterval(() => void checkForUpdate(), UPDATE_INTERVAL_MS).unref();
}

async function checkForUpdate(): Promise<void> {
  if (!app.isPackaged) {
    publishUpdate({ state: 'idle', message: 'Updates are only checked in an installed build.' });
    return;
  }
  // Never interrupt a download that is already under way.
  if (updateStatus.state === 'downloading' || updateStatus.state === 'ready') return;
  try {
    await autoUpdater.checkForUpdates();
  } catch (error) {
    console.error('[aisai] update check failed:', error instanceof Error ? error.message : error);
  }
}

app.on('window-all-closed', () => {
  wsClient?.close();
  sidecar?.close();
  if (process.platform !== 'darwin') app.quit();
});

app.on('activate', () => {
  if (BrowserWindow.getAllWindows().length === 0) {
    mainWindow = createWindow();
  }
});
