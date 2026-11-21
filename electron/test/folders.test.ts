import { mkdtemp, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';

import { describe, expect, it } from 'vitest';

import { FolderApprovals } from '../src/main-process/folders';

async function tempFolder(): Promise<string> {
  return mkdtemp(path.join(os.tmpdir(), 'aisai-folder-'));
}

describe('FolderApprovals', () => {
  it('approves an existing folder and returns its normalised path', async () => {
    const dir = await tempFolder();
    const approvals = new FolderApprovals();
    expect(await approvals.approve(`${dir}${path.sep}`)).toBe(path.resolve(dir));
    expect(approvals.isApproved(dir)).toBe(true);
  });

  it('refuses a relative path, a missing folder and a file', async () => {
    const dir = await tempFolder();
    const file = path.join(dir, 'a.txt');
    await writeFile(file, 'x');
    const approvals = new FolderApprovals();
    expect(await approvals.approve('relative/dir')).toBeNull();
    expect(await approvals.approve(path.join(dir, 'missing'))).toBeNull();
    expect(await approvals.approve(file)).toBeNull();
    expect(await approvals.approve(42)).toBeNull();
    expect(approvals.isApproved(file)).toBe(false);
  });

  it('refuses an audit request for a folder that was not chosen', async () => {
    const dir = await tempFolder();
    const approvals = new FolderApprovals();
    expect(approvals.check('POST', '/api/audits', { path: dir })).not.toBeNull();
    await approvals.approve(dir);
    expect(approvals.check('POST', '/api/audits', { path: dir })).toBeNull();
    expect(approvals.check('POST', '/api/audits?x=1', { path: '/etc' })).not.toBeNull();
  });

  it('does not touch requests that carry no folder', () => {
    const approvals = new FolderApprovals();
    expect(approvals.check('POST', '/api/audits', { package: 'flask' })).toBeNull();
    expect(approvals.check('POST', '/api/audits', null)).toBeNull();
    expect(approvals.check('GET', '/api/audits', undefined)).toBeNull();
    expect(approvals.check('POST', '/api/audits/a1/scan', { path: '/etc' })).toBeNull();
  });
});
