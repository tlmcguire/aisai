/**
 * The search order for the Go sidecar binary.
 *
 * It lives in its own module, without any import of Electron, so that it can
 * be tested directly. The order is part of the contract: a stale binary that
 * shadows a fresh one is invisible until the behaviour differs.
 */

import path from 'node:path';

export interface SidecarLocation {
  /** Absolute path of the binary that was found. */
  path: string;
  /** Where it was found, for the UI message. */
  origin: string;
}

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

  if (override) candidates.push({ path: override, origin: 'AISAI_SERVER_BIN' });

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
  if (appPath) {
    const roots = [
      appPath,
      path.resolve(appPath, '..'),
      path.resolve(appPath, '..', '..'),
    ];
    for (const root of roots) {
      const candidate = path.join(root, 'bin', 'aisai-server');
      if (!candidates.some((c) => c.path === candidate)) {
        candidates.push({ path: candidate, origin: `repository ${root}/bin` });
      }
    }
  }

  if (resourcesPath) {
    candidates.push({ path: path.join(resourcesPath, 'aisai-server'), origin: 'app resources' });
  }

  candidates.push({ path: path.join(dirname, 'aisai-server'), origin: 'application directory' });

  return candidates;
}
