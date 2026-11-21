#!/usr/bin/env node
/**
 * Checks that the native binaries in node_modules match the architecture of
 * the Node process.
 *
 * The failure this prevents is confusing: an npm install that runs under
 * Rosetta 2 on an Apple Silicon Mac puts the x64 builds of esbuild and
 * rollup in node_modules, and every later build fails with
 *
 *   Cannot find module '@rollup/rollup-darwin-arm64'
 *
 * even though the lockfile lists it. The fix is a clean reinstall:
 *
 *   npm ci
 *
 * The script looks for the platform packages of the current architecture
 * and reports what is missing.
 */

import { existsSync, readdirSync } from 'node:fs';
import { join } from 'node:path';

/** The optional dependencies that ship a native binary per platform. */
const NATIVE_PACKAGES = ['@esbuild', '@rollup'];

const platform = process.platform;
const arch = process.arch;

/** The directory npm installs the optional dependencies into. */
const modulesDir = join(process.cwd(), 'node_modules');

/**
 * The name of the platform package of a native dependency.
 *
 * npm publishes one package per platform and architecture, for example
 * "@esbuild/darwin-arm64".
 */
function platformPackage(base, targetPlatform, targetArch) {
  return base + '/' + targetPlatform + '-' + targetArch;
}

/** The installed platform packages of a native dependency. */
function installedPackages(base) {
  const dir = join(modulesDir, base);
  if (!existsSync(dir)) {
    return [];
  }
  return readdirSync(dir).filter((entry) => entry.startsWith(platform + '-'));
}

const problems = [];

for (const base of NATIVE_PACKAGES) {
  // The package name without its scope, for example "darwin-arm64".
  const wanted = platform + '-' + arch;
  const installed = installedPackages(base);
  if (installed.length === 0) {
    // The dependency was never installed at all; npm will sort it out.
    continue;
  }
  if (!installed.includes(wanted)) {
    problems.push(
      base + ': the build for ' + wanted + ' is missing, but ' +
        installed.join(', ') + ' is installed',
    );
  }
}

if (problems.length > 0) {
  console.error('The native dependencies do not match this machine.');
  for (const problem of problems) {
    console.error('  - ' + problem);
  }
  console.error('');
  console.error('This usually means npm ran under a different architecture than');
  console.error('node (Rosetta 2 on an Apple Silicon Mac is the common case).');
  console.error('Reinstall the dependencies with the same node that runs the build:');
  console.error('');
  console.error('  npm ci');
  console.error('');
  console.error('If npm and node still disagree, install node with the universal');
  console.error('installer: https://nodejs.org/en/download/');
  process.exit(1);
}

console.log('[check-native-arch] the native dependencies match ' + platform + '-' + arch + '.');
