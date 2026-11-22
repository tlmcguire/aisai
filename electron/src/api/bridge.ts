/**
 * The `window.aisai` bridge contract.
 *
 * This is the ONLY surface the renderer gets. It deliberately contains no
 * socket paths, no filesystem handles and no API keys: HTTP traffic and
 * server-pushed events are proxied through the Electron main process.
 */

import type { Event } from './types';

export type HttpMethod = 'GET' | 'POST' | 'PUT' | 'DELETE' | 'PATCH';

/** Normalised HTTP response handed back to the renderer. */
export interface HttpResponse {
  status: number;
  ok: boolean;
  /** Parsed JSON body, or `undefined` for non-JSON responses. */
  body: unknown;
  /** Raw text body (export endpoints, non-JSON errors). */
  text?: string;
  contentType?: string;
  /** `error.code` from the API error envelope, when the request failed. */
  errorCode?: string;
  /** `error.message` from the API error envelope, when the request failed. */
  errorMessage?: string;
}

/** Lifecycle of the Go sidecar. */
export type BackendState = 'starting' | 'ready' | 'mock' | 'error';

export interface BackendStatus {
  state: BackendState;
  /** 'live' once the Go sidecar is connected, 'mock' while using fixtures. */
  mode: 'live' | 'mock';
  /** Human readable, already sanitised for display. */
  message: string;
  version?: string;
}

/** Client → server WebSocket messages (docs/API.md). */
export interface ClientMessage {
  type: 'subscribe' | 'unsubscribe' | 'ping';
  auditId?: string;
}

/** Progress of the self-updater, reported by the main process. */
export interface UpdateStatus {
  state: 'idle' | 'checking' | 'available' | 'downloading' | 'ready' | 'manual' | 'error';
  /** The version on offer, once one is known. */
  version?: string;
  /** Download progress, 0–100, while downloading. */
  percent?: number;
  /** A human readable note: the error, or where to download by hand. */
  message?: string;
  /** The release page, for a platform that cannot update itself. */
  url?: string;
}

/** What a click on an application menu item asks the window to do. */
export type MenuCommand =
  | 'open-folder'
  | 'settings'
  | 'about'
  | 'save'
  | 'undo'
  | 'redo'
  | 'export-pdf'
  | 'export-html'
  | 'shortcuts'
  | 'tutorials';

export interface AisaiBridge {
  /** Subscribe to updater status changes. Returns an unsubscribe function. */
  onUpdateStatus(cb: (status: UpdateStatus) => void): () => void;
  /** Check for a new version now. */
  checkForUpdates(): Promise<void>;
  /** Quit and install the downloaded update. */
  installUpdate(): Promise<void>;
  /** Open the release page in the browser. */
  openReleasePage(): Promise<void>;
  /** Subscribe to native menu commands (File > Open Folder, Settings, About). */
  onMenuCommand(cb: (command: MenuCommand) => void): () => void;
  /** Shows the user's key bindings in the application menu (accelerators by shortcut id). */
  setMenuShortcuts(shortcuts: Record<string, string>): Promise<void>;
  /** Open a web page in the system browser. Only http and https addresses. */
  openExternal(url: string): Promise<void>;
  /** Renders the HTML report as a PDF and saves it where the user picks. */
  exportPdf(html: string, name: string): Promise<{ saved: boolean; path?: string; bytes?: number }>;
  /** Shows a PDF saved by exportPdf in the file manager. */
  showInFolder(file: string): Promise<void>;
  /** Current transport mode. */
  getMode(): 'live' | 'mock';
  /** Current backend status snapshot. */
  getStatus(): BackendStatus;
  /** Perform an HTTP request against the AISAI backend. */
  http(method: HttpMethod, path: string, body?: unknown): Promise<HttpResponse>;
  /** Subscribe to server-pushed events (audit/scan/chat). */
  connectEvents(cb: (event: Event) => void): void;
  /** Drop every event subscription and close the event channel. */
  disconnect(): void;
  /** Subscribe to backend status changes. Returns an unsubscribe function. */
  onBackendStatus(cb: (status: BackendStatus) => void): () => void;
  /** Send a client → server WebSocket message (subscribe/unsubscribe/ping). */
  send(message: ClientMessage): void;
  /**
   * Open the native folder dialog. Resolves to the folder the user picked, or
   * `null` when the dialog was cancelled. Only a folder chosen this way (or
   * dropped on the window) can be audited.
   */
  chooseFolder(): Promise<string | null>;
  /**
   * Resolve a folder that was dropped on the window. Resolves to the folder's
   * path, or `null` when the dropped item is not a folder.
   */
  folderFromDrop(file: File): Promise<string | null>;
  /**
   * Whether an API key for a hosted provider is saved in the OS keychain,
   * and whether the keychain is usable at all. The key itself is never
   * returned.
   */
  providerKeyStatus(kind: string): Promise<{ saved: boolean; available: boolean }>;
  /** Remove a saved API key from the keychain. */
  forgetProviderKey(kind: string): Promise<void>;
}

declare global {
  const __APP_VERSION__: string;
  interface Window {
    aisai: AisaiBridge;
  }
}
