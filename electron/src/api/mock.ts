/**
 * Mock AISAI backend.
 *
 * Serves fixture responses for every endpoint in docs/API.md so the UI is fully
 * demonstrable before the Go sidecar exists. The API surface is identical to
 * the live client: `request(method, path, body)` plus an event stream.
 *
 * The mock is selected by the renderer when `window.aisai.mode === 'mock'`
 * (i.e. the Go sidecar binary was not found).
 */

import type {
  Advisory,
  AdvisoriesResponse,
  ApplyPatchResponse,
  AST,
  AstResponse,
  Audit,
  AuditResponse,
  AuditsResponse,
  Change,
  ChangesResponse,
  ChatDoneData,
  ChunksResponse,
  Config,
  ConfigResponse,
  Event,
  FileContent,
  FileNode,
  FileResponse,
  FilesResponse,
  Finding,
  FindingState,
  FindingStateResponse,
  FindingsResponse,
  Graph,
  GraphResponse,
  HealthResponse,
  IndexStatusResponse,
  JobResponse,
  ModelInfo,
  ModelSelection,
  ModelsResponse,
  PackageLookup,
  Ecosystem,
  PackageLookupResponse,
  Patch,
  PatchResponse,
  ProviderInfo,
  ProvidersResponse,
  Severity,
  UnloadResponse,
} from './types';

/* ------------------------------------------------------------------ */
/* Fixtures                                                            */
/* ------------------------------------------------------------------ */

const NOW = '2026-11-21T09:00:00Z';

const PROVIDERS: ProviderInfo[] = [
  {
    id: 'ollama',
    kind: 'ollama',
    url: 'http://localhost:11434',
    ok: true,
    models: ['qwen3.5:4b', 'qwen2.5-coder:7b', 'embeddinggemma'],
  },
  {
    id: 'openai',
    kind: 'openai',
    ok: false,
    error: 'no API key configured (mock)',
  },
];

const CONFIG: Config = {
  workspace: '/Users/tyler/Library/Application Support/AISAI/audits/a1',
  providers: PROVIDERS,
  ollamaUrl: 'http://localhost:11434',
  chatModel: 'qwen3.5:4b',
  embedModel: 'embeddinggemma',
  scanModel: 'qwen3.5:4b',
  numCtx: 8192,
  keepAlive: '3m',
};

/** The models the mock pretends are installed on the Ollama server. */
const INSTALLED_MODELS: ModelInfo[] = [
  {
    name: 'qwen3.5:4b',
    size: 3_300_000_000,
    sizeText: '3.1GB',
    family: 'qwen3',
    parameterSize: '4.0B',
    quantizationLevel: 'Q4_K_M',
    modifiedAt: '2026-11-01T10:00:00Z',
  },
  {
    name: 'qwen2.5-coder:7b',
    size: 4_700_000_000,
    sizeText: '4.4GB',
    family: 'qwen2',
    parameterSize: '7.6B',
    quantizationLevel: 'Q4_K_M',
    modifiedAt: '2026-10-14T10:00:00Z',
  },
  {
    name: 'embeddinggemma',
    size: 300_000_000,
    sizeText: '287.0MB',
    family: 'gemma',
    parameterSize: '300M',
    quantizationLevel: 'F16',
    modifiedAt: '2026-09-30T10:00:00Z',
  },
];

const PACKAGE_FIXTURES: PackageLookup[] = [
  {
    name: 'express',
    ecosystem: 'npm',
    summary: 'Fast, unopinionated, minimalist web framework.',
    latest: '4.21.1',
    versions: ['4.21.1', '4.21.0', '4.20.0', '4.19.2'],
  },
  {
    name: 'serde',
    ecosystem: 'cargo',
    summary: 'A generic serialization/deserialization framework.',
    latest: '1.0.215',
    versions: ['1.0.215', '1.0.214', '1.0.210'],
  },
  {
    name: 'flask',
    summary: 'A simple framework for building complex web applications.',
    latest: '3.1.0',
    versions: ['3.1.0', '3.0.3', '3.0.2', '3.0.1', '3.0.0', '2.3.3', '2.2.5', '2.0.0'],
  },
  {
    name: 'requests',
    summary: 'Python HTTP for Humans.',
    latest: '2.32.3',
    versions: ['2.32.3', '2.32.2', '2.31.0', '2.30.0'],
  },
  {
    name: 'numpy',
    summary: 'The fundamental package for scientific computing with Python.',
    latest: '2.1.3',
    versions: ['2.1.3', '2.1.2', '2.0.2', '1.26.4'],
  },
  {
    name: 'pandas',
    summary: 'Powerful data structures for data analysis and time series.',
    latest: '2.2.3',
    versions: ['2.2.3', '2.2.2', '2.1.4', '2.0.3'],
  },
  {
    name: 'django',
    summary: 'A high-level Python web framework that encourages rapid development.',
    latest: '5.1.4',
    versions: ['5.1.4', '5.1.3', '5.0.9', '4.2.20'],
  },
];

const AUDIT: Audit = {
  id: 'a1',
  package: 'flask',
  version: '2.0.0',
  status: 'ready',
  phase: 'complete',
  progress: 1,
  message: 'Audit complete',
  createdAt: '2026-11-21T08:52:00Z',
  updatedAt: NOW,
  root: '/Users/tyler/Library/Application Support/AISAI/audits/a1',
  fileCount: 9,
  chunkCount: 41,
  findingCount: 12,
  sandboxBytes: 4_812_416,
  sourcesDeleted: false,
};

