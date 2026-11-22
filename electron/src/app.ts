/**
 * Application orchestration: talks to the backend, keeps the zustand stores in
 * sync and exposes the actions the views call.
 */

import { ApiClient, ApiError } from './api/client';
import type {
  Advisory,
  ApplyPatchResponse,
  AST,
  Audit,
  AuditResponse,
  AuditsResponse,
  ChatDoneData,
  ChatTokenData,
  ChunksResponse,
  Config,
  FileContent,
  FileNode,
  FilesResponse,
  Finding,
  FindingsResponse,
  GraphResponse,
  HealthResponse,
  IndexStatusResponse,
  JobResponse,
  ModelSelection,
  ModelState,
  ModelsResponse,
  Patch,
  PatchResponse,
  PackageLookup,
  LinkInfo,
  SaveFileResponse,
  TerminalOpenResponse,
  Ecosystem,
  DownloadEvent,
  ProviderInfo,
  ProviderRequest,
  UnloadResponse,
} from './api/types';
import { useAuditStore } from './state/audit';
import { useChatStore } from './state/chat';
import { terminalBus, useTerminalStore } from './state/terminal';
import { useDebugStore, type DebugFrame, type DebugVariable } from './state/debug';
import { useFindingsStore } from './state/findings';
import { useGraphStore } from './state/graph';
import { useUiStore } from './state/ui';
import { useWorkspaceStore } from './state/workspace';

function messageOf(err: unknown): string {
  if (err instanceof ApiError) return err.message;
  if (err instanceof Error) return err.message;
  return String(err);
}

export class AppController {
  private auditId: string | null = null;
  private unsubscribe: (() => void) | null = null;

  constructor(private readonly client: ApiClient) {}

  get clientRef(): ApiClient {
    return this.client;
  }

  get currentAuditId(): string | null {
    return this.auditId;
  }

  /* ---------------------------------------------------------------- */
  /* Bootstrap                                                         */
  /* ---------------------------------------------------------------- */

  async bootstrap(): Promise<void> {
    this.unsubscribe = this.client.onEvent((event) => this.onEvent(event));
    this.unsubscribeDebug = this.watchDebugger();
    await this.reloadAudits();
  }

  dispose(): void {
    this.unsubscribe?.();
    this.unsubscribe = null;
    this.unsubscribeDebug?.();
    this.unsubscribeDebug = null;
  }

  private async reloadAudits(): Promise<void> {
    const auditStore = useAuditStore.getState();
    try {
      const response = await this.client.request<AuditsResponse>('GET', '/api/audits');
      auditStore.setAudits(response.audits ?? []);
      const first = response.audits?.[0];
      if (first) {
        await this.selectAudit(first.id);
      } else {
        this.auditId = null;
        auditStore.setCurrent(null);
      }
    } catch (err) {
      auditStore.setError(messageOf(err));
    }
  }

  /** Load everything that belongs to one audit. */
  async selectAudit(id: string): Promise<void> {
    this.auditId = id;
    useChatStore.getState().setAudit(id);
    const auditStore = useAuditStore.getState();
    try {
      const response = await this.client.request<AuditResponse>('GET', `/api/audits/${id}`);
      auditStore.setCurrent(response.audit);
    } catch (err) {
      auditStore.setError(messageOf(err));
    }
    await Promise.all([this.loadGraph(), this.loadFiles(), this.loadFindings()]);
  }

  async loadGraph(): Promise<void> {
    if (!this.auditId) return;
    try {
      const response = await this.client.request<GraphResponse>(
        'GET',
        `/api/audits/${this.auditId}/graph`,
      );
      useGraphStore.getState().setGraph(response.graph);
    } catch (err) {
      useAuditStore.getState().setError(messageOf(err));
    }
  }

  async loadFiles(): Promise<void> {
    if (!this.auditId) return;
    const workspace = useWorkspaceStore.getState();
    try {
      const response = await this.client.request<FilesResponse>(
        'GET',
        `/api/audits/${this.auditId}/files`,
      );
      workspace.setTree(response.tree);
      workspace.expand(collectDirectories(response.tree));
    } catch (err) {
      workspace.setError(messageOf(err));
    }
  }

  async loadFindings(): Promise<void> {
    if (!this.auditId) return;
    const findings = useFindingsStore.getState();
    try {
      const response = await this.client.request<FindingsResponse>(
        'GET',
        `/api/audits/${this.auditId}/findings`,
      );
      findings.setFindings(response.findings ?? [], response.total ?? 0);
    } catch (err) {
      findings.setError(messageOf(err));
    }
  }

  /* ---------------------------------------------------------------- */
  /* Actions                                                           */
  /* ---------------------------------------------------------------- */

  /** Start a new audit for a package. */
  async startAudit(packageName: string, version: string, ecosystem: Ecosystem = 'pypi'): Promise<void> {
    const body: Record<string, string> = { package: packageName, ecosystem };
    if (version) body.version = version;
    await this.beginAudit(packageName, body);
  }

  /** Pull a pasted link: a package page, a package URL or a repository to clone. */
  async startLinkAudit(url: string): Promise<void> {
    await this.beginAudit(url, { url });
  }

  /** Works out what a pasted link refers to. Resolves to an error message when it is none. */
  async resolveLink(url: string): Promise<{ link?: LinkInfo; error?: string }> {
    try {
      return await this.client.request<{ link?: LinkInfo; error?: string }>(
        'GET',
        `/api/packages/link?url=${encodeURIComponent(url)}`,
      );
    } catch (err) {
      return { error: messageOf(err) };
    }
  }

  /** Start a new audit for a project folder the user chose. */
  async startFolderAudit(folder: string): Promise<void> {
    const name = folder.split(/[\\/]/).filter(Boolean).pop() ?? folder;
    await this.beginAudit(name, { path: folder });
  }

