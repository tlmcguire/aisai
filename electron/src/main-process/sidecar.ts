/**
 * Sidecar supervision.
 *
 * The Go backend prints exactly one JSON line on stdout when it is ready:
 *
 *   {"type":"listening","transport":"unix","address":"/tmp/aisai-501.sock"}
 *
 * We start it, wait for that line, and hand the address back to the main
 * process. Everything else on stdout/stderr is treated as a diagnostic.
 */

import { spawn, type ChildProcess } from 'node:child_process';
import { utilityProcess, type UtilityProcess } from 'electron';

export interface ListeningInfo {
  transport: string;
  address: string;
}

export interface SidecarOptions {
  serverPath: string;
  socketPath: string;
  timeoutMs?: number;
  onStderr?: (text: string) => void;
  /** Called when the backend dies after it had started. */
  onExit?: (code: number | null, signal: string | null) => void;
}

export interface SidecarHandle {
  listening: ListeningInfo;
  close: () => void;
}

class StartupError extends Error {}

function parseListening(line: string): ListeningInfo | null {
  const trimmed = line.trim();
  if (!trimmed.startsWith('{')) return null;
  try {
    const parsed = JSON.parse(trimmed) as { type?: string; transport?: string; address?: string };
    if (parsed.type !== 'listening' || typeof parsed.address !== 'string') return null;
    return { transport: parsed.transport ?? 'unix', address: parsed.address };
  } catch {
    return null;
  }
}

/**
 * Start the sidecar with `utilityProcess.fork`, per the process model in
 * PLAN_rewrite_electron_go.md §4.1.
 *
 * `utilityProcess.fork` runs its target as a Node module, so a compiled Go
 * binary cannot be launched that way: Node tries to parse the executable and
 * fails with a syntax error. We therefore only use the fork when the target
 * really is a JavaScript file, and run anything else with
 * `child_process.spawn`, which starts the binary natively.
 */
export async function startSidecar(opts: SidecarOptions): Promise<SidecarHandle> {
  const timeoutMs = opts.timeoutMs ?? 15000;

  if (isJavaScript(opts.serverPath)) {
    try {
      return await startViaUtilityProcess(opts, timeoutMs);
    } catch (err) {
      const forkError = err instanceof Error ? err.message : String(err);
      try {
        return await startViaSpawn(opts, timeoutMs);
      } catch (spawnErr) {
        const spawnMessage = spawnErr instanceof Error ? spawnErr.message : String(spawnErr);
        throw new StartupError(
          `could not start the AISAI backend: ${spawnMessage} (utilityProcess.fork: ${forkError})`,
        );
      }
    }
  }

  // A native binary: spawn it directly.
  try {
    return await startViaSpawn(opts, timeoutMs);
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    throw new StartupError(`could not start the AISAI backend: ${message}`);
  }
}

/** isJavaScript reports whether the sidecar target is a script Node can run. */
function isJavaScript(target: string): boolean {
  return /\.(js|mjs|cjs)$/i.test(target);
}

function startViaUtilityProcess(
  opts: SidecarOptions,
  timeoutMs: number,
): Promise<SidecarHandle> {
  return new Promise<SidecarHandle>((resolve, reject) => {
    let settled = false;
    const stderrTail: string[] = [];
    let child: UtilityProcess | null = null;
    let closing = false; // set by close(): that exit is expected, not a crash

    const timer = setTimeout(() => {
      if (settled) return;
      settled = true;
      try {
        child?.kill();
      } catch {
        /* ignore */
      }
      reject(new StartupError(`timed out after ${timeoutMs}ms waiting for the backend to start`));
    }, timeoutMs);

    const finish = (err: Error | null, handle?: SidecarHandle) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      if (err) reject(err);
      else if (handle) resolve(handle);
    };

    try {
      child = utilityProcess.fork(opts.serverPath, ['--socket', opts.socketPath], {
        stdio: 'pipe',
        serviceName: 'aisai-server',
      });
    } catch (err) {
      clearTimeout(timer);
      settled = true;
      reject(new StartupError(err instanceof Error ? err.message : String(err)));
      return;
    }

    child.stdout?.on('data', (chunk: Buffer) => {
      for (const line of chunk.toString('utf8').split('\n')) {
        const info = parseListening(line);
        if (!info) continue;
        finish(null, {
          listening: info,
          close: () => {
            closing = true;
            try {
              child?.kill();
            } catch {
              /* ignore */
            }
          },
        });
        return;
      }
    });

    child.stderr?.on('data', (chunk: Buffer) => {
      const text = chunk.toString('utf8');
      stderrTail.push(text);
      opts.onStderr?.(text);
    });

    child.on('exit', (code) => {
      if (!closing) opts.onExit?.(code, null);
      finish(
        new StartupError(
          `backend exited with code ${code} before it was ready${stderrTail.length ? `: ${stderrTail.join('').trim()}` : ''}`,
        ),
      );
    });
  });
}

function startViaSpawn(opts: SidecarOptions, timeoutMs: number): Promise<SidecarHandle> {
  return new Promise<SidecarHandle>((resolve, reject) => {
    let settled = false;
    const stderrTail: string[] = [];
    let child: ChildProcess | null = null;
    let closing = false; // set by close(): that exit is expected, not a crash

    const timer = setTimeout(() => {
      if (settled) return;
      settled = true;
      try {
        child?.kill();
      } catch {
        /* ignore */
      }
      reject(new StartupError(`timed out after ${timeoutMs}ms waiting for the backend to start`));
    }, timeoutMs);

    const finish = (err: Error | null, handle?: SidecarHandle) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      if (err) reject(err);
      else if (handle) resolve(handle);
    };

    try {
      child = spawn(opts.serverPath, ['--socket', opts.socketPath, '--exit-with-parent'], {
        // stdin stays an open pipe: the server exits when it closes, so it
        // cannot outlive this process.
        stdio: ['pipe', 'pipe', 'pipe'],
      });
    } catch (err) {
      clearTimeout(timer);
      settled = true;
      reject(new StartupError(err instanceof Error ? err.message : String(err)));
      return;
    }

    child.stdout?.on('data', (chunk: Buffer) => {
      for (const line of chunk.toString('utf8').split('\n')) {
        const info = parseListening(line);
        if (!info) continue;
        finish(null, {
          listening: info,
          close: () => {
            closing = true;
            try {
              child?.kill();
              // A server that ignores SIGTERM must not be left running.
              const stuck = setTimeout(() => {
                try {
                  child?.kill('SIGKILL');
                } catch {
                  /* ignore */
                }
              }, 3000);
              stuck.unref();
              child?.once('exit', () => clearTimeout(stuck));
            } catch {
              /* ignore */
            }
          },
        });
        return;
      }
    });

    child.stderr?.on('data', (chunk: Buffer) => {
      const text = chunk.toString('utf8');
      stderrTail.push(text);
      opts.onStderr?.(text);
    });

    child.on('error', (err) => {
      finish(new StartupError(err.message));
    });

    child.on('exit', (code, signal) => {
      if (!closing) opts.onExit?.(code, signal);
      finish(
        new StartupError(
          `backend exited with code ${code} before it was ready${stderrTail.length ? `: ${stderrTail.join('').trim()}` : ''}`,
        ),
      );
    });
  });
}