const GRAPH: Graph = {
  nodes: [
    {
      id: 'pkg:pypi/flask@2.0.0',
      label: 'flask\nv2.0.0',
      name: 'flask',
      version: '2.0.0',
      direct: true,
      root: true,
      maxSeverity: 'critical',
      findingCount: 9,
      vulnCount: 1,
    },
    {
      id: 'pkg:pypi/werkzeug@2.0.0',
      label: 'werkzeug\nv2.0.0',
      name: 'werkzeug',
      version: '2.0.0',
      direct: true,
      maxSeverity: 'high',
      findingCount: 3,
      vulnCount: 1,
    },
    {
      id: 'pkg:pypi/jinja2@3.0.1',
      label: 'jinja2\nv3.0.1',
      name: 'jinja2',
      version: '3.0.1',
      direct: false,
      maxSeverity: 'medium',
      findingCount: 1,
      vulnCount: 0,
    },
    {
      id: 'pkg:pypi/itsdangerous@2.0.0',
      label: 'itsdangerous\nv2.0.0',
      name: 'itsdangerous',
      version: '2.0.0',
      direct: false,
      maxSeverity: 'medium',
      findingCount: 1,
      vulnCount: 0,
    },
    {
      id: 'pkg:pypi/click@8.0.0',
      label: 'click\nv8.0.0',
      name: 'click',
      version: '8.0.0',
      direct: false,
      maxSeverity: 'info',
      findingCount: 0,
      vulnCount: 0,
    },
    {
      id: 'pkg:pypi/markupsafe@2.0.1',
      label: 'markupsafe\nv2.0.1',
      name: 'markupsafe',
      version: '2.0.1',
      direct: false,
      maxSeverity: 'info',
      findingCount: 0,
      vulnCount: 0,
    },
    {
      id: 'pkg:pypi/urllib3@1.26.0',
      label: 'urllib3\nv1.26.0',
      name: 'urllib3',
      version: '1.26.0',
      direct: false,
      maxSeverity: 'high',
      findingCount: 0,
      vulnCount: 1,
    },
    {
      id: 'pkg:pypi/requests@2.25.0',
      label: 'requests\nv2.25.0',
      name: 'requests',
      version: '2.25.0',
      direct: false,
      maxSeverity: 'critical',
      findingCount: 0,
      vulnCount: 2,
    },
  ],
  edges: [
    { source: 'pkg:pypi/flask@2.0.0', target: 'pkg:pypi/werkzeug@2.0.0' },
    { source: 'pkg:pypi/flask@2.0.0', target: 'pkg:pypi/jinja2@3.0.1' },
    { source: 'pkg:pypi/flask@2.0.0', target: 'pkg:pypi/itsdangerous@2.0.0' },
    { source: 'pkg:pypi/flask@2.0.0', target: 'pkg:pypi/click@8.0.0' },
    { source: 'pkg:pypi/jinja2@3.0.1', target: 'pkg:pypi/markupsafe@2.0.1' },
    { source: 'pkg:pypi/requests@2.25.0', target: 'pkg:pypi/urllib3@1.26.0' },
    { source: 'pkg:pypi/requests@2.25.0', target: 'pkg:pypi/werkzeug@2.0.0' },
    { source: 'pkg:pypi/requests@2.25.0', target: 'pkg:pypi/click@8.0.0' },
  ],
};