  /** Open the native folder dialog and audit the folder the user picks. */
  async chooseFolder(): Promise<void> {
    try {
      const folder = await window.aisai.chooseFolder();
      if (folder) await this.startFolderAudit(folder);
    } catch (err) {
      useAuditStore.getState().setError(messageOf(err));
    }
  }

  /** Create an audit from a request body and follow it. */
  private async beginAudit(label: string, body: Record<string, string>): Promise<void> {
    const auditStore = useAuditStore.getState();
    auditStore.setError(null);
    auditStore.clearDownloads();
    auditStore.setBusy('audit');
    auditStore.setProgress('queued', 0, `Starting the audit of ${label}`);
    try {
      const response = await this.client.request<AuditResponse>('POST', '/api/audits', body);
      auditStore.setCurrent(response.audit);
      this.auditId = response.audit?.id ?? this.auditId;
      useChatStore.getState().setAudit(this.auditId);
      await this.reloadAudits();
      // The audit keeps running in the backend after the request returns:
      // the job stays busy until audit.done or audit.error arrives, so the
      // progress strip and the download tracker stay up while it works.
      const status = useAuditStore.getState().current?.status;
      if (status === 'ready' || status === 'failed') auditStore.setBusy(null);
      else if (this.auditId) this.watchAudit(this.auditId);
    } catch (err) {
      auditStore.setError(messageOf(err));
      auditStore.setProgressVisible(false);
      auditStore.setBusy(null);
    }
  }

  /**
   * Follows a running audit by polling, as a safety net for the event stream.
   *
   * The audit's progress and completion arrive as events. If the event
   * connection is down or an event is dropped, the Scan button would stay
   * disabled for good and the app would look frozen. Asking for the audit's
   * status every few seconds makes sure it always ends.
   */
  private watchAudit(id: string): void {
    const timer = window.setInterval(() => {
      const store = useAuditStore.getState();
      // The events got there first, or another audit was started.
      if (store.busy !== 'audit' || this.auditId !== id) {
        window.clearInterval(timer);
        return;
      }
      void this.client
        .request<AuditResponse>('GET', `/api/audits/${id}`)
        .then((response) => {
          const audit = response.audit;
          const now = useAuditStore.getState();
          if (!audit || now.busy !== 'audit' || this.auditId !== id) return;
          if (audit.status === 'ready') {
            window.clearInterval(timer);
            now.setProgress('complete', 1, 'Audit complete');
            now.setProgressVisible(false);
            now.setBusy(null);
            void this.selectAudit(id);
          } else if (audit.status === 'failed') {
            window.clearInterval(timer);
            now.setError(audit.error || 'The audit failed.');
            now.setProgressVisible(false);
            now.setBusy(null);
          }
        })
        .catch(() => {
          /* the next tick tries again */
        });
    }, 3000);
  }

  /** Run a scan (sast or llm) over the current audit. */
  async runScan(engine: 'sast' | 'llm'): Promise<void> {
    if (!this.auditId) return;
    const auditStore = useAuditStore.getState();
    auditStore.setError(null);
    auditStore.setBusy(`scan:${engine}`);
    auditStore.setProgress(`${engine} scan`, 0, 'Starting the scan');
    try {
      const response = await this.client.request<JobResponse>(
        'POST',
        `/api/audits/${this.auditId}/scan`,
        engine === 'llm' ? { engine } : { engine: 'sast' },
      );
      // The scan runs in the backend after the request returns. The job stays
      // busy until scan.done or scan.error arrives, with polling as a safety net.
      this.scanJob = response.jobId;
      this.watchScan(response.jobId, this.auditId);
    } catch (err) {
      auditStore.setError(messageOf(err));
      auditStore.setProgressVisible(false);
      auditStore.setBusy(null);
    }
  }

  /** The scan job the app is waiting for. */
  private scanJob: string | null = null;

  /** Ends the wait for a scan and shows what it found. */
  private finishScan(jobId: string, error?: string): void {
    if (this.scanJob !== jobId) return;
    this.scanJob = null;
    const store = useAuditStore.getState();
    if (error) store.setError(error);
    store.setProgress('complete', 1, error ? 'The scan failed' : 'Scan complete');
    store.setProgressVisible(false);
    store.setBusy(null);
    void Promise.all([this.loadFindings(), this.loadFiles(), this.loadGraph()]);
  }

  private watchScan(jobId: string, auditId: string): void {
    const timer = window.setInterval(() => {
      if (this.scanJob !== jobId) {
        window.clearInterval(timer);
        return;
      }
      void this.client
        .request<{ job: { state: string; error?: string } }>('GET', `/api/audits/${auditId}/jobs/${jobId}`)
        .then((response) => {
          if (response.job.state === 'done') this.finishScan(jobId);
          else if (response.job.state === 'failed') this.finishScan(jobId, response.job.error ?? 'The scan failed.');
        })
        .catch(() => {
          /* the next tick tries again */
        });
    }, 3000);
  }

  /** Re-reads the open file after something else changed it on disk (a patch). */
  async reloadFile(path: string): Promise<void> {
    if (!this.auditId || useWorkspaceStore.getState().file?.path !== path) return;
    try {
      const response = await this.client.request<{ file: FileContent }>(
        'GET',
        `/api/audits/${this.auditId}/file?path=${encodeURIComponent(path)}`,
      );
      useWorkspaceStore.getState().setFile(response.file);
      await this.loadAst(path);
    } catch {
      /* the open copy stays; the next open reads it again */
    }
  }

