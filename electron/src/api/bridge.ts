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

export interface AisaiBridge {
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
}

declare global {
  interface Window {
    aisai: AisaiBridge;
  }
}