const FINDINGS: Finding[] = [
  {
    id: 'cwe-798-3f9a2c1d04b7e',
    rule: 'hardcoded-secret',
    tool: 'sast',
    cwe: 'CWE-798',
    severity: 'critical',
    confidence: 0.97,
    file: 'flask/security.py',
    startLine: 12,
    endLine: 12,
    code: 'DEFAULT_SECRET = "super-secret-key-change-me"',
    message:
      'A secret is hard-coded in the source and is used as the fallback signing key when SECRET_KEY is unset.',
    evidence: {
      source: 'flask/app.py:88',
      sink: 'flask/security.py:12',
      flow: ['app.py:88', 'security.py:44', 'security.py:12'],
      note: 'Default secret is used when SECRET_KEY is unset.',
    },
    remediation:
      'Load the secret from configuration or the environment and compare it with hmac.compare_digest.',
    patch: '--- a/flask/security.py\n+++ b/flask/security.py\n',
    reachability: 'reachable',
    state: 'open',
    package: 'pkg:pypi/flask@2.0.0',
    verified: true,
    verifierNote: 'Confirmed by re-running the rule with the literal present.',
  },
  {
    id: 'cwe-798-9d0e5b3a77112',
    rule: 'hardcoded-secret',
    tool: 'sast',
    cwe: 'CWE-798',
    severity: 'critical',
    confidence: 0.91,
    file: 'flask/sessions.py',
    startLine: 24,
    endLine: 26,
    code: 'default_salt = b"super-secret-salt"',
    message: 'Session signing salt is a hard-coded constant.',
    evidence: { sink: 'flask/sessions.py:24' },
    remediation: 'Generate a random salt per installation and persist it.',
    reachability: 'reachable',
    state: 'open',
    package: 'pkg:pypi/flask@2.0.0',
    verified: false,
  },
  {
    id: 'cwe-327-1a4b90d33e8c',
    rule: 'weak-hash-md5',
    tool: 'sast',
    cwe: 'CWE-327',
    severity: 'high',
    confidence: 0.95,
    file: 'flask/helpers.py',
    startLine: 61,
    endLine: 63,
    code: 'return hashlib.md5(value.encode()).hexdigest()',
    message: 'MD5 is used to derive a cache key; MD5 is collision-prone and must not be used for security decisions.',
    evidence: { sink: 'flask/helpers.py:61' },
    remediation: 'Use SHA-256 (hashlib.sha256) or HMAC-SHA256.',
    reachability: 'reachable',
    state: 'open',
    package: 'pkg:pypi/flask@2.0.0',
    verified: true,
  },
  {
    id: 'cwe-330-6c2b8f0a17d9',
    rule: 'weak-random',
    tool: 'sast',
    cwe: 'CWE-330',
    severity: 'high',
    confidence: 0.88,
    file: 'flask/security.py',
    startLine: 38,
    endLine: 38,
    code: 'token = "".join(random.choice(alphabet) for _ in range(length))',
    message: 'Security token generated with a non-cryptographic random source.',
    evidence: { sink: 'flask/security.py:88' },
    remediation: 'Use the secrets module (secrets.token_urlsafe).',
    reachability: 'reachable',
    state: 'open',
    package: 'pkg:pypi/flask@2.0.0',
    verified: false,
  },
  {
    id: 'cwe-287-2b7c1e9f44310',
    rule: 'missing-auth-check',
    tool: 'sast',
    cwe: 'CWE-287',
    severity: 'high',
    confidence: 0.72,
    file: 'flask/app.py',
    startLine: 143,
    endLine: 151,
    code: 'def delete_account(self, user_id):',
    message: 'Destructive account endpoint has no authentication decorator.',
    evidence: {
      source: 'flask/app.py:143',
      note: 'No @login_required or equivalent guard found on the call path.',
    },
    remediation: 'Require authentication and authorization before destructive operations.',
    reachability: 'unknown',
    state: 'open',
    package: 'pkg:pypi/flask@2.0.0',
    verified: false,
  },
  {
    id: 'cwe-79-77e0c1a55b312',
    rule: 'reflected-xss',
    tool: 'llm',
    cwe: 'CWE-79',
    severity: 'medium',
    confidence: 0.64,
    file: 'flask/helpers.py',
    startLine: 104,
    endLine: 106,
    code: 'return Markup("<b>%s</b>" % value)',
    message: 'User-controlled value is interpolated into HTML without escaping.',
    evidence: { source: 'request.args', sink: 'flask/helpers.py:104' },
    remediation: 'Escape the value with markupsafe.escape before wrapping it in Markup.',
    reachability: 'reachable',
    state: 'open',
    package: 'pkg:pypi/flask@2.0.0',
    verified: false,
  },
  {
    id: 'cwe-22-4b91de55aa02',
    rule: 'path-traversal',
    tool: 'llm',
    cwe: 'CWE-22',
    severity: 'medium',
    confidence: 0.58,
    file: 'flask/helpers.py',
    startLine: 133,
    endLine: 136,
    code: 'with open(os.path.join(root, name)) as fh:',
    message: 'File name is joined to a root directory without normalisation or containment check.',
    evidence: { sink: 'flask/helpers.py:133' },
    remediation: 'Resolve the path and assert it stays inside root before opening it.',
    reachability: 'unknown',
    state: 'open',
    package: 'pkg:pypi/flask@2.0.0',
    verified: false,
  },
  {
    id: 'cwe-502-8f0c3a7719ac',
    rule: 'unsafe-deserialize',
    tool: 'sast',
    cwe: 'CWE-502',
    severity: 'high',
    confidence: 0.86,
    file: 'flask/sessions.py',
    startLine: 51,
    endLine: 53,
    code: 'data = pickle.loads(blob)',
    message: 'Untrusted session blob is deserialized with pickle, allowing arbitrary code execution.',
    evidence: { source: 'request.cookies', sink: 'flask/sessions.py:51' },
    remediation: 'Use itsdangerous.URLSafeTimedSerializer for session payloads.',
    reachability: 'reachable',
    state: 'confirmed',
    package: 'pkg:pypi/flask@2.0.0',
    verified: true,
  },
  {
    id: 'cwe-209-5a7d1c0b33e6',
    rule: 'error-exposure',
    tool: 'llm',
    cwe: 'CWE-209',
    severity: 'low',
    confidence: 0.51,
    file: 'flask/app.py',
    startLine: 301,
    endLine: 304,
    code: 'return str(exc), 500',
    message: 'Internal exception text is returned to the client.',
    evidence: { sink: 'flask/app.py:303' },
    remediation: 'Log the exception server-side and return a generic message.',
    reachability: 'reachable',
    state: 'open',
    package: 'pkg:pypi/flask@2.0.0',
    verified: false,
  },
  {
    id: 'cwe-1104-0c1b2d3e4f56',
    rule: 'outdated-dependency',
    tool: 'sca',
    cwe: 'CWE-1104',
    severity: 'medium',
    confidence: 0.99,
    file: 'requirements.txt',
    startLine: 3,
    endLine: 3,
    code: 'urllib3==1.26.0',
    message: 'urllib3 1.26.0 is affected by CVE-2023-43804 (cookie header leak on redirect).',
    evidence: { source: 'requirements.txt:3' },
    remediation: 'Upgrade to urllib3 >= 1.26.17.',
    reachability: 'reachable',
    state: 'open',
    package: 'pkg:pypi/urllib3@1.26.0',
    verified: true,
  },
  {
    id: 'cwe-1104-9e8d7c6b5a43',
    rule: 'outdated-dependency',
    tool: 'sca',
    cwe: 'CWE-1104',
    severity: 'high',
    confidence: 0.99,
    file: 'requirements.txt',
    startLine: 4,
    endLine: 4,
    code: 'requests==2.25.0',
    message: 'requests 2.25.0 is affected by CVE-2023-32681 (proxy-authorization leak).',
    evidence: { source: 'requirements.txt:4' },
    remediation: 'Upgrade to requests >= 2.31.0.',
    reachability: 'reachable',
    state: 'open',
    package: 'pkg:pypi/requests@2.25.0',
    verified: true,
  },
  {
    id: 'cwe-703-1a2b3c4d5e6f',
    rule: 'unchecked-return',
    tool: 'sast',
    cwe: 'CWE-703',
    severity: 'info',
    confidence: 0.42,
    file: 'flask/config.py',
    startLine: 18,
    endLine: 18,
    code: 'os.environ.get("FLASK_DEBUG")',
    message: 'Debug flag read from the environment without validation.',
    evidence: { sink: 'flask/config.py:18' },
    remediation: 'Parse the value as a boolean and reject unknown values.',
    reachability: 'reachable',
    state: 'suppressed',
    suppressedUntil: '2026-12-01T00:00:00Z',
    package: 'pkg:pypi/flask@2.0.0',
    verified: false,
  },
];

const SECURITY_PY_LINES = [
  '"""Security helpers for Flask sessions and tokens."""',
  'import hashlib',
  'import hmac',
  'import os',
  'import random',
  'import string',
  '',
  'from ._compat import text_type',
  '',
  '__all__ = ["compare_digest", "generate_token", "safe_str_cmp"]',
  '',
  'DEFAULT_SECRET = "super-secret-key-change-me"',
  'TOKEN_LENGTH = 32',
  '',
  '',
  'def compare_digest(a, b):',
  '    """Compare two strings without leaking their contents through timing."""',
  '    if isinstance(a, text_type):',
  '        a = a.encode("utf-8")',
  '    if isinstance(b, text_type):',
  '        b = b.encode("utf-8")',
  '    return hmac.compare_digest(a, b)',
  '',
  '',
  'def safe_str_cmp(a, b):',
  '    """Legacy comparison helper kept for backwards compatibility."""',
  '    return a == b',
  '',
  '',
  'def is_default_secret(secret):',
  '    """Check whether the caller left the default secret in place."""',
  '    return secret == DEFAULT_SECRET',
  '',
  '',
  'def generate_token(length=TOKEN_LENGTH):',
  '    """Return a random token for password resets."""',
  '    alphabet = string.ascii_letters + string.digits',
  '    token = "".join(random.choice(alphabet) for _ in range(length))',
  '    return token',
  '',
  '',
  'def sign_payload(payload, secret=None):',
  '    """Sign a payload with the application secret."""',
  '    secret = secret or os.environ.get("SECRET_KEY", DEFAULT_SECRET)',
  '    digest = hashlib.sha256(secret.encode("utf-8")).hexdigest()',
  '    return "%s.%s" % (digest, payload)',
  '',
  '',
  'def verify_payload(payload, signature, secret=None):',
  '    """Verify a signed payload against a signature."""',
  '    secret = secret or os.environ.get("SECRET_KEY", DEFAULT_SECRET)',
  '    expected = sign_payload(payload, secret)',
  '    if safe_str_cmp(expected, signature):',
  '        return True',
  '    return False',
  '',
  '',
  'def make_password_hash(password):',
  '    """Hash a password for storage."""',
  '    return hashlib.md5(password.encode("utf-8")).hexdigest()',
  '',
  '',
  'def check_password_hash(password, hashed):',
  '    """Check a password against a stored hash."""',
  '    return make_password_hash(password) == hashed',
  '',
  '',
  'def constant_time_equals(a, b):',
  '    """Constant-time comparison for API tokens."""',
  '    return hmac.compare_digest(str(a), str(b))',
];