  /**
   * Saves an edit made in the editor.
   *
   * The backend backs the file up and records the change, so it shows up in
   * the Changes view and can be reverted; it also re-scans the file, so the
   * findings follow the new text.
   */
  async saveFile(path: string, content: string): Promise<boolean> {
    if (!this.auditId) return false;
    const auditStore = useAuditStore.getState();
    try {
      const response = await this.client.request<SaveFileResponse>(
        'PUT',
        `/api/audits/${this.auditId}/file`,
        { path, content },
      );
      if (response.error) auditStore.setError(`Saved, but the re-scan failed: ${response.error}`);
      await Promise.all([this.loadFindings(), this.loadFiles(), this.loadAst(path)]);
      // The open copy becomes what was saved, with the findings of the
      // re-scan: Discard then goes back to the saved text, not the old one.
      const workspace = useWorkspaceStore.getState();
      if (workspace.file?.path === path) {
        workspace.setFile({
          ...workspace.file,
          lines: content.split('\n'),
          findings: useFindingsStore.getState().findings.filter((f) => f.file === path),
        });
      }
      return true;
    } catch (err) {
      auditStore.setError(messageOf(err));
      return false;
    }
  }

  /** Inline completion for the cursor position. Resolves to '' when there is none. */
  async complete(path: string, language: string, prefix: string, suffix: string): Promise<string> {
    if (!this.auditId || !useChatStore.getState().model.available) return '';
    try {
      const response = await this.client.request<{ completion: string }>(
        'POST',
        `/api/audits/${this.auditId}/complete`,
        { path, language, prefix, suffix },
      );
      return response.completion ?? '';
    } catch {
      return '';
    }
  }

  /** Asks the model to rewrite a selection. Throws the backend's message on failure. */
  async editSelection(request: {
    path: string;
    language: string;
    before: string;
    selection: string;
    after: string;
    instruction: string;
    startLine: number;
    endLine: number;
  }): Promise<string> {
    if (!this.auditId) throw new Error('Open an audit first.');
    const response = await this.client.request<{ replacement: string }>(
      'POST',
      `/api/audits/${this.auditId}/edit-selection`,
      request,
    );
    return response.replacement;
  }

  /** Open a file in the code pane and optionally jump to a line. */
  async openFile(path: string, line?: number): Promise<void> {
    if (!this.auditId) return;
    const workspace = useWorkspaceStore.getState();
    workspace.setLoading(true);
    workspace.setError(null);
    try {
      const response = await this.client.request<{ file: FileContent }>(
        'GET',
        `/api/audits/${this.auditId}/file?path=${encodeURIComponent(path)}`,
      );
      workspace.setFile(response.file);
      useUiStore.getState().setActiveTab('code');
      if (line && line > 0) {
        workspace.requestJump(line);
      }
      await this.loadAst(path);
    } catch (err) {
      workspace.setError(messageOf(err));
    } finally {
      workspace.setLoading(false);
    }
  }

  async loadAst(path: string): Promise<void> {
    if (!this.auditId) return;
    try {
      const response = await this.client.request<{ ast: AST }>(
        'GET',
        `/api/audits/${this.auditId}/ast?path=${encodeURIComponent(path)}`,
      );
      useWorkspaceStore.getState().setAst(response.ast);
    } catch {
      /* The AST is optional — a missing AST must not break the code pane. */
      useWorkspaceStore.getState().setAst(null);
    }
  }

  /** Ask the backend for a proposed patch for a finding. */
  async proposeFix(findingId: string): Promise<void> {
    if (!this.auditId) return;
    const workspace = useWorkspaceStore.getState();
    workspace.setLoading(true);
    workspace.setError(null);
    try {
      const response = await this.client.request<PatchResponse>(
        'POST',
        `/api/audits/${this.auditId}/propose-fix`,
        { findingId },
      );
      workspace.setPatch(response.patch);
      useUiStore.getState().setActiveTab('diff');
    } catch (err) {
      workspace.setError(messageOf(err));
    } finally {
      workspace.setLoading(false);
    }
  }

  /** Apply the current patch inside the audit workspace. */
  async applyPatch(path: string): Promise<ApplyPatchResponse | null> {
    if (!this.auditId) return null;
    const patch = useWorkspaceStore.getState().patch;
    if (!patch) return null;
    const auditStore = useAuditStore.getState();
    auditStore.setBusy('apply');
    try {
      const response = await this.client.request<ApplyPatchResponse>(
        'POST',
        `/api/audits/${this.auditId}/apply-patch`,
        { path, unified: patch.unified },
      );
      // Accepting a fix marks its finding fixed, unless the preference is off.
      if (response.applied && patch.findingId && useUiStore.getState().acceptMarksFixed) {
        await this.setFindingState(patch.findingId, 'fixed');
      }
      await this.loadFindings();
      // The open file must show the patched text, or a later save from the
      // editor would silently undo the fix.
      await this.reloadFile(path);
      return response;
    } catch (err) {
      auditStore.setError(messageOf(err));
      return null;
    } finally {
      auditStore.setBusy(null);
    }
  }

  /** Download the findings as a themed, self-contained HTML report. */
  async exportReport(): Promise<void> {
    if (!this.auditId) return;
    try {
      const html = await this.client.requestText('GET', `/api/audits/${this.auditId}/export?format=html`);
      const url = URL.createObjectURL(new Blob([html], { type: 'text/html' }));
      const link = document.createElement('a');
      link.href = url;
      link.download = `aisai-${useAuditStore.getState().current?.package ?? this.auditId}-report.html`;
      document.body.append(link);
      link.click();
      link.remove();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
    } catch (err) {
      useFindingsStore.getState().setError(messageOf(err));
    }
  }

