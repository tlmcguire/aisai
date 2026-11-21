/**
 * TypeScript mirror of the AISAI Go backend data model.
 *
 * MIRRORS: internal/model/model.go  (and docs/API.md for the request/response
 * envelopes, which are not Go structs but are part of the same contract).
 *
 * Rules for this file:
 *   - Field names and JSON tags are byte-for-byte the Go ones. A contract test
 *     fails CI if this file and model.go drift.
 *   - `Severity` and `AuditStatus` are closed string unions, matching the Go
 *     constant sets exactly.
 *   - Go `time.Time` fields are RFC 3339 UTC strings (per docs/API.md).
 *   - Go `*time.Time` (pointer) fields are optional strings.
 *   - Go `interface{}` is `unknown`.
 *   - Go `[]string` / `[]T` are `T[]`.
 *
 * KNOWN GAP: docs/API.md refers to `model.Patch` and `model.Change`, but
 * internal/model/model.go does not define them. The two interfaces below are
 * derived from the JSON samples in docs/API.md and are marked as such.
 */

/* ------------------------------------------------------------------ */
/* Enumerations                                                        */
/* ------------------------------------------------------------------ */

/** Mirrors model.Severity and its severityRank map. */
export type Severity = 'critical' | 'high' | 'medium' | 'low' | 'info';

/** Mirrors model.AuditStatus. */
export type AuditStatus =
  | 'pending'
  | 'acquiring'
  | 'indexing'
  | 'scanning'
  | 'ready'
  | 'failed';

/** Mirrors model.FindingState constants (open | confirmed | suppressed | fixed). */
export type FindingState = 'open' | 'confirmed' | 'suppressed' | 'fixed';

/** Mirrors model.Reachability constants. */
export type Reachability = 'reachable' | 'unreachable' | 'unknown';

/** Mirrors model.ChunkType constants. */
export type ChunkType = 'module' | 'file' | 'class' | 'function' | 'block';

/** Mirrors model.EventType constants. */
export type EventType =
  | 'audit.progress'
  | 'audit.done'
  | 'audit.error'
  | 'scan.progress'
  | 'finding.created'
  | 'chat.token'
  | 'chat.done'
  | 'chat.error';

/** Mirrors model.PURL — canonical form `pkg:pypi/<name>@<version>`. */
export type PURL = string;

/** Mirrors model.FileNode.Type. */
export type FileNodeType = 'dir' | 'file';

export const SEVERITIES: readonly Severity[] = ['critical', 'high', 'medium', 'low', 'info'];

export const FINDING_STATES: readonly FindingState[] = [
  'open',
  'confirmed',
  'suppressed',
  'fixed',
];

export const EVENT_TYPES: readonly EventType[] = [
  'audit.progress',
  'audit.done',
  'audit.error',
  'scan.progress',
  'finding.created',
  'chat.token',
  'chat.done',
  'chat.error',
];

/** Mirrors model.severityRank — higher is worse. */
export const SEVERITY_RANK: Record<Severity, number> = {
  critical: 5,
  high: 4,
  medium: 3,
  low: 2,
  info: 1,
};

/* ------------------------------------------------------------------ */
/* Core model types                                                    */
/* ------------------------------------------------------------------ */

/** Mirrors model.Advisory. */
export interface Advisory {
  id: string;
  source: string;
  summary: string;
  severity: Severity;
  cwe?: string[];
  fixedIn?: string[];
  url?: string;
}

/** Mirrors model.Package. */
export interface Package {
  purl: PURL;
  name: string;
  version: string;
  ecosystem: string;
  direct: boolean;
  localPath?: string;
  summary?: string;
  license?: string;
  homepage?: string;
  vulnerabilities?: Advisory[];
  maxSeverity: Severity;
}

/** Mirrors model.GraphNode. */
export interface GraphNode {
  id: PURL;
  label: string;
  name: string;
  version: string;
  direct: boolean;
  maxSeverity: Severity;
  findingCount: number;
  vulnCount: number;
}

/** Mirrors model.GraphEdge — source depends on target. */
export interface GraphEdge {
  source: PURL;
  target: PURL;
}

/** Mirrors model.Graph. */
export interface Graph {
  nodes: GraphNode[];
  edges: GraphEdge[];
}