const FILE_TREE: FileNode = {
  name: 'flask-2.0.0',
  path: '',
  type: 'dir',
  findingCount: 12,
  maxSeverity: 'critical',
  children: [
    {
      name: 'flask',
      path: 'flask',
      type: 'dir',
      findingCount: 10,
      maxSeverity: 'critical',
      children: [
        {
          name: '__init__.py',
          path: 'flask/__init__.py',
          type: 'file',
          size: 512,
        },
        {
          name: 'app.py',
          path: 'flask/app.py',
          type: 'file',
          size: 18422,
          findingCount: 2,
          maxSeverity: 'high',
        },
        {
          name: 'config.py',
          path: 'flask/config.py',
          type: 'file',
          size: 2210,
          findingCount: 1,
          maxSeverity: 'info',
        },
        {
          name: 'helpers.py',
          path: 'flask/helpers.py',
          type: 'file',
          size: 9031,
          findingCount: 3,
          maxSeverity: 'medium',
        },
        {
          name: 'security.py',
          path: 'flask/security.py',
          type: 'file',
          size: 3011,
          findingCount: 2,
          maxSeverity: 'critical',
        },
        {
          name: 'sessions.py',
          path: 'flask/sessions.py',
          type: 'file',
          size: 5502,
          findingCount: 2,
          maxSeverity: 'critical',
        },
      ],
    },
    {
      name: 'README.md',
      path: 'README.md',
      type: 'file',
      size: 903,
    },
    {
      name: 'requirements.txt',
      path: 'requirements.txt',
      type: 'file',
      size: 61,
      findingCount: 2,
      maxSeverity: 'high',
    },
    {
      name: 'setup.py',
      path: 'setup.py',
      type: 'file',
      size: 2044,
    },
  ],
};

const FILE_CONTENTS: Record<string, FileContent> = {
  'flask/security.py': {
    path: 'flask/security.py',
    language: 'python',
    lines: SECURITY_PY_LINES,
    findings: FINDINGS.filter((f) => f.file === 'flask/security.py'),
  },
};

const AST_FIXTURE: AST = {
  path: 'flask/security.py',
  nodes: [
    { id: 'n1', type: 'module', label: 'Module security.py', startLine: 1, endLine: 70, depth: 0 },
    { id: 'n2', type: 'import', label: 'import hashlib', startLine: 2, endLine: 2, depth: 1, parentId: 'n1' },
    { id: 'n3', type: 'import', label: 'import hmac', startLine: 3, endLine: 3, depth: 1, parentId: 'n1' },
    { id: 'n4', type: 'assignment', label: 'DEFAULT_SECRET = "super-secret-key-change-me"', startLine: 12, endLine: 12, depth: 1, parentId: 'n1' },
    { id: 'n5', type: 'function_definition', label: 'compare_digest(a, b)', startLine: 16, endLine: 22, depth: 1, parentId: 'n1', symbol: 'compare_digest' },
    { id: 'n6', type: 'call', label: 'hmac.compare_digest(a, b)', startLine: 22, endLine: 22, depth: 2, parentId: 'n5' },
    { id: 'n7', type: 'function_definition', label: 'safe_str_cmp(a, b)', startLine: 25, endLine: 27, depth: 1, parentId: 'n1', symbol: 'safe_str_cmp' },
    { id: 'n8', type: 'comparison', label: 'a == b', startLine: 27, endLine: 27, depth: 2, parentId: 'n7' },
    { id: 'n19', type: 'function_definition', label: 'is_default_secret(secret)', startLine: 30, endLine: 32, depth: 1, parentId: 'n1', symbol: 'is_default_secret' },
    { id: 'n20', type: 'comparison', label: 'secret == DEFAULT_SECRET', startLine: 32, endLine: 32, depth: 2, parentId: 'n19' },
    { id: 'n9', type: 'function_definition', label: 'generate_token(length)', startLine: 35, endLine: 39, depth: 1, parentId: 'n1', symbol: 'generate_token' },
    { id: 'n10', type: 'call', label: 'random.choice(alphabet)', startLine: 38, endLine: 38, depth: 2, parentId: 'n9' },
    { id: 'n11', type: 'function_definition', label: 'sign_payload(payload, secret)', startLine: 42, endLine: 46, depth: 1, parentId: 'n1', symbol: 'sign_payload' },
    { id: 'n12', type: 'call', label: 'hashlib.sha256(secret.encode("utf-8"))', startLine: 45, endLine: 45, depth: 2, parentId: 'n11' },
    { id: 'n13', type: 'function_definition', label: 'verify_payload(payload, signature)', startLine: 49, endLine: 55, depth: 1, parentId: 'n1', symbol: 'verify_payload' },
    { id: 'n14', type: 'if_statement', label: 'if safe_str_cmp(expected, signature)', startLine: 53, endLine: 54, depth: 2, parentId: 'n13' },
    { id: 'n15', type: 'function_definition', label: 'make_password_hash(password)', startLine: 58, endLine: 60, depth: 1, parentId: 'n1', symbol: 'make_password_hash' },
    { id: 'n16', type: 'call', label: 'hashlib.md5(password.encode("utf-8"))', startLine: 60, endLine: 60, depth: 2, parentId: 'n15' },
    { id: 'n17', type: 'function_definition', label: 'constant_time_equals(a, b)', startLine: 68, endLine: 70, depth: 1, parentId: 'n1', symbol: 'constant_time_equals' },
    { id: 'n18', type: 'call', label: 'hmac.compare_digest(str(a), str(b))', startLine: 70, endLine: 70, depth: 2, parentId: 'n17' },
  ],
  edges: [
    { source: 'n1', target: 'n2' },
    { source: 'n1', target: 'n3' },
    { source: 'n1', target: 'n4' },
    { source: 'n1', target: 'n5' },
    { source: 'n5', target: 'n6' },
    { source: 'n1', target: 'n7' },
    { source: 'n7', target: 'n8' },
    { source: 'n1', target: 'n19' },
    { source: 'n19', target: 'n20' },
    { source: 'n1', target: 'n9' },
    { source: 'n9', target: 'n10' },
    { source: 'n1', target: 'n11' },
    { source: 'n11', target: 'n12' },
    { source: 'n1', target: 'n13' },
    { source: 'n13', target: 'n14' },
    { source: 'n1', target: 'n15' },
    { source: 'n15', target: 'n16' },
    { source: 'n1', target: 'n17' },
    { source: 'n17', target: 'n18' },
  ],
};