  /** Change the triage state of a finding. */
  async setFindingState(
    findingId: string,
    state: 'open' | 'confirmed' | 'suppressed' | 'fixed',
    suppressedUntil?: string,
  ): Promise<void> {
    if (!this.auditId) return;
    const findings = useFindingsStore.getState();
    try {
      const body: Record<string, string> = { state };
      if (state === 'suppressed' && suppressedUntil) body.suppressedUntil = suppressedUntil;
      const response = await this.client.request<{ finding: Finding }>(
        'POST',
        `/api/audits/${this.auditId}/findings/${findingId}/state`,
        body,
      );
      findings.upsert(response.finding);
    } catch (err) {
      findings.setError(messageOf(err));
    }
  }

  /**
   * Works out which model answers chat questions, and whether one can.
   *
   * Chat is limited, not broken, when no model is usable: the panel says so
   * and does not accept a question that could only fail.
   */
  async refreshChatModel(providers?: ProviderInfo[]): Promise<void> {
    const list = providers ?? (await this.fetchProviders());
    const config = await this.fetchConfig();
    const name = config?.chatModel?.trim() ?? '';
    const usable = list.filter((provider) => provider.ok);
    const provider = usable[0];
    let reason = '';
    if (!list.length) reason = 'No model server is configured.';
    else if (!usable.length) reason = 'The model server is not reachable.';
    else if (!name) reason = 'No chat model is selected.';
    useChatStore.getState().setModel({
      name,
      provider: provider?.kind ?? list[0]?.kind ?? '',
      available: reason === '',
      reason,
    });
  }

  /* ---------------------------------------------------------------- */
  /* Terminals                                                         */
  /* ---------------------------------------------------------------- */

  /** Keystrokes are sent in order: each one waits for the one before it. */
  /* ---------------- debugger ---------------- */

  private unsubscribeDebug: (() => void) | null = null;
  private debugSeq = 0;

  /**
   * Keeps the debugger in step with the app: breakpoints set while the
   * program runs reach it, and switching to another audit stops the program
   * and forgets the breakpoints of the old one.
   */
  private watchDebugger(): () => void {
    const offAudit = useAuditStore.subscribe((state) => {
      const id = state.current?.id ?? null;
      const debug = useDebugStore.getState();
      if (debug.auditId === id) return;
      if (debug.sessionId && debug.status !== 'exited') void this.stopDebug();
      debug.setAudit(id);
    });
    const offBreakpoints = useDebugStore.subscribe((state, previous) => {
      if (state.breakpoints === previous.breakpoints || !state.sessionId) return;
      if (state.status === 'idle' || state.status === 'exited') return;
      const files = new Set([...Object.keys(state.breakpoints), ...Object.keys(previous.breakpoints)]);
      for (const file of files) {
        const now = state.breakpoints[file] ?? [];
        const before = previous.breakpoints[file] ?? [];
        if (now.join(',') === before.join(',')) continue;
        this.debugSend({ cmd: 'setBreakpoints', file, lines: now });
      }
    });
    return () => {
      offAudit();
      offBreakpoints();
    };
  }

  /** Whether the current audit can be debugged: a folder the user opened. */
  canDebug(): boolean {
    const audit = useAuditStore.getState().current;
    return Boolean(audit && audit.source === 'local' && audit.status === 'ready' && !audit.sourcesDeleted);
  }

  /** Runs a Python file of the local project under the debugger. */
  async startDebug(program: string, stopOnEntry = false): Promise<void> {
    if (!this.auditId) return;
    const debug = useDebugStore.getState();
    if (debug.sessionId && (debug.status === 'running' || debug.status === 'paused' || debug.status === 'starting')) {
      await this.stopDebug();
    }
    useUiStore.getState().setBottomView('debug');
    useUiStore.getState().setTerminalOpen(true);
    debug.starting(program);
    debug.print({ kind: 'info', text: `Starting ${program}…` });
    try {
      const response = await this.client.request<{ id: string; python: string; version?: string; program: string }>('POST', '/api/debug', {
        auditId: this.auditId,
        program,
        breakpoints: useDebugStore.getState().breakpoints,
        stopOnEntry,
      });
      const now = useDebugStore.getState();
      // The program may already have ended (its exit event came first).
      if (now.status === 'exited' && now.sessionId === response.id) return;
      now.started(response.id, response.version ? `Python ${response.version}` : response.python);
      now.print({ kind: 'info', text: `Running with ${response.python}${response.version ? ` (Python ${response.version})` : ''}` });
    } catch (err) {
      useDebugStore.getState().failed(messageOf(err));
    }
  }

  async restartDebug(): Promise<void> {
    const program = useDebugStore.getState().program;
    if (program) await this.startDebug(program);
  }

  async stopDebug(): Promise<void> {
    const id = useDebugStore.getState().sessionId;
    if (!id) return;
    try {
      await this.client.request('DELETE', `/api/debug/${id}`);
    } catch {
      /* it may have ended on its own */
    }
  }

  /** Continue, step, or pause the debugged program. */
  debugCommand(cmd: 'continue' | 'next' | 'step' | 'out' | 'pause'): void {
    const debug = useDebugStore.getState();
    if (cmd === 'pause' ? debug.status !== 'running' : debug.status !== 'paused') return;
    this.debugSend({ cmd });
  }

  /** Shows the variables of another frame of the stack, and its code. */
  debugSelectFrame(index: number): void {
    const debug = useDebugStore.getState();
    const frame = debug.frames[index];
    if (!frame || debug.status !== 'paused') return;
    useDebugStore.setState({ frame: index });
    this.debugSend({ cmd: 'frame', frame: index });
    if (!frame.external) void this.showDebugLine(frame);
  }

