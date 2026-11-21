/**
 * The search order for the Go sidecar binary.
 *
 * It lives in its own module, without any import of Electron, so that it
 * can be tested directly. The order is part of the contract: a stale binary
 * that shadows a fresh one is invisible until the behaviour differs.
 */

import path from 'node:path';

export interface SidecarLocation {
  /** Absolute path of the binary that was found. */
  path: string;
  /** Where it was found, for the UI message. */
  origin: string;
}

/**
 * The names the sidecar binary can have.
 *
 * `go build` writes `aisai-server` on macOS and Linux, and `aisai-server.exe`
 * on Windows. Both names are offered at every location: a name that does not
 * exist is skipped by the caller, and the platform is not known here.
 */
const NAMES = ['aisai-server', 'aisai-server.exe'];

/**
 * The ordered list of places the sidecar may live.
 *
 * @param appPath       the application path, as `app.getAppPath()` reports it.
 * @param resourcesPath the resources directory of a packaged app.
 * @param dirname       the directory of the compiled main process.
 * @param override      the value of `AISAI_SERVER_BIN`, if it is set.
 */
export function sidecarCandidates(
  appPath: string,
  resourcesPath: string,
  dirname: string,
  override: string | undefined,
): SidecarLocation[] {
  const candidates: SidecarLocation[] = [];
  const seen = new Set<string>();

  const push = (filePath: string, origin: string): void => {
    if (seen.has(filePath)) return;
    seen.add(filePath);
    candidates.push({ path: filePath, origin });
  };

  if (override) {
    push(override, 'AISAI_SERVER_BIN');
    // The override names a file, so the Windows name is offered next to it
    // rather than inside it.
    if (!override.toLowerCase().endsWith('.exe')) {
      push(override + '.exe', 'AISAI_SERVER_BIN');
    }
  }

  // The repository's build output, while developing.
  //
  // The Electron app lives in <repo>/electron, so the repository root is
  // ONE level up. The application path itself, its parent and its
  // grandparent are all tried, because the path depends on how the app was
  // launched: `electron .` from the electron directory reports <repo>/electron,
  // while some launch contexts report the repository root itself.
  //
  // Getting this wrong is not cosmetic. A path that does not exist is
  // skipped, and the search falls through to the copy next to the main
  // process — which `npm run build` made from the Go binary as it was, and
  // which then goes stale the moment the Go code changes. The symptom is an
  // app that runs yesterday's backend and nobody can say why.
  const directories: Array<{ dir: string; origin: string }> = [];

  if (appPath) {
    const roots = [
      appPath,
      path.resolve(appPath, '..'),
      path.resolve(appPath, '..', '..'),
    ];
    for (const root of roots) {
      directories.push({
        dir: path.join(root, 'bin'),
        origin: 'repository ' + root + '/bin',
      });
    }
  }

  // The packaged app. The sidecar is copied next to the app's resources,
  // outside the asar archive, because a binary inside an asar cannot be
  // executed.
  if (resourcesPath) {
    directories.push({ dir: resourcesPath, origin: 'app resources' });
  }

  directories.push({ dir: dirname, origin: 'application directory' });

  for (const { dir, origin } of directories) {
    for (const name of NAMES) {
      push(path.join(dir, name), origin);
    }
  }

  return candidates;
}
