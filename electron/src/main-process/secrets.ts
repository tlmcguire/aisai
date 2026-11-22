/**
 * API keys for hosted model services, kept in the operating system's
 * credential store through Electron's safeStorage (the macOS Keychain, the
 * Windows DPAPI, libsecret on Linux; https://www.electronjs.org/docs/latest/api/safe-storage).
 *
 * On macOS, the first use of safeStorage asks for the "AISAI Safe Storage"
 * Keychain item, which may show a password prompt. That is why nothing here
 * touches it until a key has actually been saved.
 *
 * The renderer can hand a key to the main process and ask whether one is
 * saved, but it can never read a saved key back. The main process adds a
 * saved key to provider requests on the renderer's behalf, and passes the
 * active one to the Go backend through AISAI_BYOK_API_KEY at launch.
 */

import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import path from 'node:path';

import { app, safeStorage } from 'electron';

interface SecretFile {
  /** The provider kind the backend was last switched to. */
  active?: string;
  /** Encrypted keys, base64, by provider kind. */
  keys: Record<string, string>;
}

const KIND = /^[a-z][a-z0-9]{1,23}$/;

function file(): string {
  return path.join(app.getPath('userData'), 'provider-secrets.json');
}

function load(): SecretFile {
  try {
    if (!existsSync(file())) return { keys: {} };
    const parsed = JSON.parse(readFileSync(file(), 'utf8')) as Partial<SecretFile>;
    return { active: parsed.active, keys: parsed.keys ?? {} };
  } catch {
    return { keys: {} };
  }
}

function save(data: SecretFile): void {
  writeFileSync(file(), JSON.stringify(data, null, 2), { mode: 0o600 });
}

/** Whether keys can be stored encrypted on this machine. */
export function canStoreSecrets(): boolean {
  return safeStorage.isEncryptionAvailable();
}

/** Store a key for a provider kind. Refuses when encryption is unavailable. */
export function saveSecret(kind: string, key: string): boolean {
  if (!KIND.test(kind) || !key || !canStoreSecrets()) return false;
  const data = load();
  data.keys[kind] = safeStorage.encryptString(key).toString('base64');
  save(data);
  return true;
}

/** Read a key. Main process only: never send the result to the renderer. */
export function readSecret(kind: string): string | null {
  if (!KIND.test(kind) || !canStoreSecrets()) return null;
  const blob = load().keys[kind];
  if (!blob) return null;
  try {
    return safeStorage.decryptString(Buffer.from(blob, 'base64'));
  } catch {
    return null;
  }
}

export function hasSecret(kind: string): boolean {
  return KIND.test(kind) && Boolean(load().keys[kind]);
}

export function forgetSecret(kind: string): void {
  const data = load();
  delete data.keys[kind];
  save(data);
}

/** Remember which provider is active, so its key is passed at launch. */
export function setActiveProvider(kind: string): void {
  if (!KIND.test(kind)) return;
  const data = load();
  data.active = kind;
  save(data);
}

/** The key of the active provider, for the backend's environment. */
export function activeSecret(): string | null {
  const data = load();
  // Reading the keychain at launch makes macOS ask for permission to use
  // "AISAI Safe Storage", so do not touch it unless a key is actually saved.
  if (!data.active || !data.keys[data.active]) return null;
  return readSecret(data.active);
}