  /** Evaluates an expression in the selected frame; the answer is printed in the console. */
  debugEvaluate(expression: string): void {
    const debug = useDebugStore.getState();
    const text = expression.trim();
    if (!text) return;
    debug.print({ kind: 'input', text });
    if (debug.status !== 'paused') {
      debug.print({ kind: 'error', text: 'Pause the program to evaluate an expression.' });
      return;
    }
    this.debugSeq += 1;
    this.debugSend({ cmd: 'evaluate', expression: text, frame: debug.frame, seq: this.debugSeq });
  }

  private debugSend(command: Record<string, unknown>): void {
    const id = useDebugStore.getState().sessionId;
    if (!id) return;
    void this.client.request('POST', `/api/debug/${id}/command`, command).catch((err: unknown) => {
      useDebugStore.getState().print({ kind: 'error', text: messageOf(err) });
    });
  }

  /** Opens the file of a frame at its line, unless it is already shown. */
  private async showDebugLine(frame: DebugFrame): Promise<void> {
    const file = useWorkspaceStore.getState().file;
    if (file?.path === frame.file) {
      useUiStore.getState().setActiveTab('code');
      return;
    }
    await this.openFile(frame.file, frame.line);
  }

  private onDebugEvent(sessionId: string, type: string, data: Record<string, unknown>): void {
    const debug = useDebugStore.getState();
    // Events of the session being started arrive before its id is known.
    if (debug.sessionId && debug.sessionId !== sessionId) return;
    if (!debug.sessionId) {
      if (debug.status !== 'starting') return;
      useDebugStore.setState({ sessionId });
    }
    if (type === 'debug.output') {
      const stream = data.stream === 'stderr' ? 'stderr' : 'stdout';
      debug.print({ kind: stream, text: String(data.text ?? '') });
      return;
    }
    if (type === 'debug.exit') {
      const code = typeof data.code === 'number' ? data.code : 0;
      debug.exited(code);
      useDebugStore.getState().print({ kind: 'info', text: `The program exited with code ${code}.` });
      return;
    }
    switch (data.event) {
      case 'stopped': {
        const frames = (data.frames as DebugFrame[] | undefined) ?? [];
        if (debug.status === 'starting') useDebugStore.setState({ status: 'running' });
        useDebugStore.getState().stopped({
          reason: String(data.reason ?? ''),
          description: String(data.description ?? ''),
          frames,
          frame: typeof data.frame === 'number' ? data.frame : 0,
          locals: data.locals as DebugVariable[] | undefined,
          globals: data.globals as DebugVariable[] | undefined,
        });
        if (data.reason === 'exception' && data.description) {
          useDebugStore.getState().print({ kind: 'error', text: `Stopped on an uncaught exception: ${String(data.description)}` });
        }
        // The first frame in the project's own code is the one to show.
        const shown = frames.findIndex((f) => !f.external);
        if (shown > 0) this.debugSelectFrame(shown);
        else if (shown === 0) void this.showDebugLine(frames[0]);
        useUiStore.getState().setBottomView('debug');
        useUiStore.getState().setTerminalOpen(true);
        break;
      }
      case 'continued':
        debug.continued();
        break;
      case 'scope':
        debug.scope(
          typeof data.frame === 'number' ? data.frame : 0,
          (data.locals as DebugVariable[] | undefined) ?? [],
          (data.globals as DebugVariable[] | undefined) ?? [],
        );
        break;
      case 'evaluated':
        if (data.error) debug.print({ kind: 'error', text: String(data.error) });
        else debug.print({ kind: 'result', text: String(data.value ?? '') });
        break;
      case 'breakpoints': {
        // A breakpoint the debugger could not set (a line past the end) is dropped.
        if (typeof data.file === 'string' && !data.external && Array.isArray(data.lines)) {
          const asked = debug.breakpoints[data.file] ?? [];
          const set = (data.lines as number[]).filter((n) => asked.includes(n));
          if (set.length !== asked.length) debug.setBreakpoints(data.file, set);
        }
        break;
      }
      default:
        break;
    }
  }

  private terminalQueue: Promise<unknown> = Promise.resolve();
  private terminalsReset = false;

  /** Starts a shell in the folder of the current audit (or the home folder). */
  async openTerminal(cols: number, rows: number): Promise<string | null> {
    const terminals = useTerminalStore.getState();
    // Shells left by an earlier window (a reload) are ended once, first.
    if (!this.terminalsReset) {
      this.terminalsReset = true;
      await this.client.request('DELETE', '/api/terminals').catch(() => undefined);
    }
    try {
      const response = await this.client.request<TerminalOpenResponse>('POST', '/api/terminals', {
        auditId: this.auditId ?? '',
        cols,
        rows,
      });
      const used = terminals.sessions.length + 1;
      const folder = response.cwd.split(/[\\/]/).filter(Boolean).pop() ?? 'shell';
      terminals.add({ id: response.id, title: `${folder} (${used})`, cwd: response.cwd });
      return response.id;
    } catch (err) {
      terminals.setError(messageOf(err));
      return null;
    }
  }

  terminalInput(id: string, data: string): void {
    this.terminalQueue = this.terminalQueue
      .then(() => this.client.request('POST', `/api/terminals/${id}/input`, { data }))
      .catch(() => undefined);
  }

  terminalResize(id: string, cols: number, rows: number): void {
    void this.client.request('POST', `/api/terminals/${id}/resize`, { cols, rows }).catch(() => undefined);
  }

  async closeTerminal(id: string): Promise<void> {
    useTerminalStore.getState().remove(id);
    terminalBus.forget(id);
    try {
      await this.client.request('DELETE', `/api/terminals/${id}`);
    } catch {
      /* it may have ended on its own */
    }
  }

