/**
 * Minimal RFC 6455 WebSocket client.
 *
 * Node's built-in `WebSocket` cannot dial a Unix domain socket, and the
 * AISAI backend listens on one, so we speak the protocol directly over a
 * `net.Socket`. Only what AISAI needs is implemented: text frames, ping/pong,
 * close. No extensions, no fragmentation (the backend does not use them).
 */

import crypto from 'node:crypto';
import net from 'node:net';

export interface WsClientOptions {
  /** Unix socket path. Takes precedence over host/port. */
  socketPath?: string;
  host?: string;
  port?: number;
  /** Request target, e.g. '/ws'. */
  path?: string;
  onOpen: () => void;
  onMessage: (text: string) => void;
  onClose: (reason: string) => void;
  onError: (err: Error) => void;
}

const OPCODE_CONTINUATION = 0x0;
const OPCODE_TEXT = 0x1;
const OPCODE_BINARY = 0x2;
const OPCODE_CLOSE = 0x8;
const OPCODE_PING = 0x9;
const OPCODE_PONG = 0xa;

export class WsClient {
  private socket: net.Socket | null = null;
  private buffer: Buffer = Buffer.alloc(0);
  private handshaked = false;
  private closed = false;

  constructor(private readonly opts: WsClientOptions) {}

  connect(): void {
    const socket = this.opts.socketPath
      ? net.connect({ path: this.opts.socketPath })
      : net.connect({ host: this.opts.host ?? '127.0.0.1', port: this.opts.port ?? 0 });

    this.socket = socket;
    socket.setNoDelay(true);
    socket.on('connect', () => this.sendHandshake());
    socket.on('data', (chunk: Buffer) => this.onData(chunk));
    socket.on('error', (err: Error) => {
      if (!this.closed) this.opts.onError(err);
    });
    socket.on('close', () => {
      if (!this.closed) {
        this.closed = true;
        this.opts.onClose('connection closed');
      }
    });
  }

  /** Send a text frame. */
  send(text: string): void {
    if (!this.socket || !this.handshaked || this.closed) return;
    const payload = Buffer.from(text, 'utf8');
    const length = payload.length;
    let header: Buffer;
    if (length < 126) {
      header = Buffer.alloc(2);
      header[1] = 0x80 | length;
    } else if (length < 65536) {
      header = Buffer.alloc(4);
      header[1] = 0x80 | 126;
      header.writeUInt16BE(length, 2);
    } else {
      header = Buffer.alloc(10);
      header[1] = 0x80 | 127;
      header.writeBigUInt64BE(BigInt(length), 2);
    }
    header[0] = 0x80 | OPCODE_TEXT; // FIN + text

    const mask = crypto.randomBytes(4);
    const masked = Buffer.from(payload);
    for (let i = 0; i < masked.length; i += 1) {
      masked[i] ^= mask[i % 4];
    }
    this.socket.write(Buffer.concat([header, mask, masked]));
  }

  close(): void {
    if (this.closed) return;
    this.closed = true;
    if (this.socket && this.handshaked) {
      try {
        const header = Buffer.alloc(2);
        header[0] = 0x80 | OPCODE_CLOSE;
        header[1] = 0x80; // masked, zero-length payload
        const mask = crypto.randomBytes(4);
        this.socket.write(Buffer.concat([header, mask]));
      } catch {
        /* the socket may already be gone */
      }
    }
    this.socket?.destroy();
    this.socket = null;
  }

  private sendHandshake(): void {
    const key = crypto.randomBytes(16).toString('base64');
    const request = [
      `GET ${this.opts.path ?? '/ws'} HTTP/1.1`,
      'Host: localhost',
      'Upgrade: websocket',
      'Connection: Upgrade',
      `Sec-WebSocket-Key: ${key}`,
      'Sec-WebSocket-Version: 13',
      '',
      '',
    ].join('\r\n');
    this.socket?.write(request);
  }

  private onData(chunk: Buffer): void {
    this.buffer = Buffer.concat([this.buffer, chunk]);
    if (!this.handshaked) {
      const headerEnd = this.buffer.indexOf('\r\n\r\n');
      if (headerEnd < 0) return;
      const header = this.buffer.subarray(0, headerEnd).toString('utf8');
      this.buffer = this.buffer.subarray(headerEnd + 4);
      const statusLine = header.split('\r\n')[0] ?? '';
      if (!/ 101 /.test(statusLine)) {
        this.opts.onError(new Error(`websocket handshake failed: ${statusLine.trim()}`));
        this.close();
        return;
      }
      this.handshaked = true;
      this.opts.onOpen();
    }
    this.drainFrames();
  }

  private drainFrames(): void {
    for (;;) {
      const buf = this.buffer;
      if (buf.length < 2) return;
      const opcode = buf[0] & 0x0f;
      const masked = (buf[1] & 0x80) !== 0;
      let length = buf[1] & 0x7f;
      let offset = 2;

      if (length === 126) {
        if (buf.length < 4) return;
        length = buf.readUInt16BE(2);
        offset = 4;
      } else if (length === 127) {
        if (buf.length < 10) return;
        length = Number(buf.readBigUInt64BE(2));
        offset = 10;
      }

      let maskKey: Buffer | null = null;
      if (masked) {
        if (buf.length < offset + 4) return;
        maskKey = buf.subarray(offset, offset + 4);
        offset += 4;
      }
      if (buf.length < offset + length) return;

      const payload = Buffer.from(buf.subarray(offset, offset + length));
      if (maskKey) {
        for (let i = 0; i < payload.length; i += 1) {
          payload[i] ^= maskKey[i % 4];
        }
      }
      this.buffer = buf.subarray(offset + length);
      this.handleFrame(opcode, payload);
      if (this.closed) return;
    }
  }

  private handleFrame(opcode: number, payload: Buffer): void {
    switch (opcode) {
      case OPCODE_TEXT:
        this.opts.onMessage(payload.toString('utf8'));
        break;
      case OPCODE_BINARY:
        this.opts.onMessage(payload.toString('utf8'));
        break;
      case OPCODE_PING:
        this.sendPong(payload);
        break;
      case OPCODE_PONG:
        break;
      case OPCODE_CLOSE:
        this.closed = true;
        this.socket?.destroy();
        this.opts.onClose('server closed the connection');
        break;
      case OPCODE_CONTINUATION:
        break;
      default:
        break;
    }
  }

  private sendPong(payload: Buffer): void {
    if (!this.socket || this.closed) return;
    const header = Buffer.alloc(2);
    header[0] = 0x80 | OPCODE_PONG;
    header[1] = 0x80 | payload.length;
    const mask = crypto.randomBytes(4);
    const masked = Buffer.from(payload);
    for (let i = 0; i < masked.length; i += 1) {
      masked[i] ^= mask[i % 4];
    }
    this.socket.write(Buffer.concat([header, mask, masked]));
  }
}