const CHUNKS_FIXTURE = [
  {
    id: 'c1',
    auditId: 'a1',
    file: 'flask/security.py',
    startLine: 16,
    endLine: 22,
    type: 'function' as const,
    symbolPath: 'flask/security.py::compare_digest',
    docstring: 'Compare two strings without leaking their contents through timing.',
    content: SECURITY_PY_LINES.slice(15, 22).join('\n'),
    calls: ['hmac.compare_digest'],
    imports: ['hmac'],
    language: 'python',
  },
  {
    id: 'c2',
    auditId: 'a1',
    file: 'flask/security.py',
    startLine: 37,
    endLine: 41,
    type: 'function' as const,
    symbolPath: 'flask/security.py::sign_payload',
    content: SECURITY_PY_LINES.slice(36, 41).join('\n'),
    calls: ['hashlib.sha256'],
    imports: ['hashlib', 'os'],
    language: 'python',
  },
];

const ADVISORIES: Advisory[] = [
  {
    id: 'GHSA-x84v-xcm2-53pg',
    source: 'osv',
    summary: 'urllib3 leaks the Cookie header on cross-origin redirects',
    severity: 'medium',
    cwe: ['CWE-200'],
    fixedIn: ['1.26.17'],
    url: 'https://osv.dev/vulnerability/GHSA-x84v-xcm2-53pg',
  },
  {
    id: 'CVE-2023-32681',
    source: 'osv',
    summary: 'Unintended leak of Proxy-Authorization header in requests',
    severity: 'medium',
    cwe: ['CWE-200'],
    fixedIn: ['2.31.0'],
    url: 'https://osv.dev/vulnerability/CVE-2023-32681',
  },
  {
    id: 'PYSEC-2022-42889',
    source: 'pypi',
    summary: 'Flask session cookie may be disclosed to third parties',
    severity: 'low',
    fixedIn: ['2.2.5'],
  },
];

const PATCH: Patch = {
  findingId: 'cwe-798-3f9a2c1d04b7e',
  valid: true,
  error: '',
  summary: 'Load the signing secret from the environment instead of embedding it',
  unified: [
    '--- a/flask/security.py',
    '+++ b/flask/security.py',
    '@@ -9,7 +9,7 @@',
    ' ',
    ' __all__ = ["compare_digest", "generate_token", "safe_str_cmp"]',
    ' ',
    '-DEFAULT_SECRET = "super-secret-key-change-me"',
    '+DEFAULT_SECRET = os.environ.get("SECRET_KEY", "")',
    ' TOKEN_LENGTH = 32',
    ' ',
  ].join('\n'),
};

const CHANGES: Change[] = [];

/* ------------------------------------------------------------------ */
/* Mock implementation                                                 */
/* ------------------------------------------------------------------ */

export type MockHttpMethod = 'GET' | 'POST' | 'PUT' | 'DELETE' | 'PATCH';

/** Error thrown by the mock for the same conditions the real server reports. */
export class MockApiError extends Error {
  readonly status: number;
  readonly code: string;

  constructor(status: number, code: string, message: string) {
    super(message);
    this.name = 'MockApiError';
    this.status = status;
    this.code = code;
  }
}

export interface MockRequestOptions {
  method: MockHttpMethod;
  path: string;
  body?: unknown;
}

type EventListener = (event: Event) => void;

export class MockApi {
  private listeners = new Set<EventListener>();
  private timers = new Set<ReturnType<typeof setTimeout>>();
  private state = {
    audits: [AUDIT],
    findings: FINDINGS.map((f) => ({ ...f })),
    changes: [...CHANGES],
    indexReady: true,
  };

  /** Subscribe to server-pushed events. Returns an unsubscribe function. */
  onEvent(listener: EventListener): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  /** Release any pending timers (used by tests and on teardown). */
  dispose(): void {
    for (const t of this.timers) clearTimeout(t);
    this.timers.clear();
    this.listeners.clear();
  }

  private emit(event: Event): void {
    for (const listener of Array.from(this.listeners)) {
      try {
        listener(event);
      } catch {
        /* a broken listener must not break the mock */
      }
    }
  }

  private later(fn: () => void, ms: number): void {
    const t = setTimeout(() => {
      this.timers.delete(t);
      fn();
    }, ms);
    this.timers.add(t);
  }