/** Mirrors model.Evidence. */
export interface Evidence {
  source?: string;
  sink?: string;
  flow?: string[];
  note?: string;
}

/** Mirrors model.Finding. */
export interface Finding {
  id: string;
  rule: string;
  tool: string;
  cwe?: string;
  severity: Severity;
  confidence: number;
  file: string;
  startLine: number;
  endLine: number;
  code?: string;
  message: string;
  evidence: Evidence;
  remediation?: string;
  patch?: string;
  reachability?: Reachability;
  state: FindingState;
  suppressedUntil?: string;
  package?: PURL;
  verified: boolean;
  verifierNote?: string;
}

/** Mirrors model.Chunk. */
export interface Chunk {
  id: string;
  auditId: string;
  file: string;
  startLine: number;
  endLine: number;
  type: ChunkType;
  symbolPath: string;
  docstring?: string;
  content: string;
  parentId?: string;
  calls?: string[];
  imports?: string[];
  language?: string;
}

/** Mirrors model.Audit. */
export interface Audit {
  id: string;
  package: string;
  version?: string;
  status: AuditStatus;
  phase?: string;
  progress: number;
  message?: string;
  error?: string;
  createdAt: string;
  updatedAt: string;
  root: string;
  fileCount: number;
  chunkCount: number;
  findingCount: number;
  /** The size of the downloaded sources of the audit, in bytes. */
  sandboxBytes: number;
  /** Whether the downloaded sources were deleted. */
  sourcesDeleted: boolean;
}

/** Mirrors model.PackageLookup — a package name resolved on the index. */
export interface PackageLookup {
  name: string;
  summary?: string;
  latest?: string;
  versions?: string[];
  error?: string;
}

/** The response of the package lookup endpoint. */
export interface PackageLookupResponse {
  package: PackageLookup;
}

/** Mirrors model.FileNode. */
export interface FileNode {
  name: string;
  path: string;
  type: FileNodeType;
  size?: number;
  children?: FileNode[];
  findingCount?: number;
  maxSeverity?: Severity;
}

/** Mirrors model.FileContent. */
export interface FileContent {
  path: string;
  language: string;
  lines: string[];
  findings?: Finding[];
}

/** Mirrors model.ASTNode. */
export interface ASTNode {
  id: string;
  type: string;
  label: string;
  startLine: number;
  endLine: number;
  depth: number;
  parentId?: string;
  symbol?: string;
}

/** Mirrors model.ASTEdge — source is the parent, target the child. */
export interface ASTEdge {
  source: string;
  target: string;
}

/** Mirrors model.AST. */
export interface AST {
  path: string;
  nodes: ASTNode[];
  edges: ASTEdge[];
  /** The language the file was parsed with. */
  language?: string;
}

/** Mirrors model.ProviderInfo. */
export interface ProviderInfo {
  id: string;
  kind: string;
  url?: string;
  ok: boolean;
  models?: string[];
  error?: string;
}

/** Mirrors model.Config — never contains API keys. */
export interface Config {
  workspace: string;
  providers: ProviderInfo[];
  ollamaUrl: string;
  chatModel: string;
  embedModel: string;
  scanModel: string;
  numCtx: number;
  /** How long Ollama keeps a model in memory after the last request. */
  keepAlive?: string;
}

/** Mirrors model.ModelInfo — one model installed on the Ollama server. */
export interface ModelInfo {
  name: string;
  size: number;
  sizeText: string;
  family?: string;
  parameterSize?: string;
  quantizationLevel?: string;
  modifiedAt?: string;
}

/** The response of the model endpoints: installed and selected models. */
export interface ModelState {
  models: ModelInfo[];
  chatModel: string;
  scanModel: string;
  embedModel: string;
  numCtx: number;
  keepAlive: string;
  error?: string;
}

/** The body of POST /api/models. Empty names leave a role unchanged. */
export interface ModelSelection {
  chatModel?: string;
  scanModel?: string;
  embedModel?: string;
  numCtx?: number;
  keepAlive?: string;
}

/** Mirrors model.Event. */
export interface Event {
  type: EventType;
  auditId?: string;
  jobId?: string;
  phase?: string;
  percent?: number;
  message?: string;
  error?: string;
  finding?: Finding;
  data?: unknown;
}

