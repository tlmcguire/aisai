/**
 * T8 — the security posture of the desktop app.
 *
 * The threat model (docs/THREAT_MODEL.md, threat T8) requires:
 *
 *   - `contextIsolation: true`, `nodeIntegration: false`, `sandbox: true`
 *     and `webSecurity: true` in the BrowserWindow's webPreferences;
 *   - a `setWindowOpenHandler` that denies every window open;
 *   - a `will-navigate` handler that prevents any navigation away from the
 *     current document;
 *   - a `will-attach-webview` handler that prevents every webview attach;
 *   - an IPC path allowlist that only lets the renderer reach the API
 *     prefixes the app needs, and that refuses a path containing "..";
 *   - a preload that exposes exactly one object, `window.aisai`, with the
 *     nine documented members.
 *
 * The values live inside `createWindow()` in electron/main.ts and inside the
 * module scope of electron/preload.ts, and the main process imports
 * `electron`, so they cannot be imported into a vitest process. The test
 * therefore reads the two real source files and *evaluates* the real literals
 * out of them: the webPreferences object and the allowlist array are turned
 * into live values, and the allowlist predicate is executed against them.
 *
 * That is more than a text match: if `sandbox: true` is deleted, the
 * evaluated object simply has no `sandbox` key and the assertion fails; if
 * the traversal check is removed from `isAllowedApiPath`, the evaluated
 * predicate starts accepting "/api/audits/../../etc/passwd" and the
 * behavioural assertion fails. The textual assertions are limited to the
 * event handlers, which are statements rather than values.
 */

import { describe, expect, it } from 'vitest';

import mainSource from '../main.ts?raw';
import preloadSource from '../preload.ts?raw';

/** The members of the bridge the threat model documents. */
const EXPECTED_BRIDGE_MEMBERS = [
  'checkForUpdates',
  'chooseFolder',
  'connectEvents',
  'disconnect',
  'exportPdf',
  'folderFromDrop',
  'forgetProviderKey',
  'getMode',
  'getStatus',
  'http',
  'installUpdate',
  'onBackendStatus',
  'onMenuCommand',
  'onUpdateStatus',
  'openExternal',
  'openReleasePage',
  'providerKeyStatus',
  'send',
  'setLocale',
  'setMenuShortcuts',
  'showInFolder',
];

/** The API prefixes the renderer is allowed to reach. */
const EXPECTED_API_PREFIXES = [
  '/api/health',
  '/api/config',
  '/api/providers',
  '/api/models',
  '/api/packages',
  '/api/audits',
  '/api/advisories',
  '/api/chat',
  '/api/terminals',
  '/api/debug',
];

/**
 * Returns the balanced `{ ... }` literal that follows `marker`.
 *
 * The scan counts braces so that a nested object does not end the literal
 * early.
 */
function extractObjectLiteral(source: string, marker: string): string {
  const start = source.indexOf(marker);
  if (start < 0) {
    throw new Error(`the source does not contain ${marker}`);
  }
  const open = source.indexOf('{', start);
  if (open < 0) {
    throw new Error(`${marker} is not followed by an object literal`);
  }
  let depth = 0;
  for (let i = open; i < source.length; i += 1) {
    const ch = source[i];
    if (ch === '{') depth += 1;
    else if (ch === '}') {
      depth -= 1;
      if (depth === 0) {
        return source.slice(open, i + 1);
      }
    }
  }
  throw new Error(`the object literal after ${marker} is not balanced`);
}

/**
 * Returns the balanced `[ ... ]` literal that follows `marker`.
 */
function extractArrayLiteral(source: string, marker: string): string {
  const start = source.indexOf(marker);
  if (start < 0) {
    throw new Error(`the source does not contain ${marker}`);
  }
  const open = source.indexOf('[', start);
  if (open < 0) {
    throw new Error(`${marker} is not followed by an array literal`);
  }
  let depth = 0;
  for (let i = open; i < source.length; i += 1) {
    const ch = source[i];
    if (ch === '[') depth += 1;
    else if (ch === ']') {
      depth -= 1;
      if (depth === 0) {
        return source.slice(open, i + 1);
      }
    }
  }
  throw new Error(`the array literal after ${marker} is not balanced`);
}

/**
 * Evaluates a literal that was extracted from the source.
 *
 * `path` and `__dirname` are provided because the webPreferences literal
 * refers to them.
 */
function evaluateLiteral(literal: string): unknown {
  const factory = new Function('path', '__dirname', `return (${literal});`);
  return factory(nodePath, '/electron');
}

/** The node:path module, imported lazily to keep the import list short. */
import * as nodePath from 'node:path';

describe('T8 — the BrowserWindow web preferences', () => {
  const webPreferences = evaluateLiteral(
    extractObjectLiteral(mainSource, 'webPreferences:'),
  ) as Record<string, unknown>;

  it('runs the renderer in a sandbox', () => {
    expect(webPreferences.sandbox).toBe(true);
  });

  it('isolates the context from the preload', () => {
    expect(webPreferences.contextIsolation).toBe(true);
  });

  it('does not give the renderer node', () => {
    expect(webPreferences.nodeIntegration).toBe(false);
  });

  it('keeps the web security policy on', () => {
    expect(webPreferences.webSecurity).toBe(true);
  });

  it('loads the preload script', () => {
    expect(typeof webPreferences.preload).toBe('string');
  });
});