  async request<T>(method: MockHttpMethod, path: string, body?: unknown): Promise<T> {
    const url = new URL(path, 'http://mock.local');
    const p = url.pathname;
    const q = url.searchParams;
    const seg = p.split('/').filter(Boolean); // ['api', 'audits', 'a1', 'graph']

    if (method === 'GET' && p === '/api/health') {
      return this.ok<T>({
        ok: true,
        version: '0.7.0-mock',
        workspace: CONFIG.workspace,
        uptimeSeconds: 412,
        providers: PROVIDERS,
      } satisfies HealthResponse);
    }

    if (method === 'GET' && p === '/api/config') {
      return this.ok<T>({ config: CONFIG } satisfies ConfigResponse);
    }

    if (method === 'GET' && p === '/api/providers') {
      return this.ok<T>({ providers: PROVIDERS } satisfies ProvidersResponse);
    }

    if (method === 'GET' && p === '/api/packages/link') {
      const raw = q.get('url') ?? '';
      const npm = /npmjs\.com\/package\/(@?[^/]+(?:\/[^/]+)?)/.exec(raw);
      const pypi = /pypi\.org\/project\/([^/]+)(?:\/([^/]+))?/.exec(raw);
      const repo = /^https:\/\/(?:github|gitlab)\.com\/([^/]+)\/([^/.]+)/.exec(raw);
      if (npm) return this.ok<T>({ link: { kind: 'package', ecosystem: 'npm', name: npm[1] } });
      if (pypi) return this.ok<T>({ link: { kind: 'package', ecosystem: 'pypi', name: pypi[1], version: pypi[2] } });
      if (repo) {
        return this.ok<T>({
          link: { kind: 'repo', repoUrl: `${raw.split('/').slice(0, 5).join('/')}.git`, name: repo[2], display: `${repo[1]}/${repo[2]}` },
        });
      }
      return this.ok<T>({ error: 'that link is not a package page or a repository' });
    }

    if (method === 'GET' && p === '/api/packages') {
      const name = (q.get('name') ?? '').trim().toLowerCase();
      const eco = q.get('ecosystem') ?? 'pypi';
      const found = PACKAGE_FIXTURES.find(
        (entry) => entry.name.toLowerCase() === name && (entry.ecosystem ?? 'pypi') === eco,
      );
      if (!found) {
        return this.ok<T>({
          package: { name, ecosystem: eco as Ecosystem, error: `${eco}: ${name} was not found` },
        });
      }
      return this.ok<T>({ package: found } satisfies PackageLookupResponse);
    }

    if (method === 'GET' && p === '/api/models') {
      return this.ok<T>({
        models: INSTALLED_MODELS,
        chatModel: CONFIG.chatModel,
        scanModel: CONFIG.scanModel,
        embedModel: CONFIG.embedModel,
        numCtx: CONFIG.numCtx,
        keepAlive: CONFIG.keepAlive ?? '3m',
      } satisfies ModelsResponse);
    }

    if (method === 'POST' && p === '/api/models') {
      const input = (body ?? {}) as ModelSelection;
      if (input.chatModel) CONFIG.chatModel = input.chatModel;
      if (input.scanModel) CONFIG.scanModel = input.scanModel;
      if (input.embedModel) CONFIG.embedModel = input.embedModel;
      if (input.numCtx) CONFIG.numCtx = input.numCtx;
      if (input.keepAlive) CONFIG.keepAlive = input.keepAlive;
      return this.ok<T>({
        models: INSTALLED_MODELS,
        chatModel: CONFIG.chatModel,
        scanModel: CONFIG.scanModel,
        embedModel: CONFIG.embedModel,
        numCtx: CONFIG.numCtx,
        keepAlive: CONFIG.keepAlive ?? '3m',
      } satisfies ModelsResponse);
    }

    if (method === 'POST' && p === '/api/models/unload') {
      const input = (body ?? {}) as { models?: string[] };
      const unloaded = input.models?.length
        ? input.models
        : [CONFIG.chatModel, CONFIG.scanModel, CONFIG.embedModel];
      return this.ok<T>({ unloaded } satisfies UnloadResponse);
    }

    if (method === 'POST' && (p === '/api/providers/test' || p === '/api/providers')) {
      const input = (body ?? {}) as { kind?: string; url?: string; model?: string };
      const kind = input.kind ?? 'ollama';
      const provider: ProviderInfo = {
        id: kind,
        kind,
        url: input.url ?? CONFIG.ollamaUrl,
        ok: true,
        models: kind === 'ollama' ? ['qwen3.5:4b', 'qwen2.5-coder:7b', 'embeddinggemma'] : [input.model || 'local-model'],
      };
      if (p === '/api/providers') {
        PROVIDERS.splice(0, PROVIDERS.length, provider);
      }
      return this.ok<T>({ provider });
    }

    if (method === 'POST' && p === '/api/audits') {
      return this.createAudit(body) as Promise<T>;
    }

    if (method === 'GET' && p === '/api/audits') {
      return this.ok<T>({ audits: this.state.audits } satisfies AuditsResponse);
    }

    if (method === 'GET' && p === '/api/advisories/status') {
      return this.ok<T>({
        mode: 'online',
        osv: { available: true, updatedAt: '2026-11-20T00:00:00Z', count: 26428 },
      });
    }

    const job = /^\/api\/audits\/[^/]+\/jobs\/([^/]+)$/.exec(p);
    if (method === 'GET' && job) {
      return this.ok<T>({ job: { state: this.mockJobs.get(job[1]) ?? 'done' } });
    }

    if (method === 'PUT' && /^\/api\/audits\/[^/]+\/file$/.test(p)) {
      return this.ok<T>({ saved: true, changed: true });
    }
    if (method === 'POST' && /^\/api\/audits\/[^/]+\/complete$/.test(p)) {
      return this.ok<T>({ completion: 'return hmac.compare_digest(a, b)', model: CONFIG.chatModel });
    }
    if (method === 'POST' && /^\/api\/audits\/[^/]+\/edit-selection$/.test(p)) {
      const input = (body ?? {}) as { selection?: string };
      return this.ok<T>({ replacement: (input.selection ?? '').replace('a == b', 'hmac.compare_digest(a, b)'), model: CONFIG.chatModel });
    }

    // A pretend shell, so the terminal panel can be seen without a backend.
    if (method === 'POST' && p === '/api/terminals') {
      const id = `mock-t${++this.mockTerminals}`;
      const say = (text: string): void =>
        this.emit({ type: 'terminal.data', jobId: id, data: { data: btoa(text) } });
      this.later(() => say('\x1b[2mmock terminal: no shell is attached (mock data)\x1b[0m\r\n$ '), 150);
      return this.ok<T>({ id, cwd: '/mock/flask' });
    }
    const terminalInput = /^\/api\/terminals\/([^/]+)\/input$/.exec(p);
    if (method === 'POST' && terminalInput) {
      const typed = ((body ?? {}) as { data?: string }).data ?? '';
      const echo = typed.replace(/\r/g, '\r\n$ ');
      this.emit({ type: 'terminal.data', jobId: terminalInput[1], data: { data: btoa(echo) } });
      return this.ok<T>({ ok: true });
    }
    if (/^\/api\/terminals\//.test(p)) return this.ok<T>({ ok: true });

    if (method === 'POST' && p === '/api/chat/title') {
      return this.ok<T>({ title: 'Where the secret is compared' });
    }

    if (method === 'POST' && p === '/api/chat') {
      return this.startChat(body) as Promise<T>;
    }

    if (seg[0] === 'api' && seg[1] === 'audits' && seg.length >= 3) {
      const id = seg[2];
      const tail = seg[3];

      if (method === 'GET' && tail === undefined) {
        const audit = this.state.audits.find((a) => a.id === id);
        if (!audit) throw new MockApiError(404, 'not_found', `no audit with id ${id}`);
        return this.ok<T>({ audit } satisfies AuditResponse);
      }

      if (method === 'GET' && tail === 'graph') {
        return this.ok<T>({ graph: GRAPH } satisfies GraphResponse);
      }

      if (method === 'GET' && tail === 'files') {
        return this.ok<T>({ tree: FILE_TREE } satisfies FilesResponse);
      }

      if (method === 'GET' && tail === 'file') {
        const path = q.get('path') ?? '';
        const file = FILE_CONTENTS[path];
        if (!file) {
          throw new MockApiError(404, 'not_found', `file not found: ${path}`);
        }
        return this.ok<T>({ file } satisfies FileResponse);
      }

      if (method === 'GET' && tail === 'ast') {
        const path = q.get('path') ?? 'flask/security.py';
        return this.ok<T>({ ast: { ...AST_FIXTURE, path } } satisfies AstResponse);
      }

      if (method === 'GET' && tail === 'chunks') {
        return this.ok<T>({ chunks: CHUNKS_FIXTURE } satisfies ChunksResponse);
      }

      if (method === 'GET' && tail === 'findings' && seg[4]) {
        const finding = this.state.findings.find((f) => f.id === seg[4]);
        if (!finding) {
          throw new MockApiError(404, 'not_found', `no finding with id ${seg[4]}`);
        }
        return this.ok<T>({ finding });
      }

      if (method === 'GET' && tail === 'findings') {
        return this.ok<T>(this.filterFindings(q));
      }

      if (method === 'POST' && tail === 'findings' && seg[5] === 'state') {
        return this.setFindingState(seg[4], body) as Promise<T>;
      }

      if (method === 'POST' && tail === 'scan') {
        return this.startScan(id, body) as Promise<T>;
      }

      if (method === 'POST' && tail === 'propose-fix') {
        const input = (body ?? {}) as { findingId?: string };
        return this.ok<T>(({
          patch: { ...PATCH, findingId: input.findingId ?? PATCH.findingId },
        } satisfies PatchResponse));
      }

      if (method === 'POST' && tail === 'apply-patch') {
        return this.applyPatch(body) as Promise<T>;
      }

      if (method === 'GET' && tail === 'changes') {
        return this.ok<T>({ changes: this.state.changes } satisfies ChangesResponse);
      }

      if (method === 'DELETE' && tail === 'workspace') {
        const audit = this.state.audits.find((a) => a.id === id);
        if (!audit) {
          throw new MockApiError(404, 'not_found', `no audit with id ${id}`);
        }
        const freed = audit.sandboxBytes;
        audit.sourcesDeleted = true;
        audit.sandboxBytes = 0;
        audit.fileCount = 0;
        audit.chunkCount = 0;
        return this.ok<T>({
          audit,
          freed,
          freedText: `${(freed / (1024 * 1024)).toFixed(1)} MB`,
        });
      }

      if (method === 'GET' && tail === 'advisories') {
        return this.ok<T>({ advisories: ADVISORIES, mode: 'online' } satisfies AdvisoriesResponse);
      }

      if (method === 'POST' && tail === 'advisories') {
        return this.ok<T>({ advisories: ADVISORIES } satisfies AdvisoriesResponse);
      }

      if (method === 'GET' && tail === 'export') {
        return this.ok<T>({
          format: q.get('format') ?? 'json',
          content: JSON.stringify({ audit: AUDIT, findings: this.state.findings }, null, 2),
        });
      }

      if (method === 'POST' && tail === 'index') {
        return this.ok<T>(({ jobId: 'job-index-1' } satisfies JobResponse));
      }

      if (method === 'GET' && tail === 'index/status') {
        return this.ok<T>({
          ready: true,
          phase: 'complete',
          progress: 1,
          chunks: 1234,
          embedded: 1234,
          keywordOnly: false,
          embedModel: CONFIG.embedModel,
        } satisfies IndexStatusResponse);
      }
    }

    throw new MockApiError(404, 'not_found', `no mock route for ${method} ${p}`);
  }

  private async ok<T>(value: unknown): Promise<T> {
    return value as T;
  }

  private filterFindings(q: URLSearchParams): FindingsResponse {
    let list = this.state.findings;
    const severities = q.getAll('severity');
    if (severities.length) {
      list = list.filter((f) => severities.includes(f.severity));
    }
    const cwes = q.getAll('cwe');
    if (cwes.length) {
      list = list.filter((f) => !!f.cwe && cwes.includes(f.cwe));
    }
    const tool = q.get('tool');
    if (tool) list = list.filter((f) => f.tool === tool);
    const file = q.get('file');
    if (file) list = list.filter((f) => f.file === file);
    const state = q.get('state');
    if (state) list = list.filter((f) => f.state === state);
    const pkg = q.get('package');
    if (pkg) list = list.filter((f) => f.package === pkg);
    const limit = Number(q.get('limit') ?? '0');
    const offset = Number(q.get('offset') ?? '0');
    const total = list.length;
    if (limit > 0) list = list.slice(offset, offset + limit);
    else if (offset > 0) list = list.slice(offset);
    return { findings: list, total };
  }

  private mockTerminals = 0;
  private mockJobs = new Map<string, string>();

  private createAudit(body: unknown): Promise<AuditResponse> {
    const input = (body ?? {}) as { package?: string; version?: string; path?: string; ecosystem?: Ecosystem; url?: string };
    if (input.url) {
      const parsed = /\/([^/]+?)(?:\.git)?\/?$/.exec(input.url);
      input.package = parsed?.[1] ?? 'repository';
    }
    const folder = input.path ? (input.path.split(/[\\/]/).filter(Boolean).pop() ?? input.path) : '';
    const pkg = folder || (input.package ?? 'flask');
    const version = folder ? '' : (input.version ?? '2.0.0');
    const id = folder ? `a-local-${pkg}` : `a-${pkg}-${version}`;
    const audit: Audit = {
      ...AUDIT,
      id,
      package: pkg,
      version,
      source: folder ? 'local' : (input.ecosystem ?? 'pypi'),
      ecosystem: folder ? undefined : (input.ecosystem ?? 'pypi'),
      status: 'pending',
      phase: 'queued',
      progress: 0,
      message: 'Audit queued',
      findingCount: 0,
    };
    this.state.audits = [audit, ...this.state.audits.filter((a) => a.id !== id)];

    const phases: Array<[string, number, string]> = [
      ['acquiring', 0.15, folder ? `Copying ${pkg}` : `Downloading ${pkg}==${version}`],
      ['indexing', 0.45, 'Building retrieval index'],
      ['scanning', 0.75, 'Running static analysis'],
    ];
    // Per-package download events, as the Go backend sends them.
    const packages = folder
      ? []
      : [
          [pkg, version],
          ['click', '8.1.7'],
          ['itsdangerous', '2.2.0'],
          ['jinja2', '3.1.4'],
          ['markupsafe', '2.1.5'],
          ['werkzeug', '3.0.3'],
        ];
    packages.forEach(([name, ver], i) => {
      this.later(() => {
        this.emit({
          type: 'audit.progress', auditId: id, phase: 'acquiring', percent: 0.1,
          message: `Downloading ${name} ${ver}`,
          data: { download: { package: name, version: ver, state: 'downloading', done: i, total: packages.length } },
        });
      }, 30 + i * 40);
      this.later(() => {
        this.emit({
          type: 'audit.progress', auditId: id, phase: 'acquiring', percent: 0.15 + (0.3 * (i + 1)) / packages.length,
          message: `Fetched ${name} ${ver}`,
          data: { download: { package: name, version: ver, state: name === 'markupsafe' ? 'warning' : 'done', detail: name === 'markupsafe' ? 'no source archive; used the wheel' : '', done: i + 1, total: packages.length } },
        });
      }, 80 + i * 50);
    });
    const offset = packages.length ? 80 + packages.length * 50 : 0;
    phases.forEach(([phase, percent, message], i) => {
      this.later(() => {
        this.emit({
          type: 'audit.progress',
          auditId: id,
          phase,
          percent,
          message,
        });
      }, offset + 90 * (i + 1));
    });
    this.later(() => {
      this.emit({ type: 'audit.done', auditId: id });
      const updated: Audit = { ...audit, status: 'ready', phase: 'complete', progress: 1 };
      this.state.audits = this.state.audits.map((a) => (a.id === id ? updated : a));
    }, offset + 90 * (phases.length + 1));

    return this.ok<AuditResponse>({ audit });
  }

  private setFindingState(
    findingId: string | undefined,
    body: unknown,
  ): Promise<FindingStateResponse> {
    if (!findingId) throw new MockApiError(400, 'bad_request', 'missing finding id');
    const input = (body ?? {}) as { state?: FindingState; suppressedUntil?: string };
    const state = input.state ?? 'open';
    if (state === 'suppressed' && !input.suppressedUntil) {
      throw new MockApiError(
        400,
        'bad_request',
        'suppressedUntil is required when state is "suppressed"',
      );
    }
    const finding = this.state.findings.find((f) => f.id === findingId);
    if (!finding) {
      throw new MockApiError(404, 'not_found', `no finding with id ${findingId}`);
    }
    finding.state = state;
    finding.suppressedUntil = state === 'suppressed' ? input.suppressedUntil : undefined;
    return this.ok<FindingStateResponse>({ finding });
  }

  private startScan(auditId: string, body: unknown): Promise<JobResponse> {
    const input = (body ?? {}) as { engine?: string; path?: string };
    const engine = input.engine ?? 'sast';
    const jobId = `job-${engine}-${Date.now()}`;
    this.later(() => {
      this.emit({
        type: 'scan.progress',
        auditId,
        phase: 'starting',
        percent: 0.1,
        message: `Starting ${engine} scan`,
      });
    }, 80);
    this.later(() => {
      this.emit({
        type: 'scan.progress',
        auditId,
        phase: 'scanning',
        percent: 0.6,
        message: input.path ? `Scanning ${input.path}` : 'Scanning workspace',
      });
    }, 220);
    this.later(() => {
      this.emit({
        type: 'scan.progress',
        auditId,
        phase: 'done',
        percent: 1,
        message: `${engine} scan complete`,
      });
      this.mockJobs.set(jobId, 'done');
      this.emit({ type: 'scan.done', auditId, jobId, data: { engine } });
    }, 420);
    this.mockJobs.set(jobId, 'running');
    return this.ok<JobResponse>({ jobId });
  }

  private applyPatch(body: unknown): Promise<ApplyPatchResponse> {
    const input = (body ?? {}) as { path?: string; unified?: string };
    if (!input.path || !input.unified) {
      throw new MockApiError(400, 'bad_request', 'path and unified are required');
    }
    this.state.changes = [
      ...this.state.changes,
      { path: input.path, unified: input.unified, appliedAt: NOW },
    ];
    const resolved = this.state.findings
      .filter((f) => f.file === input.path)
      .map((f) => f.id);
    return this.ok<ApplyPatchResponse>({
      applied: true,
      rescan: { resolved, introduced: [], unchanged: this.state.findings.length - resolved.length },
    });
  }

  private startChat(body: unknown): Promise<JobResponse> {
    const input = (body ?? {}) as { auditId?: string; question?: string };
    const jobId = `chat-${Date.now()}`;
    const answer =
      'The comparison at flask/security.py:27 uses `a == b`, which short-circuits and leaks ' +
      'timing information about the secret. `hmac.compare_digest` (flask/security.py:22) is ' +
      'already used elsewhere in this module, so the fix is to reuse it.';
    const chunks = answer.match(/.{1,24}/gs) ?? [answer];
    // The model thinks, runs a tool, then writes the answer.
    const lead = 1400;
    const thinking = [
      'The question is about where the secret is compared. ',
      'I should look for equality checks on it, ',
      'and for a constant-time helper already in the module.',
    ];
    thinking.forEach((think, i) => {
      this.later(() => this.emit({ type: 'chat.step', jobId, data: { kind: 'thinking', think } }), 250 * (i + 1));
    });
    this.later(
      () =>
        this.emit({
          type: 'chat.step',
          jobId,
          data: { kind: 'tool', id: 'search-1', name: 'search_code', args: { query: 'compare secret' } },
        }),
      800,
    );
    this.later(
      () =>
        this.emit({
          type: 'chat.step',
          jobId,
          data: {
            kind: 'tool_result',
            id: 'search-1',
            name: 'search_code',
            output: 'flask/security.py:22  return hmac.compare_digest(a, b)\nflask/security.py:27  return a == b',
          },
        }),
      lead - 200,
    );
    chunks.forEach((chunk, i) => {
      this.later(() => {
        this.emit({ type: 'chat.token', jobId, data: { delta: chunk } });
      }, lead + 40 * (i + 1));
    });
    this.later(() => {
      const done: ChatDoneData & { answer?: string } = {
        citations: ['flask/security.py:27', 'flask/security.py:22'],
        chunksUsed: ['c1', 'c2'],
        answer,
      };
      this.emit({ type: 'chat.done', jobId, data: done });
    }, lead + 40 * (chunks.length + 1));
    void input;
    return this.ok<JobResponse>({ jobId });
  }
}

/** Severity ordering helper shared with the renderer (mirrors model.severityRank). */
export function severityWeight(s: Severity): number {
  return { critical: 5, high: 4, medium: 3, low: 2, info: 1 }[s];
}
