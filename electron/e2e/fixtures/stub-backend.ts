/**
 * The stub backend of the end-to-end test.
 *
 * The Electron main process starts the Go backend as a sidecar and talks to
 * it over a Unix socket. The real backend cannot be used here: creating an
 * audit downloads the package from PyPI, which would make the test depend on
 * the network.
 *
 * So the sidecar is this stub: it speaks the same protocol (one JSON line
 * on stdout, HTTP over a Unix socket, a WebSocket event stream) and serves
 * the same API, backed by the fixture package that is vendored in
 * e2e/fixtures/.
 *
 * The stub is bundled into a single CommonJS file by the Playwright global
 * setup, because the Electron main process starts a sidecar with
 * utilityProcess.fork, which runs a Node module.
 */

import crypto from 'node:crypto';
import fs from 'node:fs';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';

import { MockApi, MockApiError, type MockHttpMethod } from '../../src/api/mock';
import type { Event, FileContent, FileNode } from '../../src/api/types';

/**
 * The directory of the vendored fixture package.
 *
 * The stub is bundled into out/e2e/, so the fixtures are not next to it.
 * They are found by walking up until a directory that holds e2e/fixtures
 * shows up. The environment variable is an escape hatch.
 */
function findFixtureDir(): string {
  const fromEnv = process.env.AISAI_E2E_FIXTURE_DIR;
  if (fromEnv) {
    return fromEnv;
  }
  let dir = __dirname;
  for (let i = 0; i < 6; i += 1) {
    const candidate = path.join(dir, 'e2e', 'fixtures');
    if (fs.existsSync(candidate)) {
      return candidate;
    }
    const parent = path.dirname(dir);
    if (parent === dir) {
      break;
    }
    dir = parent;
  }
  throw new Error(`the vendored fixture package was not found above ${__dirname}`);
}

const FIXTURE_DIR = findFixtureDir();

/** The mock API holds the fixtures of the API. */
const api = new MockApi();

/* ------------------------------------------------------------------ */
/* The vendored fixture package                                        */
/* ------------------------------------------------------------------ */

interface FixtureFile {
  path: string;
  language: string;
  lines: string[];
}

/** Reads every file of the vendored package. */
function readFixtureFiles(): Map<string, FixtureFile> {
  const files = new Map<string, FixtureFile>();
  const walk = (dir: string, prefix: string): void => {
    for (const entry of fs.readdirSync(dir, { withFileTypes: true }).sort((a, b) => a.name.localeCompare(b.name))) {
      const abs = path.join(dir, entry.name);
      const rel = prefix ? `${prefix}/${entry.name}` : entry.name;
      if (entry.isDirectory()) {
        walk(abs, rel);
        continue;
      }
      const content = fs.readFileSync(abs, 'utf8');
      files.set(rel, {
        path: rel,
        language: rel.endsWith('.py') ? 'python' : rel.endsWith('.md') ? 'markdown' : 'text',
        lines: content.split('\n'),
      });
    }
  };
  walk(FIXTURE_DIR, '');
  return files;
}

const fixtureFiles = readFixtureFiles();

/** Builds the file tree of the vendored package. */
function buildTree(): FileNode {
  const root: FileNode = {
    name: 'flask-2.0.0',
    path: '',
    type: 'dir',
    children: [],
  };
  for (const file of fixtureFiles.values()) {
    const parts = file.path.split('/');
    let node = root;
    for (let i = 0; i < parts.length - 1; i += 1) {
      const name = parts[i] as string;
      let child = node.children?.find((c) => c.name === name && c.type === 'dir');
      if (!child) {
        child = { name, path: parts.slice(0, i + 1).join('/'), type: 'dir', children: [] };
        node.children = node.children ?? [];
        node.children.push(child);
      }
      node = child;
    }
    node.children = node.children ?? [];
    node.children.push({
      name: parts[parts.length - 1] as string,
      path: file.path,
      type: 'file',
      size: file.lines.join('\n').length,
    });
  }
  return root;
}

const fixtureTree = buildTree();

/* ------------------------------------------------------------------ */
/* The HTTP server                                                     */
/* ------------------------------------------------------------------ */

/** Reads the body of a request. */
function readBody(req: http.IncomingMessage): Promise<unknown> {
  return new Promise((resolve) => {
    const chunks: Buffer[] = [];
    req.on('data', (chunk: Buffer) => chunks.push(chunk));
    req.on('end', () => {
      const raw = Buffer.concat(chunks).toString('utf8');
      if (!raw) {
        resolve(undefined);
        return;
      }
      try {
        resolve(JSON.parse(raw));
      } catch {
        resolve(undefined);
      }
    });
  });
}

