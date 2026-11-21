#!/usr/bin/env node
/**
 * The electron-builder `beforeBuild` hook.
 *
 * It is the second line of defence behind .github/run-builder.mjs: even when
 * somebody starts electron-builder by hand with the release configuration,
 * a build without the signing credentials must stop here rather than
 * produce an unsigned artifact.
 *
 * electron-builder *imports* this module and calls the exported function,
 * so the checks have to live inside that function. A script that exits at
 * import time would take the whole build process down with it, and the
 * build would end quietly without an artifact.
 *
 * A developer build (no AISAI_RELEASE) is not affected: the hook returns
 * without doing anything.
 *
 * The file can also be run directly, which performs the same checks and
 * exits with a non-zero status when something is missing.
 */

import { pathToFileURL } from 'node:url';

const release = globalThis.process.env.AISAI_RELEASE === '1';
const unsigned = globalThis.process.env.AISAI_RELEASE_UNSIGNED === '1';

/**
 * The platform the build targets.
 *
 * The name is normalised, because the workflow calls the macOS job
 * "macos" and the Windows job "windows-amd64", while the credentials are
 * keyed on "mac" and "win".
 */
function targetOf() {
  const configured = (
    globalThis.process.env.AISAI_BUILD_TARGET
    ?? (globalThis.process.platform === 'darwin'
      ? 'mac'
      : globalThis.process.platform === 'win32'
        ? 'win'
        : 'linux')
  ).toLowerCase();
  if (configured.startsWith('mac')) {
    return 'mac';
  }
  if (configured.startsWith('win')) {
    return 'win';
  }
  return 'linux';
}

/** The credentials a target needs. */
function requiredCredentials(target) {
  switch (target) {
    case 'mac':
      return [
        ['CSC_LINK', 'the Developer ID Application certificate (p12)'],
        ['CSC_KEY_PASSWORD', 'the password of that certificate'],
        ['APPLE_ID', 'the Apple ID that notarises the build'],
        ['APPLE_APP_SPECIFIC_PASSWORD', 'the app-specific password of that Apple ID'],
        ['APPLE_TEAM_ID', 'the Apple team id'],
      ];
    case 'win':
      return [
        ['WIN_CSC_LINK', 'the code signing certificate (pfx)'],
        ['WIN_CSC_KEY_PASSWORD', 'the password of that certificate'],
        ['WINDOWS_PUBLISHER_NAME', 'the publisher name the updater verifies against'],
      ];
    default:
      return [];
  }
}

/**
 * The hook electron-builder calls before it packages the app.
 *
 * It throws when a credential is missing, which fails the build with a
 * message that says what is wrong.
 */
export async function beforeBuild() {
  if (!release) {
    return undefined;
  }

  if (unsigned) {
    globalThis.console.log('[verify-release-env] the release is unsigned; no credential is needed.');
    return undefined;
  }

  const target = targetOf();
  const missing = requiredCredentials(target)
    .filter(([name]) => !globalThis.process.env[name] || !globalThis.process.env[name].trim())
    .map(([name, description]) => '  ' + name + ' — ' + description);

  if (!globalThis.process.env.AISAI_UPDATE_FEED_URL || !globalThis.process.env.AISAI_UPDATE_FEED_URL.trim()) {
    missing.push('  AISAI_UPDATE_FEED_URL — the URL of the signed release feed');
  }

  if (missing.length > 0) {
    throw new Error(
      'The release build cannot start: the following credentials are missing.\n' +
        missing.join('\n') +
        '\n\nA release must be signed and must have a signed update feed.',
    );
  }

  globalThis.console.log('[verify-release-env] the ' + target + ' release credentials are present.');
  return undefined;
}

// Running the file directly performs the same checks.
const invokedDirectly =
  globalThis.process.argv[1] &&
  import.meta.url === pathToFileURL(globalThis.process.argv[1]).href;

if (invokedDirectly) {
  beforeBuild()
    .then(() => {
      globalThis.process.exit(0);
    })
    .catch((err) => {
      globalThis.console.error(err.message);
      globalThis.process.exit(1);
    });
}