describe('T8 — the navigation and window-open guards', () => {
  it('denies every window open', () => {
    expect(mainSource).toMatch(/setWindowOpenHandler\s*\(\s*\(\)\s*=>\s*\(\{\s*action:\s*'deny'\s*\}\)\s*\)/);
  });

  it('prevents a navigation away from the current document', () => {
    const handler = /on\(\s*'will-navigate'[\s\S]*?\n\s*\}\);/.exec(mainSource);
    expect(handler).not.toBeNull();
    expect(handler?.[0]).toContain('event.preventDefault()');
  });

  it('prevents every webview attach', () => {
    const handler = /on\(\s*'will-attach-webview'[\s\S]*?\n\s*\}\);/.exec(mainSource);
    expect(handler).not.toBeNull();
    expect(handler?.[0]).toContain('event.preventDefault()');
  });
});

describe('T8 — the IPC path allowlist', () => {
  const prefixes = evaluateLiteral(
    extractArrayLiteral(mainSource, 'const ALLOWED_API_PREFIXES ='),
  ) as string[];

  /**
   * The real predicate, lifted out of the source and executed with the real
   * allowlist. The TypeScript annotations are stripped first.
   */
  const predicate = ((): ((apiPath: string) => boolean) => {
    const match = /function isAllowedApiPath\(apiPath: string\): boolean \{([\s\S]*?)\n\}/.exec(
      mainSource,
    );
    if (!match) {
      throw new Error('isAllowedApiPath was not found in electron/main.ts');
    }
    const body = match[1];
    const source = `const ALLOWED_API_PREFIXES = ${extractArrayLiteral(
      mainSource,
      'const ALLOWED_API_PREFIXES =',
    )};
function isAllowedApiPath(apiPath) {${body}
}
return isAllowedApiPath;`;
    return new Function(source)() as (apiPath: string) => boolean;
  })();

  it('lists exactly the documented API prefixes', () => {
    expect([...prefixes].sort()).toEqual([...EXPECTED_API_PREFIXES].sort());
  });

  it('only lists paths under /api/', () => {
    for (const prefix of prefixes) {
      expect(prefix.startsWith('/api/')).toBe(true);
    }
  });

  it('allows the API paths the app needs', () => {
    expect(predicate('/api/health')).toBe(true);
    expect(predicate('/api/config')).toBe(true);
    expect(predicate('/api/providers')).toBe(true);
    expect(predicate('/api/models')).toBe(true);
    expect(predicate('/api/models/unload')).toBe(true);
    expect(predicate('/api/packages')).toBe(true);
    expect(predicate('/api/packages?name=flask')).toBe(true);
    expect(predicate('/api/audits')).toBe(true);
    expect(predicate('/api/audits/a1/findings')).toBe(true);
    expect(predicate('/api/audits/a1/file')).toBe(true);
    expect(predicate('/api/chat')).toBe(true);
    expect(predicate('/api/terminals')).toBe(true);
    expect(predicate('/api/terminals/t1/input')).toBe(true);
    expect(predicate('/api/terminalsx')).toBe(false);
    expect(predicate('/api/debug')).toBe(true);
    expect(predicate('/api/debug/d1/command')).toBe(true);
    expect(predicate('/api/debugger')).toBe(false);
    expect(predicate('/api/advisories/status')).toBe(true);
  });

  it('refuses a path that is not under /api/', () => {
    expect(predicate('/etc/passwd')).toBe(false);
    expect(predicate('/ws')).toBe(false);
    expect(predicate('http://example.com/')).toBe(false);
  });

  it('refuses a path traversal', () => {
    expect(predicate('/api/audits/../../etc/passwd')).toBe(false);
    expect(predicate('/api/../secret')).toBe(false);
  });

  it('refuses an unknown API prefix', () => {
    expect(predicate('/api/internal')).toBe(false);
    expect(predicate('/api/auditsX')).toBe(false);
  });
});

describe('T8 — the preload bridge', () => {
  it('exposes exactly one object to the renderer', () => {
    const exposes = preloadSource.match(/contextBridge\.exposeInMainWorld\(/g) ?? [];
    expect(exposes).toHaveLength(1);
    expect(preloadSource).toMatch(/contextBridge\.exposeInMainWorld\(\s*'aisai'/);
  });

  it('exposes exactly the documented members', () => {
    // The bridge literal carries TypeScript annotations, so it cannot be
    // evaluated as JavaScript. Its members are read from the literal
    // instead: a member is a key at the first level of the object.
    const literal = extractObjectLiteral(preloadSource, 'const bridge: AisaiBridge =');
    const members: string[] = [];
    for (const line of literal.split('\n').slice(1)) {
      const match = /^ {2}([A-Za-z_$][\w$]*):/.exec(line);
      if (match) {
        members.push(match[1] as string);
      }
    }
    expect(members.sort()).toEqual(EXPECTED_BRIDGE_MEMBERS);
  });

  it('does not hand the renderer the node integration', () => {
    expect(preloadSource).not.toMatch(/exposeInMainWorld\([^)]*require\s*\(/);
    expect(preloadSource).not.toMatch(/exposeInMainWorld\([^)]*process\./);
  });
});