  /** Names a conversation from its first exchange. The start of the question stays when this fails. */
  async nameThread(threadId: string, question: string, answer: string): Promise<void> {
    try {
      const response = await this.client.request<{ title: string }>('POST', '/api/chat/title', { question, answer });
      if (response.title) useChatStore.getState().autoTitleThread(threadId, response.title);
    } catch {
      /* the provisional title is fine */
    }
  }

  /** Ask a question about the current audit. */
  async ask(question: string): Promise<void> {
    const chat = useChatStore.getState();
    // The panel disables its input when no model is usable; this guards
    // every other caller.
    if (!chat.model.available) return;
    const selection = chat.selection;
    // The thread so far is the conversation the model continues.
    // Failed and stopped turns are not part of the conversation.
    // Long conversations are sent whole (up to a bound): the backend
    // summarises the older turns when they no longer fit.
    const history = chat.messages
      .filter((m) => m.content && !m.streaming && !m.error)
      .filter((m) => !(m.role === 'assistant' && (m.content.startsWith('The assistant could not answer:') || m.content === 'Stopped.')))
      .slice(-40)
      .map((m) => ({ role: m.role, content: m.content }));
    chat.pushUser(
      selection ? `${question}\n[${selection.file}:${selection.startLine}-${selection.endLine}]` : question,
    );
    chat.setSelection(null);
    try {
      const response = await this.client.request<JobResponse>('POST', '/api/chat', {
        auditId: this.auditId ?? '',
        question,
        history,
        ...(selection ? { selection } : {}),
      });
      chat.startAssistant(response.jobId);
    } catch (err) {
      chat.failAssistant('', messageOf(err));
    }
  }

  /** Replace a question with an edited one and ask again from there. */
  async editAndResend(messageId: string, text: string): Promise<void> {
    const chat = useChatStore.getState();
    if (chat.busy || !text.trim()) return;
    chat.truncateFrom(messageId);
    await this.ask(text.trim());
  }

