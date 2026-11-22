/**
 * The language logos.
 *
 * Every file type has the logo of its language, the way an editor shows the
 * files of a project: Python, Rust, C, C#, Markdown and the rest are told
 * apart at a glance instead of by their extension.
 *
 * The marks are the icons of the Material Icon Theme for Visual Studio Code
 * (MIT licence); the Kotlin and .env marks come from Simple Icons (CC0 1.0).
 * They keep the colours of their language and are drawn in their own grid, so
 * they are used as they are and are not redrawn on the grid of the other
 * icons.
 *
 * A language that AISAI cannot scan is dimmed, so the files that are worth a
 * look are the ones that stand out.
 */

/** The marks, by language, as they were published. */
const MARKS = import.meta.glob('../assets/langicons/*.svg', {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>;

/** The marks that were read already, so that a file does not parse them again. */
const parsed = new Map<string, SVGSVGElement>();

/** The extensions that name a language. */
const BY_EXTENSION: Record<string, string> = {
  py: 'python', pyi: 'python', pyw: 'python',
  rs: 'rust',
  c: 'c', h: 'h',
  cc: 'cpp', cpp: 'cpp', cxx: 'cpp', 'c++': 'cpp', hpp: 'hpp', hh: 'cpp', hxx: 'cpp',
  'h++': 'cpp', ipp: 'cpp',
  cs: 'csharp', csx: 'csharp',
  go: 'go',
  js: 'javascript', mjs: 'javascript', cjs: 'javascript', jsx: 'javascript',
  ts: 'typescript', mts: 'typescript', cts: 'typescript', tsx: 'typescript',
  java: 'java',
  rb: 'ruby', rake: 'ruby', gemspec: 'ruby',
  php: 'php',
  sh: 'shell', bash: 'shell', zsh: 'shell', fish: 'shell', ksh: 'shell',
  ps1: 'powershell', psm1: 'powershell',
  html: 'html', htm: 'html', xhtml: 'html',
  css: 'css',
  scss: 'scss', sass: 'scss',
  less: 'less',
  md: 'markdown', markdown: 'markdown', mdx: 'markdown',
  json: 'json', jsonc: 'json', json5: 'json',
  yml: 'yaml', yaml: 'yaml',
  toml: 'toml',
  ini: 'ini', cfg: 'ini', conf: 'ini', properties: 'ini', editorconfig: 'ini',
  xml: 'xml', xsd: 'xml', xsl: 'xml', svg: 'xml', plist: 'xml',
  sql: 'sql', ddl: 'sql',
  swift: 'swift',
  kt: 'kotlin', kts: 'kotlin',
  scala: 'scala', sc: 'scala',
  dart: 'dart',
  lua: 'lua',
  pl: 'perl', pm: 'perl',
  r: 'r', rmd: 'r',
  jl: 'julia',
  hs: 'haskell', lhs: 'haskell',
  ex: 'elixir', exs: 'elixir',
  erl: 'erlang', hrl: 'erlang',
  clj: 'clojure', cljs: 'clojure', cljc: 'clojure', edn: 'clojure',
  ml: 'ocaml', mli: 'ocaml',
  fs: 'fsharp', fsi: 'fsharp', fsx: 'fsharp',
  zig: 'zig',
  nim: 'nim', nims: 'nim',
  vue: 'vue',
  svelte: 'svelte',
  m: 'matlab',
  bat: 'batch', cmd: 'batch',
  asm: 'assembly', s: 'assembly',
  vim: 'vim',
  tex: 'tex', sty: 'tex', cls: 'tex', bib: 'tex',
  mk: 'make',
  cmake: 'cmake',
  gradle: 'gradle',
  dockerfile: 'docker',
  gitignore: 'git', gitattributes: 'git', gitmodules: 'git',
  png: 'image', jpg: 'image', jpeg: 'image', gif: 'image', bmp: 'image', webp: 'image', ico: 'image',
  avif: 'image', tiff: 'image',
  pdf: 'pdf',
  csv: 'sheet', tsv: 'sheet',
  txt: 'plain', rst: 'plain',
  log: 'log',
  ipynb: 'jupyter',
};

/** The files that are known by their name rather than their extension. */
const BY_NAME: Record<string, string> = {
  dockerfile: 'docker',
  containerfile: 'docker',
  'docker-compose.yml': 'docker',
  'docker-compose.yaml': 'docker',
  makefile: 'make',
  gnumakefile: 'make',
  cmakelists: 'cmake',
  'cmakelists.txt': 'cmake',
  rakefile: 'ruby',
  gemfile: 'ruby',
  'gemfile.lock': 'ruby',
  pipfile: 'python',
  'pipfile.lock': 'python',
  'requirements.txt': 'python',
  'requirements-dev.txt': 'python',
  'pyproject.toml': 'python',
  'poetry.lock': 'python',
  'uv.lock': 'python',
  'cargo.toml': 'rust',
  'cargo.lock': 'lock',
  'go.mod': 'go',
  'go.sum': 'go',
  'package.json': 'npm',
  'package-lock.json': 'lock',
  'yarn.lock': 'lock',
  'pnpm-lock.yaml': 'pnpm',
  'composer.json': 'php',
  'composer.lock': 'php',
  'pom.xml': 'java',
  'build.gradle': 'java',
  'build.gradle.kts': 'java',
  'gradle.properties': 'gradle',
  'tsconfig.json': 'tsconfig',
  'jsconfig.json': 'jsconfig',
  license: 'licence',
  licence: 'licence',
  copying: 'licence',
  notice: 'licence',
  '.env': 'env',
  '.env.local': 'env',
  '.envrc': 'env',
};

/** The extension of a file name, in lower case. */
function extensionOf(name: string): string {
  const dot = name.lastIndexOf('.');
  return dot > 0 ? name.slice(dot + 1).toLowerCase() : '';
}

/** The extensions AISAI parses and scans; the files of the other languages are dimmed. */
const SCANNED_EXTENSIONS = new Set([
  'py', 'pyi', 'js', 'mjs', 'cjs', 'jsx', 'ts', 'tsx', 'go', 'rs', 'java', 'cs', 'rb',
  'c', 'h', 'cc', 'cpp', 'cxx', 'hpp', 'hh', 'hxx', 'php', 'sh', 'bash', 'zsh', 'scala', 'sc',
]);

/** Whether AISAI can scan the file of that name. */
export function isScanned(name: string): boolean {
  return SCANNED_EXTENSIONS.has(extensionOf(name.toLowerCase()));
}

/** The language of a file, by its name. */
export function languageOf(name: string): string | undefined {
  const lower = name.toLowerCase();
  const byName = BY_NAME[lower];
  if (byName) return byName;
  const byExtension = BY_EXTENSION[extensionOf(lower)];
  if (byExtension) return byExtension;
  // A file with no extension may still be known: Dockerfile, Makefile.
  return BY_NAME[lower.replace(/^\./, '')];
}

/**
 * The mark of a language.
 *
 * The mark is read once and kept, so that a tree of thousands of files does
 * not read the same file again and again.
 */
function markOf(language: string): SVGSVGElement | null {
  const cached = parsed.get(language);
  if (cached) return cached;
  const source = MARKS['../assets/langicons/' + language + '.svg'];
  if (!source) return null;
  const doc = new DOMParser().parseFromString(source, 'image/svg+xml');
  if (doc.querySelector('parsererror')) return null;
  const svg = doc.documentElement as unknown as SVGSVGElement;
  parsed.set(language, svg);
  return svg;
}

/**
 * The symbol of a file.
 *
 * The logo of the language is drawn when it has one. A language that AISAI
 * cannot scan is dimmed, so the files that are worth a look stand out in the
 * tree.
 */
export function fileSymbol(name: string, scanned: boolean): SVGElement | null {
  const language = languageOf(name);
  if (!language) return null;
  const mark = markOf(language);
  if (!mark) return null;
  const svg = document.importNode(mark, true) as SVGElement;
  // The size is set by the style sheet, so the size of the mark is dropped.
  svg.removeAttribute('width');
  svg.removeAttribute('height');
  svg.setAttribute('class', scanned ? 'file-symbol' : 'file-symbol is-plain');
  svg.setAttribute('aria-hidden', 'true');
  return svg;
}
