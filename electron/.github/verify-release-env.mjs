#!/usr/bin/env node
/**
 * The electron-builder `beforeBuild` hook.
 *
 * It is the second line of defence behind .github/run-builder.mjs: even when
 * somebody starts electron-builder by hand with the release configuration,
 * a build without the signing credentials must stop here rather than
 * produce an unsigned artifact.
 *
 * A developer build (no AISAI_RELEASE) is not affected: the hook exits
 * quietly.
 */

const release = globalThis.process.env.AISAI_RELEASE === '1';

if (!release) {
  globalThis.process.exit(0);
}

const target = globalThis.process.env.AISAI_BUILD_TARGET
  ?? (globalThis.process.platform === 'darwin' ? 'mac' : globalThis.process.platform === 'win32' ? 'win' : 'linux');

const required = {
  mac: [
    ['CSC_LINK', 'the Developer ID Application certificate (p12)'],
    ['CSC_KEY_PASSWORD', 'the password of that certificate'],
    ['APPLE_ID', 'the Apple ID that notarises the build'],
    ['APPLE_APP_SPECIFIC_PASSWORD', 'the app-specific password of that Apple ID'],
    ['APPLE_TEAM_ID', 'the Apple team id'],
  ],
  win: [
    ['WIN_CSC_LINK', 'the code signing certificate (pfx)'],
    ['WIN_CSC_KEY_PASSWORD', 'the password of that certificate'],
    ['WINDOWS_PUBLISHER_NAME', 'the publisher name the updater verifies against'],
  ],
  linux: [],
}[target] ?? [];

const missing = required
  .filter(([name]) => !globalThis.process.env[name] || !globalThis.process.env[name].trim())
  .map(([name, description]) => `  ${name} — ${description}`);

if (!globalThis.process.env.AISAI_UPDATE_FEED_URL || !globalThis.process.env.AISAI_UPDATE_FEED_URL.trim()) {
  missing.push('  AISAI_UPDATE_FEED_URL — the URL of the signed release feed');
}

if (missing.length > 0) {
  globalThis.console.error('The release build cannot start: the following credentials are missing.');
  globalThis.console.error(missing.join('\n'));
  globalThis.process.exit(1);
}

globalThis.console.log(`[verify-release-env] the ${target} release credentials are present.`);