  /** Ask the question behind a failed or unhelpful answer again. */
  async retry(assistantId: string): Promise<void> {
    const chat = useChatStore.getState();
    if (chat.busy) return;
    const messages = chat.messages;
    const at = messages.findIndex((m) => m.id === assistantId);
    const question = [...messages.slice(0, at)].reverse().find((m) => m.role === 'user');
    if (!question) return;
    chat.truncateFrom(question.id);
    // The selection reference appended to the question is display only.
    await this.ask(question.content.replace(/\n\[[^\]]+:\d+-\d+\]$/, ''));
  }

  /** Allow or refuse a tool call the model is waiting on. */
  async answerApproval(stepId: string, approved: boolean): Promise<void> {
    const chat = useChatStore.getState();
    const jobId = chat.jobId;
    if (!jobId) return;
    chat.upsertStep(jobId, { id: stepId, name: '', args: {}, status: approved ? 'running' : 'error', approval: approved ? 'approved' : 'denied' });
    try {
      await this.client.request('POST', `/api/chat/approvals/${encodeURIComponent(stepId)}`, { jobId, approved });
    } catch (err) {
      chat.upsertStep(jobId, { id: stepId, name: '', args: {}, status: 'error', error: messageOf(err) });
    }
  }

  /** Switch the assistant's web access on or off. */
  async setWebSearch(enabled: boolean): Promise<boolean> {
    try {
      const response = await this.client.request<{ enabled: boolean }>('POST', '/api/config/web-search', { enabled });
      return response.enabled;
    } catch (err) {
      useChatStore.setState({ error: messageOf(err) });
      return !enabled;
    }
  }

  /* ---------------------------------------------------------------- */
  /* Provider / health / settings                                      */
  /* ---------------------------------------------------------------- */

  async fetchProviders(): Promise<ProviderInfo[]> {
    try {
      const response = await this.client.request<{ providers: ProviderInfo[] }>(
        'GET',
        '/api/providers',
      );
      return response.providers ?? [];
    } catch {
      return [];
    }
  }

  async fetchHealth(): Promise<HealthResponse | null> {
    try {
      return await this.client.request<HealthResponse>('GET', '/api/health');
    } catch {
      return null;
    }
  }

  async fetchConfig(): Promise<Config | null> {
    try {
      const response = await this.client.request<{ config: Config }>('GET', '/api/config');
      return response.config;
    } catch {
      return null;
    }
  }

  async testProvider(request: ProviderRequest): Promise<ProviderInfo | null> {
    try {
      const response = await this.client.request<{ provider: ProviderInfo }>(
        'POST',
        '/api/providers/test',
        request,
      );
      return response.provider;
    } catch (err) {
      return { id: request.kind, kind: request.kind, ok: false, error: messageOf(err) };
    }
  }

  /** Switch the backend that scans and chat use. The choice is saved. */
  async setProvider(request: ProviderRequest): Promise<ProviderInfo | null> {
    try {
      const response = await this.client.request<{ provider: ProviderInfo }>(
        'POST',
        '/api/providers',
        request,
      );
      return response.provider;
    } catch (err) {
      return { id: request.kind, kind: request.kind, ok: false, error: messageOf(err) };
    }
  }

  /** Ask the toolbar to re-read the provider status. */
  refreshProviderChip(): Promise<void> {
    useUiStore.getState().bumpProviders();
    return Promise.resolve();
  }

  /** Re-read the list of audits, for the sources manager. */
  async refreshAudits(): Promise<void> {
    try {
      const response = await this.client.request<AuditsResponse>('GET', '/api/audits');
      useAuditStore.getState().setAudits(response.audits ?? []);
    } catch (err) {
      useAuditStore.getState().setError(messageOf(err));
    }
  }

  /** Delete the downloaded sources of any audit, not only the open one. */
  async deleteSourcesOf(auditId: string): Promise<{ freed: number; freedText: string } | null> {
    try {
      const response = await this.client.request<{ freed: number; freedText: string }>(
        'DELETE',
        `/api/audits/${auditId}/workspace`,
      );
      await this.refreshAudits();
      if (auditId === this.auditId) await this.selectAudit(auditId);
      return { freed: response.freed ?? 0, freedText: response.freedText ?? '' };
    } catch (err) {
      useAuditStore.getState().setError(messageOf(err));
      return null;
    }
  }

  /**
   * Fetches the models installed on the provider, together with the ones
   * the server is currently using.
   */
  async fetchModels(): Promise<ModelState | null> {
    try {
      const response = await this.client.request<ModelsResponse>('GET', '/api/models');
      return {
        models: response.models ?? [],
        chatModel: response.chatModel,
        scanModel: response.scanModel,
        embedModel: response.embedModel,
        numCtx: response.numCtx,
        keepAlive: response.keepAlive,
        error: response.error,
      };
    } catch {
      return null;
    }
  }

  /**
   * Applies a model selection.
   *
   * The change takes effect on the running server: the provider, the
   * detection pipeline and the chat all read the model names on every
   * request. It is written back to the configuration file as well.
   */
  async setModels(selection: ModelSelection): Promise<ModelState | null> {
    try {
      const response = await this.client.request<ModelsResponse>('POST', '/api/models', selection);
      return {
        models: response.models ?? [],
        chatModel: response.chatModel,
        scanModel: response.scanModel,
        embedModel: response.embedModel,
        numCtx: response.numCtx,
        keepAlive: response.keepAlive,
        error: response.error,
      };
    } catch (err) {
      useAuditStore.getState().setError(messageOf(err));
      return null;
    }
  }

  /**
   * Releases models from the provider's memory.
   *
   * Ollama keeps a model resident until no request has used it for the
   * keep-alive window; this asks for the memory back straight away.
   */
  async unloadModels(models?: string[]): Promise<string[] | null> {
    try {
      const response = await this.client.request<UnloadResponse>(
        'POST',
        '/api/models/unload',
        models && models.length > 0 ? { models } : {},
      );
      return response.unloaded ?? [];
    } catch (err) {
      useAuditStore.getState().setError(messageOf(err));
      return null;
    }
  }

  /**
   * Resolves a package name on the package index.
   *
   * The package picker uses this to turn the name the user typed into the
   * project it refers to, and to fill the version dropdown with the
   * versions that actually exist.
   */
  async lookupPackage(name: string, ecosystem: Ecosystem = 'pypi'): Promise<PackageLookup | null> {
    try {
      const response = await this.client.request<{ package: PackageLookup }>(
        'GET',
        `/api/packages?name=${encodeURIComponent(name)}&ecosystem=${ecosystem}`,
      );
      return response.package ?? null;
    } catch {
      return null;
    }
  }

  /**
   * Deletes the downloaded sources of the current audit.
   *
   * The findings and the report are kept: only the sandbox of downloaded
   * code is removed.
   */
  async deleteSources(): Promise<{ freed: number; freedText: string } | null> {
    if (!this.auditId) return null;
    const auditStore = useAuditStore.getState();
    auditStore.setBusy('cleanup');
    try {
      const response = await this.client.request<{ freed: number; freedText: string }>(
        'DELETE',
        `/api/audits/${this.auditId}/workspace`,
      );
      await this.reloadAudits();
      await this.selectAudit(this.auditId);
      return { freed: response.freed ?? 0, freedText: response.freedText ?? '' };
    } catch (err) {
      auditStore.setError(messageOf(err));
      return null;
    } finally {
      auditStore.setBusy(null);
    }
  }

  async fetchAdvisories(): Promise<Advisory[]> {
    if (!this.auditId) return [];
    try {
      const response = await this.client.request<{ advisories: Advisory[] }>(
        'GET',
        `/api/audits/${this.auditId}/advisories`,
      );
      return response.advisories ?? [];
    } catch {
      return [];
    }
  }

  async fetchIndexStatus(): Promise<IndexStatusResponse | null> {
    if (!this.auditId) return null;
    try {
      return await this.client.request<IndexStatusResponse>(
        'GET',
        `/api/audits/${this.auditId}/index/status`,
      );
    } catch {
      return null;
    }
  }

  async fetchChunks(path: string): Promise<ChunksResponse['chunks']> {
    if (!this.auditId) return [];
    try {
      const response = await this.client.request<ChunksResponse>(
        'GET',
        `/api/audits/${this.auditId}/chunks?path=${encodeURIComponent(path)}`,
      );
      return response.chunks ?? [];
    } catch {
      return [];
    }
  }

  /* ---------------------------------------------------------------- */
  /* Events                                                            */
  /* ---------------------------------------------------------------- */

  private onEvent(event: {
    type: string;
    auditId?: string;
    jobId?: string;
    phase?: string;
    percent?: number;
    message?: string;
    error?: string;
    finding?: Finding;
    data?: unknown;
  }): void {
    const auditStore = useAuditStore.getState();
    const chat = useChatStore.getState();

    switch (event.type) {
      case 'audit.progress': {
        const download = (event.data as { download?: DownloadEvent } | undefined)?.download;
        if (download) {
          auditStore.upsertDownload(
            {
              package: download.package,
              version: download.version,
              state: download.state,
              detail: download.detail ?? '',
              bytes: download.bytes,
            },
            download.total,
          );
        }
        // A package that only started downloading carries no percentage;
        // keep the bar where it is instead of dropping it back to zero.
        const percent = event.percent || (download ? auditStore.percent : 0);
        auditStore.setProgress(event.phase ?? 'working', percent, event.message ?? '');
        break;
      }
      case 'audit.done': {
        auditStore.setProgress('complete', 1, 'Audit complete');
        auditStore.setProgressVisible(false);
        auditStore.setBusy(null);
        if (event.auditId && event.auditId !== this.auditId) {
          this.auditId = event.auditId;
        }
        void this.selectAudit(this.auditId ?? '');
        break;
      }
      case 'audit.error': {
        auditStore.setError(event.error ?? 'The audit failed.');
        auditStore.setProgressVisible(false);
        auditStore.setBusy(null);
        break;
      }
      case 'scan.done': {
        if (event.jobId) this.finishScan(event.jobId);
        break;
      }
      case 'scan.error': {
        if (event.jobId) this.finishScan(event.jobId, event.error ?? 'The scan failed.');
        break;
      }
      case 'scan.progress': {
        auditStore.setProgress(
          event.phase ?? 'scanning',
          event.percent ?? 0,
          event.message ?? '',
        );
        break;
      }
      case 'finding.created': {
        if (event.finding) {
          useFindingsStore.getState().upsert(event.finding);
        }
        break;
      }
      case 'chat.token': {
        const data = event.data as ChatTokenData | undefined;
        if (event.jobId && data?.delta) {
          chat.appendToken(event.jobId, data.delta);
        }
        break;
      }
      case 'terminal.data': {
        const data = event.data as { data?: string } | undefined;
        if (event.jobId && data?.data) {
          const raw = atob(data.data);
          const bytes = new Uint8Array(raw.length);
          for (let i = 0; i < raw.length; i += 1) bytes[i] = raw.charCodeAt(i);
          terminalBus.emit(event.jobId, bytes);
        }
        break;
      }
      case 'debug.event':
      case 'debug.output':
      case 'debug.exit': {
        if (event.jobId) this.onDebugEvent(event.jobId, event.type, (event.data as Record<string, unknown> | undefined) ?? {});
        break;
      }
      case 'terminal.exit': {
        const data = event.data as { code?: number } | undefined;
        if (event.jobId) useTerminalStore.getState().markExited(event.jobId, data?.code ?? 0);
        break;
      }
      case 'chat.step': {
        const step = event.data as
          | { kind: string; think?: string; id?: string; name?: string; args?: Record<string, unknown>; output?: string; error?: string; summary?: string; approved?: boolean }
          | undefined;
        if (!event.jobId || !step) break;
        if (step.kind === 'thinking' && step.think) chat.appendThinking(event.jobId, step.think);
        else if (step.kind === 'compacting') chat.setCompacting(event.jobId, true);
        else if (step.kind === 'compacted') {
          chat.setCompacting(event.jobId, false, step.error ? `The conversation could not be summarised (${step.error}); older turns were left out.` : `Conversation compacted: ${step.output ?? 'earlier messages summarised'}.`);
        } else if (step.kind === 'approval' && step.id) {
          chat.upsertStep(event.jobId, { id: step.id, name: step.name ?? 'tool', args: step.args ?? {}, status: 'waiting', summary: step.summary, approval: 'pending' });
        } else if (step.kind === 'approval_result' && step.id) {
          chat.upsertStep(event.jobId, { id: step.id, name: step.name ?? 'tool', args: {}, status: step.approved ? 'running' : 'error', approval: step.approved ? 'approved' : 'denied' });
        }
        else if (step.kind === 'tool' && step.id) {
          chat.upsertStep(event.jobId, { id: step.id, name: step.name ?? 'tool', args: step.args ?? {}, status: 'running' });
        } else if (step.kind === 'tool_result' && step.id) {
          chat.upsertStep(event.jobId, {
            id: step.id,
            name: step.name ?? 'tool',
            args: {},
            status: step.error ? 'error' : 'done',
            output: step.output,
            error: step.error,
          });
        }
        break;
      }
      case 'chat.done': {
        const data = event.data as ChatDoneData | undefined;
        if (event.jobId) {
          const thread = chat.activeThread();
          chat.finishAssistant(
            event.jobId,
            data?.citations ?? [],
            data?.chunksUsed ?? [],
          );
          // The first exchange of a thread names it.
          const first = thread?.messages.find((m) => m.role === 'user');
          if (thread && thread.autoTitle && thread.messages.filter((m) => m.role === 'user').length === 1 && first) {
            void this.nameThread(thread.id, first.content, (data as { answer?: string } | undefined)?.answer ?? '');
          }
        }
        break;
      }
      case 'chat.error': {
        if (event.jobId) {
          chat.failAssistant(event.jobId, event.error ?? 'The chat request failed.');
        }
        break;
      }
      default:
        break;
    }
  }
}

/**
 * The number of directory levels that are expanded when a tree is first
 * shown.
 *
 * The tree of a big package has hundreds of directories. Expanding all of
 * them builds thousands of rows for a tree that is mostly empty, so the
 * deep levels stay folded until they are opened.
 */
const MAX_EXPANDED_DEPTH = 4;

/** Collect the directory paths to expand when a tree is first shown. */
export function collectDirectories(node: FileNode | null): string[] {
  const paths: string[] = [];
  const walk = (current: FileNode, depth: number): void => {
    if (current.type !== 'dir') return;
    if (depth < MAX_EXPANDED_DEPTH) paths.push(current.path);
    for (const child of current.children ?? []) walk(child, depth + 1);
  };
  if (node) walk(node, 0);
  return paths;
}

/** Convenience for views that need the audit id. */
export function currentAudit(): Audit | null {
  return useAuditStore.getState().current;
}

export { useFindingsStore, useGraphStore, useUiStore, useWorkspaceStore, useChatStore, useAuditStore };
export type { Patch, ProviderInfo };
