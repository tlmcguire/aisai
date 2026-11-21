/**
 * Typed client over the AISAI backend.
 *
 * In live mode every call is proxied through `window.aisai` (Electron main
 * process → Unix socket → Go backend). In mock mode the same calls are served
 * by the in-renderer fixture backend, so the UI is fully usable before the Go
 * binary exists.
 */

import type { AisaiBridge, HttpMethod } from './bridge';
import { MockApi } from './mock';
import type { Event } from './types';

/** Error carrying the API error envelope (`error.code` / `error.message`). */
export class ApiError extends Error {
  readonly code: string;
  readonly status: number;

  constructor(message: string, code: string, status: number) {
    super(message);
    this.name = 'ApiError';
    this.code = code;
    this.status = status;
  }
}

export interface ApiClientOptions {
  bridge: AisaiBridge;
  mock?: MockApi;
}

export class ApiClient {
  private readonly bridge: AisaiBridge;
  private readonly mock: MockApi;

  constructor(options: ApiClientOptions) {
    this.bridge = options.bridge;
    this.mock = options.mock ?? new MockApi();
  }

  /** 'live' when the Go backend is connected, 'mock' otherwise. */
  get mode(): 'live' | 'mock' {
    return this.bridge.getMode();
  }

  /** True when the backend can serve requests (or the mock is in use). */
  get ready(): boolean {
    const state = this.bridge.getStatus().state;
    return state === 'ready' || state === 'mock';
  }

  async request<T>(method: HttpMethod, path: string, body?: unknown): Promise<T> {
    if (this.mode === 'mock') {
      return this.mock.request<T>(method, path, body);
    }
    const response = await this.bridge.http(method, path, body);
    if (!response.ok) {
      throw new ApiError(
        response.errorMessage ?? `The request failed (HTTP ${response.status}).`,
        response.errorCode ?? 'internal',
        response.status,
      );
    }
    return response.body as T;
  }

  /** Subscribe to server-pushed events. Returns an unsubscribe function. */
  onEvent(handler: (event: Event) => void): () => void {
    const offMock = this.mock.onEvent((event) => {
      if (this.mode === 'mock') handler(event);
    });
    this.bridge.connectEvents((event) => {
      if (this.mode === 'live') handler(event);
    });
    return () => {
      offMock();
    };
  }
}
