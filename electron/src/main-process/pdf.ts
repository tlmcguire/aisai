/**
 * Turns the HTML audit report into a PDF.
 *
 * The report is rendered in a hidden window and printed with Chromium's own
 * PDF printer, so the PDF looks like the HTML report on paper (its print
 * styles) and nothing new has to be installed. The window is locked down
 * harder than the app's own: no preload, no scripts, a sandbox, a private
 * in-memory session, and every request other than the report itself is
 * refused. The report text comes from the audited code (messages, snippets),
 * so it is treated as untrusted even though it is escaped by the backend.
 */

import { randomUUID } from 'node:crypto';
import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

import { BrowserWindow, session } from 'electron';

/** The largest report that is turned into a PDF. */
export const MAX_REPORT_BYTES = 25 * 1024 * 1024;

export interface PdfOptions {
  /** Shown in the running header of every page. */
  title: string;
  /** US Letter for a US English system, A4 elsewhere. */
  pageSize: 'A4' | 'Letter';
}

/** The paper size a locale expects. */
export function pageSizeFor(locale: string): 'A4' | 'Letter' {
  return /^(en-US|en-CA|es-MX|fr-CA|en-PH)$/i.test(locale) || /^en$/i.test(locale) ? 'Letter' : 'A4';
}

/** Escapes text for the header and footer templates, which are HTML. */
function escapeHtml(text: string): string {
  return text.replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c] ?? c);
}

/** Renders the report HTML to PDF bytes. */
export async function renderPdf(html: string, options: PdfOptions): Promise<Buffer> {
  if (Buffer.byteLength(html, 'utf8') > MAX_REPORT_BYTES) {
    throw new Error('The report is too large to turn into a PDF.');
  }
  const dir = await mkdtemp(path.join(os.tmpdir(), 'aisai-report-'));
  const file = path.join(dir, 'report.html');
  await writeFile(file, html, { encoding: 'utf8', mode: 0o600 });
  const reportUrl = pathToFileURL(file).toString();

  // A fresh in-memory session per export: nothing is shared with the app.
  const isolated = session.fromPartition(`aisai-pdf-${randomUUID()}`, { cache: false });
  isolated.webRequest.onBeforeRequest((details, callback) => {
    callback({ cancel: details.url !== reportUrl });
  });
  isolated.setPermissionRequestHandler((_wc, _permission, callback) => callback(false));

  const win = new BrowserWindow({
    show: false,
    width: 900,
    height: 1200,
    webPreferences: {
      session: isolated,
      javascript: false,
      sandbox: true,
      contextIsolation: true,
      nodeIntegration: false,
      webSecurity: true,
      spellcheck: false,
    },
  });
  win.webContents.setWindowOpenHandler(() => ({ action: 'deny' }));
  win.webContents.on('will-navigate', (event) => event.preventDefault());

  try {
    await win.loadURL(reportUrl);
    const title = escapeHtml(options.title);
    const small = 'font-family:system-ui,-apple-system,"Segoe UI",sans-serif;font-size:7.5pt;color:#566663;width:100%;padding:0 14mm;display:flex;justify-content:space-between';
    return await win.webContents.printToPDF({
      pageSize: options.pageSize,
      printBackground: true,
      preferCSSPageSize: false,
      margins: { top: 0.6, bottom: 0.6, left: 0, right: 0 },
      displayHeaderFooter: true,
      headerTemplate: `<div style='${small}'><span>aisai security report</span><span>${title}</span></div>`,
      footerTemplate: `<div style='${small}'><span class='date'></span><span>Page <span class='pageNumber'></span> of <span class='totalPages'></span></span></div>`,
    });
  } finally {
    win.destroy();
    await isolated.clearStorageData().catch(() => undefined);
    await rm(dir, { recursive: true, force: true }).catch(() => undefined);
  }
}
