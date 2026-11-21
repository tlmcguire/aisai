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
  Patch,
  PatchResponse,
  ProviderInfo,
} from './api/types';
import { useAuditStore } from './state/audit';
import { useChatStore } from './state/chat';
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
    await this.reloadAudits();
  }

  dispose(): void {
    this.unsubscribe?.();
    this.unsubscribe = null;
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
  async startAudit(packageName: string, version: string): Promise<void> {
    const auditStore = useAuditStore.getState();
    auditStore.setError(null);
    auditStore.setBusy('audit');
    auditStore.setProgress('queued', 0, `Starting the audit of ${packageName}`);
    try {
      const body: Record<string, string> = { package: packageName };
      if (version) body.version = version;
      const response = await this.client.request<AuditResponse>('POST', '/api/audits', body);
      auditStore.setCurrent(response.audit);
      this.auditId = response.audit?.id ?? this.auditId;
      await this.reloadAudits();
    } catch (err) {
      auditStore.setError(messageOf(err));
      auditStore.setProgressVisible(false);
    } finally {
      auditStore.setBusy(null);
    }
  }

  /** Run a scan (sast or llm) over the current audit. */
  async runScan(engine: 'sast' | 'llm'): Promise<void> {
    if (!this.auditId) return;
    const auditStore = useAuditStore.getState();
    auditStore.setError(null);
    auditStore.setBusy(`scan:${engine}`);
    auditStore.setProgress(`${engine} scan`, 0, 'Starting the scan');
    try {
      await this.client.request<JobResponse>(
        'POST',
        `/api/audits/${this.auditId}/scan`,
        engine === 'llm' ? { engine } : { engine: 'sast' },
      );
    } catch (err) {
      auditStore.setError(messageOf(err));
      auditStore.setProgressVisible(false);
    } finally {
      auditStore.setBusy(null);
    }
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
      await this.loadFindings();
      return response;
    } catch (err) {
      auditStore.setError(messageOf(err));
      return null;
    } finally {
      auditStore.setBusy(null);
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

  /** Ask a question about the current audit. */
  async ask(question: string): Promise<void> {
    const chat = useChatStore.getState();
    chat.pushUser(question);
    try {
      const response = await this.client.request<JobResponse>('POST', '/api/chat', {
        auditId: this.auditId ?? '',
        question,
        history: [],
      });
      chat.startAssistant(response.jobId);
    } catch (err) {
      chat.failAssistant('', messageOf(err));
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

  async testProvider(id: string, url: string, model: string): Promise<ProviderInfo | null> {
    try {
      const response = await this.client.request<{ provider: ProviderInfo }>(
        'POST',
        '/api/providers/test',
        { id, url, model },
      );
      return response.provider;
    } catch (err) {
      useAuditStore.getState().setError(messageOf(err));
      return null;
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
        auditStore.setProgress(
          event.phase ?? 'working',
          event.percent ?? 0,
          event.message ?? '',
        );
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
      case 'chat.done': {
        const data = event.data as ChatDoneData | undefined;
        if (event.jobId) {
          chat.finishAssistant(
            event.jobId,
            data?.citations ?? [],
            data?.chunksUsed ?? [],
          );
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

/** Collect every directory path in a file tree (used to expand it initially). */
export function collectDirectories(node: FileNode | null): string[] {
  const paths: string[] = [];
  const walk = (current: FileNode): void => {
    if (current.type !== 'dir') return;
    paths.push(current.path);
    for (const child of current.children ?? []) walk(child);
  };
  if (node) walk(node);
  return paths;
}

/** Convenience for views that need the audit id. */
export function currentAudit(): Audit | null {
  return useAuditStore.getState().current;
}

export { useFindingsStore, useGraphStore, useUiStore, useWorkspaceStore, useChatStore, useAuditStore };
export type { Patch, ProviderInfo };