/**
 * NOT in model.go — inferred from docs/API.md `POST /api/audits/{id}/propose-fix`.
 */
export interface Patch {
  findingId: string;
  unified: string;
  valid: boolean;
  error: string;
  summary: string;
}

/**
 * NOT in model.go — inferred from docs/API.md `GET /api/audits/{id}/changes`.
 */
export interface Change {
  path: string;
  unified: string;
  appliedAt: string;
}

/* ------------------------------------------------------------------ */
/* HTTP envelopes (docs/API.md)                                        */
/* ------------------------------------------------------------------ */

/** The error body every non-2xx response carries. */
export interface ApiErrorBody {
  error: {
    code: string;
    message: string;
  };
}

/** Error codes documented in docs/API.md. */
export type ApiErrorCode =
  | 'bad_request'
  | 'not_found'
  | 'path_escape'
  | 'conflict'
  | 'upstream_unavailable'
  | 'disabled'
  | 'internal';

export interface HealthResponse {
  ok: boolean;
  version: string;
  workspace: string;
  uptimeSeconds: number;
  providers: ProviderInfo[];
}

export interface ConfigResponse {
  config: Config;
}

export interface ProvidersResponse {
  providers: ProviderInfo[];
}

export interface ProviderTestResponse {
  provider: ProviderInfo;
}

export type ModelsResponse = ModelState;

export interface UnloadResponse {
  unloaded: string[];
}

export interface AuditsResponse {
  audits: Audit[];
}

export interface AuditResponse {
  audit: Audit;
}

export interface GraphResponse {
  graph: Graph;
}

export interface FilesResponse {
  tree: FileNode;
}

export interface FileResponse {
  file: FileContent;
}

export interface AstResponse {
  ast: AST;
}

export interface ChunksResponse {
  chunks: Chunk[];
}

export interface FindingsResponse {
  findings: Finding[];
  total: number;
}

export interface FindingResponse {
  finding: Finding;
}

export interface FindingStateResponse {
  finding: Finding;
}

export interface PatchResponse {
  patch: Patch;
}

export interface ApplyPatchResponse {
  applied: boolean;
  rescan: {
    resolved: string[];
    introduced: string[];
    unchanged: number;
  };
}

export interface ChangesResponse {
  changes: Change[];
}

export interface AdvisoriesResponse {
  advisories: Advisory[];
  mode?: string;
}

export interface AdvisoryStatusResponse {
  mode: string;
  osv: {
    available: boolean;
    updatedAt: string;
    count: number;
  };
}

export interface JobResponse {
  jobId: string;
}

export interface IndexStatusResponse {
  ready: boolean;
  phase: string;
  progress: number;
  chunks: number;
  embedded: number;
  keywordOnly: boolean;
  embedModel: string;
}

/* ------------------------------------------------------------------ */
/* Chat payloads                                                       */
/* ------------------------------------------------------------------ */

/** Payload of a `chat.token` event. */
export interface ChatTokenData {
  delta: string;
}

/** Payload of a `chat.done` event. */
export interface ChatDoneData {
  citations: string[];
  chunksUsed: string[];
}

/** One turn of chat history sent to `POST /api/chat`. */
export interface ChatTurn {
  role: 'user' | 'assistant';
  content: string;
}

/* ------------------------------------------------------------------ */
/* Small helpers mirroring Go methods                                 */
/* ------------------------------------------------------------------ */

/** Mirrors model.PURL.Name(). */
export function purlName(purl: PURL): string {
  const s = purl.startsWith('pkg:pypi/') ? purl.slice('pkg:pypi/'.length) : purl;
  const at = s.indexOf('@');
  return at >= 0 ? s.slice(0, at) : s;
}

/** Mirrors model.PURL.Version(). */
export function purlVersion(purl: PURL): string {
  const at = purl.indexOf('@');
  return at >= 0 ? purl.slice(at + 1) : '';
}

/** Mirrors model.PackageURL(). */
export function packageURL(name: string, version: string): PURL {
  let s = `pkg:pypi/${name.toLowerCase()}`;
  if (version) s += `@${version}`;
  return s;
}

/** Mirrors model.MaxSeverity(). */
export function maxSeverity(a: Severity, b: Severity): Severity {
  return SEVERITY_RANK[b] > SEVERITY_RANK[a] ? b : a;
}
