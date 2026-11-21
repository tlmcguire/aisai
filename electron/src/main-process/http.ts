/**
 * HTTP client for the AISAI backend, spoken over a Unix domain socket (or a
 * loopback TCP fallback). Used by the Electron main process only — the
 * renderer never sees a socket.
 */

import http from 'node:http';

export interface BackendRequest {
  /** Unix socket path. Takes precedence over host/port. */
  socketPath?: string;
  host?: string;
  port?: number;
  method: string;
  path: string;
  body?: unknown;
  headers?: Record<string, string>;
  timeoutMs?: number;
}

export interface BackendResponse {
  status: number;
  headers: http.IncomingHttpHeaders;
  body: string;
}

/** Perform one HTTP request against the backend. */
export function backendRequest(req: BackendRequest): Promise<BackendResponse> {
  return new Promise((resolve, reject) => {
    const payload =
      req.body === undefined || req.body === null
        ? undefined
        : Buffer.from(JSON.stringify(req.body), 'utf8');

    const headers: Record<string, string> = {
      Accept: 'application/json',
      ...(req.headers ?? {}),
    };
    if (payload) {
      headers['Content-Type'] = 'application/json';
      headers['Content-Length'] = String(payload.byteLength);
    }

    const options: http.RequestOptions = {
      method: req.method,
      path: req.path,
      headers,
    };
    if (req.socketPath) {
      options.socketPath = req.socketPath;
    } else {
      options.host = req.host ?? '127.0.0.1';
      options.port = req.port ?? 0;
    }

    const client = http.request(options, (res) => {
      const chunks: Buffer[] = [];
      res.on('data', (chunk: Buffer) => chunks.push(chunk));
      res.on('end', () => {
        resolve({
          status: res.statusCode ?? 0,
          headers: res.headers,
          body: Buffer.concat(chunks).toString('utf8'),
        });
      });
    });

    client.on('error', (err) => reject(err));
    if (req.timeoutMs) {
      client.setTimeout(req.timeoutMs, () => {
        client.destroy(new Error(`request timed out after ${req.timeoutMs}ms`));
      });
    }
    if (payload) client.write(payload);
    client.end();
  });
}