/** Serves one request. */
async function handle(req: http.IncomingMessage, res: http.ServerResponse): Promise<void> {
  const url = new URL(req.url ?? '/', 'http://localhost');
  const method = (req.method ?? 'GET').toUpperCase();
  const body = await readBody(req);

  // The vendored package replaces the fixture of the mock, so the code pane
  // shows files that really exist in the repository.
  const auditFiles = /^\/api\/audits\/[^/]+\/files$/.test(url.pathname);
  const auditFile = /^\/api\/audits\/[^/]+\/file$/.test(url.pathname);

  if (method === 'GET' && auditFiles) {
    send(res, 200, { tree: fixtureTree });
    return;
  }
  if (method === 'GET' && auditFile) {
    const wanted = url.searchParams.get('path') ?? '';
    const file = fixtureFiles.get(wanted);
    if (!file) {
      send(res, 404, { error: { code: 'not_found', message: `file not found: ${wanted}` } });
      return;
    }
    const content: FileContent = { path: file.path, language: file.language, lines: file.lines, findings: [] };
    send(res, 200, { file: content });
    return;
  }

  try {
    const payload = await api.request(method as MockHttpMethod, url.pathname + url.search, body);
    send(res, 200, payload);
  } catch (err) {
    if (err instanceof MockApiError) {
      send(res, err.status, { error: { code: err.code, message: err.message } });
      return;
    }
    send(res, 500, { error: { code: 'internal', message: String(err) } });
  }
}

/** Sends a JSON response. */
function send(res: http.ServerResponse, status: number, payload: unknown): void {
  const body = JSON.stringify(payload);
  res.writeHead(status, {
    'Content-Type': 'application/json',
    'Content-Length': Buffer.byteLength(body),
  });
  res.end(body);
}

/* ------------------------------------------------------------------ */
/* The WebSocket event stream                                          */
/* ------------------------------------------------------------------ */

/** The GUID of the WebSocket handshake (RFC 6455). */
const WS_GUID = '258EAFA5-E914-47DA-95CA-5AB0DC85B11F';

/** Encodes a server-to-client text frame. */
function encodeTextFrame(text: string): Buffer {
  const payload = Buffer.from(text, 'utf8');
  if (payload.length < 126) {
    return Buffer.concat([Buffer.from([0x81, payload.length]), payload]);
  }
  const header = Buffer.alloc(4);
  header[0] = 0x81;
  header[1] = 126;
  header.writeUInt16BE(payload.length, 2);
  return Buffer.concat([header, payload]);
}

/** Sends one event to a connected client. */
function sendEvent(socket: http.ServerResponse | null, event: Event): void {
  if (!socket || socket.writableEnded) return;
  socket.write(encodeTextFrame(JSON.stringify(event)));
}

/* ------------------------------------------------------------------ */
/* The sidecar                                                         */
/* ------------------------------------------------------------------ */

/** The socket path the main process asked for. */
function socketPathFrom(argv: string[]): string {
  const index = argv.indexOf('--socket');
  if (index >= 0 && argv[index + 1]) {
    return argv[index + 1] as string;
  }
  return path.join(os.tmpdir(), 'aisai-stub.sock');
}

function main(): void {
  const socketPath = socketPathFrom(process.argv.slice(2));

  const server = http.createServer((req, res) => {
    void handle(req, res);
  });

  // The event stream: the main process connects with a WebSocket handshake
  // on the same socket.
  server.on('upgrade', (req, socket) => {
    const key = req.headers['sec-websocket-key'];
    const accept = crypto
      .createHash('sha1')
      .update(`${key ?? ''}${WS_GUID}`)
      .digest('base64');
    socket.write(
      [
        'HTTP/1.1 101 Switching Protocols',
        'Upgrade: websocket',
        'Connection: Upgrade',
        `Sec-WebSocket-Accept: ${accept}`,
        '',
        '',
      ].join('\r\n'),
    );

    const client = socket as unknown as http.ServerResponse;
    api.onEvent((event) => sendEvent(client, event));
  });

  try {
    fs.unlinkSync(socketPath);
  } catch {
    /* there was no stale socket */
  }

  server.listen(socketPath, () => {
    // The main process waits for exactly this line.
    process.stdout.write(
      `${JSON.stringify({ type: 'listening', transport: 'unix', address: socketPath })}\n`,
    );
  });

  const shutdown = (): void => {
    server.close();
    try {
      fs.unlinkSync(socketPath);
    } catch {
      /* the socket is already gone */
    }
    process.exit(0);
  };
  process.on('SIGTERM', shutdown);
  process.on('SIGINT', shutdown);
}

main();
