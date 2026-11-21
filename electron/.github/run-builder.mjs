#!/usr/bin/env node
/**
 * The release build wrapper.
 *
 * It exists because a release build and a developer build need different
 * rules, and electron-builder's configuration is static:
 *
 *   - a developer build must work on a machine with no signing identity;
 *   - a release build must *fail* when the signing identity or the
 *     auto-update feed is missing, never silently produce an unsigned
 *     artifact.
 *
 * So the developer configuration lives in package.json ("build") and the
 * release configuration lives in .github/electron-builder.release.json. This
 * script merges the two for a release, checks that every credential the
 * target needs is present, and only then starts electron-builder.
 *
 * Usage:
 *   node .github/run-builder.mjs [--mac|--win|--linux] [--arm64|--x64]
 *                                 [--release] [--unsigned]
 *
 * With --release (or AISAI_RELEASE=1) the release configuration is used.
 * A signed release (the default) needs the credentials and fails without
 * them; an unsigned release (--unsigned, or AISAI_RELEASE_UNSIGNED=1)
 * publishes an artifact that is not signed and is not notarised.
 */

import { spawn } from 'node:child_process';
import { readFileSync, writeFileSync, mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const projectDir = resolve(here, '..');

/** Reads package.json. */
function readPackageJson() {
  return JSON.parse(readFileSync(join(projectDir, 'package.json'), 'utf8'));
}

/** Reads the release configuration. */
function readReleaseConfig() {
  return JSON.parse(readFileSync(join(here, 'electron-builder.release.json'), 'utf8'));
}

/** Reads the unsigned release configuration. */
function readUnsignedConfig() {
  return JSON.parse(readFileSync(join(here, 'electron-builder.unsigned.json'), 'utf8'));
}

/**
 * Merges the release configuration over the base configuration.
 *
 * Objects are merged key by key; every other value (arrays included) is
 * replaced, because a release overrides the target list and the publish
 * provider rather than adding to them.
 */
function merge(base, override) {
  if (Array.isArray(override) || override === null || typeof override !== 'object') {
    return override;
  }
  const out = { ...base };
  for (const [key, value] of Object.entries(override)) {
    const current = base && typeof base === 'object' && !Array.isArray(base) ? base[key] : undefined;
    out[key] = current && typeof current === 'object' && !Array.isArray(current)
      ? merge(current, value)
      : value;
  }
  return out;
}

/** The credentials a target needs. */
function requiredEnv(target) {
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

/** The platform the build targets. */
function targetOf(argv) {
  const flag = argv.find((arg) => ['--mac', '--win', '--linux'].includes(arg));
  if (flag === '--mac') return 'mac';
  if (flag === '--win') return 'win';
  if (flag === '--linux') return 'linux';
  if (globalThis.process.platform === 'darwin') return 'mac';
  if (globalThis.process.platform === 'win32') return 'win';
  return 'linux';
}

/**
 * The architectures the build targets, from the command line.
 *
 * It is empty when no architecture was named, in which case the
 * configuration decides.
 */
function archOf(argv) {
  return argv
    .filter((arg) => ['--arm64', '--x64', '--ia32'].includes(arg))
    .map((arg) => arg.slice(2));
}

/**
 * Restricts every target of the configuration to the given architectures.
 *
 * Passing --arm64 on the command line is not enough on its own: a target
 * that names its own architectures wins over the command line. Without
 * this, every runner would build every architecture and the uploads would
 * overwrite each other.
 */
function restrictToArch(config, archs) {
  if (!archs || archs.length === 0) {
    return config;
  }
  for (const platform of ['mac', 'win', 'linux']) {
    const section = config[platform];
    if (!section || !Array.isArray(section.target)) {
      continue;
    }
    for (const target of section.target) {
      if (target && typeof target === 'object' && Array.isArray(target.arch)) {
        target.arch = [...archs];
      }
    }
  }
  return config;
}

/** Fails loudly when a credential is missing. */
function requireEnv(target) {
  const missing = [];
  for (const [name, description] of requiredEnv(target)) {
    const value = globalThis.process.env[name];
    if (!value || !value.trim()) {
      missing.push(`  ${name} — ${description}`);
    }
  }
  if (!globalThis.process.env.AISAI_UPDATE_FEED_URL || !globalThis.process.env.AISAI_UPDATE_FEED_URL.trim()) {
    missing.push('  AISAI_UPDATE_FEED_URL — the URL of the signed release feed');
  }
  if (missing.length > 0) {
    globalThis.console.error('The release build cannot start: the following credentials are missing.');
    globalThis.console.error(missing.join('\n'));
    globalThis.console.error('');
    globalThis.console.error('A release must be signed and must have a signed update feed.');
    globalThis.console.error('Set the missing values and run the release again.');
    globalThis.process.exit(1);
  }
}

/**
 * Runs electron-builder with the given arguments.
 *
 * The project directory is passed explicitly. Without it, electron-builder
 * takes it from the directory of the configuration file, and every relative
 * path in the configuration (the files to package, the sidecar, the
 * output directory) would be resolved against the temporary directory the
 * configuration was written to.
 */
function runBuilder(args, env) {
  const binary = resolve(projectDir, 'node_modules', '.bin', 'electron-builder');
  const child = spawn(binary, [...args, '--project', projectDir], {
    cwd: projectDir,
    stdio: 'inherit',
    env: { ...globalThis.process.env, ...(env ?? {}) },
  });
  child.on('exit', (code, signal) => {
    if (signal) {
      globalThis.process.kill(globalThis.process.pid, signal);
      return;
    }
    globalThis.process.exit(code ?? 1);
  });
}

function main() {
  const argv = globalThis.process.argv.slice(2);
  const release = argv.includes('--release') || globalThis.process.env.AISAI_RELEASE === '1';
  const unsigned = argv.includes('--unsigned') || globalThis.process.env.AISAI_RELEASE_UNSIGNED === '1';
  const dryRun = argv.includes('--print-config');
  const target = targetOf(argv);
  const pkg = readPackageJson();

  const platformArgs = argv.filter((arg) => ['--mac', '--win', '--linux'].includes(arg));
  // The architecture is passed explicitly so that every build produces
  // one artifact per architecture, instead of every runner producing all
  // of them and overwriting each other.
  const archArgs = argv.filter((arg) => ['--arm64', '--x64', '--ia32'].includes(arg));

  if (!release) {
    globalThis.console.log('[run-builder] developer build: signing is not required.');
    if (dryRun) {
      globalThis.console.log(JSON.stringify(pkg.build ?? {}, null, 2));
      return;
    }
    runBuilder([...platformArgs, ...archArgs, '--publish', 'never']);
    return;
  }

  if (unsigned) {
    globalThis.console.log(`[run-builder] unsigned release build for ${target}.`);
    globalThis.console.log('[run-builder] the artifact is not signed and not notarised.');

    const unsignedConfig = restrictToArch(merge(pkg.build ?? {}, readUnsignedConfig()), archOf(argv));

    if (dryRun) {
      globalThis.console.log(JSON.stringify(unsignedConfig, null, 2));
      return;
    }

    const unsignedDir = mkdtempSync(join(tmpdir(), 'aisai-builder-'));
    const unsignedConfigPath = join(unsignedDir, 'electron-builder.json');
    writeFileSync(unsignedConfigPath, JSON.stringify(unsignedConfig, null, 2));

    runBuilder(
      [...platformArgs, ...archArgs, '--config', unsignedConfigPath, '--publish', 'never'],
      // An unsigned build must not pick up a signing identity from the
      // keychain: it would sign the artifact with whatever it finds and
      // fail on a certificate it cannot use.
      { CSC_IDENTITY_AUTO_DISCOVERY: 'false' },
    );
    return;
  }

  globalThis.console.log(`[run-builder] release build for ${target}.`);
  requireEnv(target);

  const config = restrictToArch(merge(pkg.build ?? {}, readReleaseConfig()), archOf(argv));

  if (dryRun) {
    globalThis.console.log(JSON.stringify(config, null, 2));
    return;
  }

  const dir = mkdtempSync(join(tmpdir(), 'aisai-builder-'));
  const configPath = join(dir, 'electron-builder.json');
  writeFileSync(configPath, JSON.stringify(config, null, 2));

  runBuilder([...platformArgs, ...archArgs, '--config', configPath, '--publish', 'always']);
}

main();
