/**
 * afterPack hook of the unsigned macOS release build.
 *
 * With no signing identity, electron-builder leaves the bundle as it packed
 * it. Electron ships with a signature, and packing changes the bundle (the
 * app, the sidecar, Info.plist), so that signature no longer matches. On
 * Apple Silicon macOS then reports the app as "damaged and can't be opened",
 * which no right-click can get past. An ad-hoc signature (`-`) makes the seal
 * consistent again. It proves nothing about who built the app, so Gatekeeper
 * still asks the user to confirm the first launch, but the app opens.
 *
 * `codesign --sign -` needs no certificate and never touches the keychain.
 */
import { execFileSync } from 'node:child_process';
import path from 'node:path';

export default async function adhocSign(context) {
  if (context.electronPlatformName !== 'darwin') return;
  const app = path.join(context.appOutDir, `${context.packager.appInfo.productFilename}.app`);
  execFileSync('codesign', ['--force', '--deep', '--sign', '-', app], { stdio: 'inherit' });
  execFileSync('codesign', ['--verify', '--deep', '--strict', app], { stdio: 'inherit' });
  globalThis.console.log(`[adhoc-sign] ${app} carries a valid ad-hoc signature.`);
}
