/**
 * The project folders the user has chosen in this session.
 *
 * An audit of a folder copies that folder's source files into the audit
 * workspace, where the renderer can read them. The renderer shows untrusted
 * content (the code of the package under audit), so it must not be able to
 * name an arbitrary folder: the backend would copy it and the renderer could
 * then read the copy.
 *
 * A folder therefore only becomes auditable after the user has picked it, in
 * the native folder dialog or by dropping it on the window. The main process
 * records the pick here and refuses an audit request for any other folder.
 */

import { stat } from 'node:fs/promises';
import path from 'node:path';

export class FolderApprovals {
  private readonly approved = new Set<string>();

  /**
   * Approve a folder the user chose. Returns the normalised path, or `null`
   * when the path is not an absolute path to an existing folder.
   */
  async approve(folder: unknown): Promise<string | null> {
    if (typeof folder !== 'string' || !path.isAbsolute(folder)) return null;
    const resolved = path.resolve(folder);
    try {
      if (!(await stat(resolved)).isDirectory()) return null;
    } catch {
      return null;
    }
    this.approved.add(resolved);
    return resolved;
  }

  /** Whether the user chose this folder. */
  isApproved(folder: unknown): boolean {
    return typeof folder === 'string' && path.isAbsolute(folder) && this.approved.has(path.resolve(folder));
  }

  /**
   * Check an API request that came from the renderer. Returns a message when
   * the request would audit a folder the user did not choose, and `null` for
   * every other request.
   */
  check(method: string, apiPath: string, body: unknown): string | null {
    const route = apiPath.split('?')[0].split('#')[0];
    if (method !== 'POST' || route !== '/api/audits') return null;
    const folder = (body as { path?: unknown } | null | undefined)?.path;
    if (folder === undefined || folder === null || folder === '') return null;
    if (this.isApproved(folder)) return null;
    return 'Choose the folder with the folder picker, or drop it on the window.';
  }
}
