import type { AppController } from '../app';
import type { FileNode, Finding } from '../api/types';
import { h } from '../lib/dom';
import { t, tn } from '../lib/i18n';
import { severityClass } from '../lib/severity';
import { maxSeverity } from '../api/types';
import { useFindingsStore } from '../state/findings';
import { useWorkspaceStore } from '../state/workspace';

/**
 * The file tree.
 *
 * Built for reading a package you have never seen: indent guides show the
 * nesting, every file carries a short language tag, directories show how
 * many findings they hold and how bad the worst one is, and a filter box
 * narrows thousands of files to the few that matter. The keyboard works the
 * way a native tree does (arrows, Home/End, Enter).
 */

/** Short, readable tags for common extensions. */
const EXT_TAG: Record<string, string> = {
  py: 'py', pyi: 'py', js: 'js', mjs: 'js', cjs: 'js', jsx: 'jsx', ts: 'ts', tsx: 'tsx',
  go: 'go', rs: 'rs', java: 'java', cs: 'c#', rb: 'rb', c: 'c', h: 'h', cc: 'c++', cpp: 'c++',
  hpp: 'h++', php: 'php', sh: 'sh', bash: 'sh', scala: 'sc', json: '{}', toml: 'toml',
  yaml: 'yml', yml: 'yml', md: 'md', txt: 'txt', cfg: 'cfg', ini: 'ini', html: 'html', css: 'css',
};

/** Extensions AISAI parses and scans; others are dimmed. */
const SCANNED = new Set(['py', 'pyi', 'js', 'mjs', 'cjs', 'jsx', 'ts', 'tsx', 'go', 'rs', 'java', 'cs', 'rb', 'c', 'h', 'cc', 'cpp', 'cxx', 'hpp', 'hh', 'hxx', 'php', 'sh', 'bash', 'zsh', 'scala', 'sc']);

function extOf(name: string): string {
  const dot = name.lastIndexOf('.');
  return dot > 0 ? name.slice(dot + 1).toLowerCase() : '';
}

function collectDirs(node: FileNode, out: string[] = []): string[] {
  if (node.type === 'dir') {
    out.push(node.path);
    for (const child of node.children ?? []) collectDirs(child, out);
  }
  return out;
}

/** Sort a level: directories first, then files, each alphabetically. */
function sorted(children: FileNode[] | undefined): FileNode[] {
  return [...(children ?? [])].sort((a, b) => {
    if (a.type !== b.type) return a.type === 'dir' ? -1 : 1;
    return a.name.localeCompare(b.name, undefined, { numeric: true, sensitivity: 'base' });
  });
}

/**
 * The tree with its finding counts brought up to date from the findings store.
 *
 * The tree is fetched when an audit is ready, so a scan that runs afterwards
 * (the LLM scan, a rescan after a fix) would leave its counts at zero. The
 * findings store always holds the current findings, so the counts are rebuilt
 * from it; the larger of the two numbers wins, which keeps the server's count
 * when the store holds only part of a long list.
 */
function withLiveCounts(root: FileNode, findings: Finding[]): FileNode {
  const live = new Map<string, { count: number; worst: Finding['severity'] }>();
  for (const finding of findings) {
    const entry = live.get(finding.file);
    live.set(finding.file, {
      count: (entry?.count ?? 0) + 1,
      worst: entry ? maxSeverity(entry.worst, finding.severity) : finding.severity,
    });
  }
  const walk = (node: FileNode): FileNode => {
    if (node.type !== 'dir') {
      const found = live.get(node.path);
      if (!found) return node;
      const count = Math.max(found.count, node.findingCount ?? 0);
      const worst = node.maxSeverity ? maxSeverity(node.maxSeverity, found.worst) : found.worst;
      return { ...node, findingCount: count, maxSeverity: worst };
    }
    const children = (node.children ?? []).map(walk);
    const count = children.reduce((sum, child) => sum + (child.findingCount ?? 0), 0);
    let worst: Finding['severity'] | undefined;
    for (const child of children) {
      if (child.findingCount && child.maxSeverity) worst = worst ? maxSeverity(worst, child.maxSeverity) : child.maxSeverity;
    }
    return { ...node, children, findingCount: count, maxSeverity: worst ?? node.maxSeverity };
  };
  return walk(root);
}

export interface FileTreeView {
  element: HTMLElement;
  dispose: () => void;
}

export function createFileTree(controller: AppController, container: HTMLElement): FileTreeView {
  const filter = h('input', {
    type: 'search',
    class: 'tree-filter',
    placeholder: t('files.filterPlaceholder'),
    'aria-label': t('files.filterLabel'),
    spellcheck: 'false',
  });
  const onlyFindings = h(
    'button',
    { class: 'tiny ghost', type: 'button', 'aria-pressed': 'false', title: t('files.onlyFindingsTitle') },
    t('files.withFindings'),
  );
  const expandAll = h('button', { class: 'tiny ghost', type: 'button', title: t('files.expandAllTitle') }, t('files.expand'));
  const collapseAll = h('button', { class: 'tiny ghost', type: 'button', title: t('files.collapseAllTitle') }, t('files.collapse'));
  const controls = h('div', { class: 'tree-controls' }, filter, onlyFindings, expandAll, collapseAll);
  const tree = h('div', { class: 'tree', role: 'tree', 'aria-label': t('files.treeLabel') });
  const body = h('div', { class: 'pane-body tree-scroll' }, tree);
  container.append(controls, body);

  let query = '';
  let findingsOnly = false;
  let focusPath: string | null = null;

  const selectedPath = (): string | null => useWorkspaceStore.getState().file?.path ?? null;

  /** Whether a node, or anything below it, passes the filters. */
  const matches = (node: FileNode): boolean => {
    if (node.type === 'dir') return (node.children ?? []).some(matches);
    if (findingsOnly && !(node.findingCount ?? 0)) return false;
    if (!query) return true;
    return node.path.toLowerCase().includes(query);
  };

  const highlight = (name: string): Node[] => {
    if (!query) return [document.createTextNode(name)];
    const at = name.toLowerCase().indexOf(query);
    if (at < 0) return [document.createTextNode(name)];
    return [
      document.createTextNode(name.slice(0, at)),
      h('mark', { text: name.slice(at, at + query.length) }),
      document.createTextNode(name.slice(at + query.length)),
    ];
  };

  const open = (node: FileNode): void => {
    focusPath = node.path;
    void controller.openFile(node.path);
  };

  const toggle = (path: string): void => {
    focusPath = path;
    useWorkspaceStore.getState().toggleExpanded(path);
  };

  const row = (node: FileNode, depth: number, isOpen: boolean): HTMLElement => {
    const isDir = node.type === 'dir';
    const count = node.findingCount ?? 0;
    const ext = extOf(node.name);
    const selected = !isDir && node.path === selectedPath();
    const tag = isDir ? null : h('span', { class: `file-tag${SCANNED.has(ext) ? '' : ' is-plain'}`, text: EXT_TAG[ext] ?? (ext.slice(0, 4) || t('files.noExtensionTag')) });

    return h(
      'div',
      {
        class: `tree-row${isDir ? ' is-dir' : ''}${selected ? ' selected' : ''}${!isDir && !SCANNED.has(ext) ? ' is-plain' : ''}`,
        role: 'treeitem',
        tabindex: '-1',
        'data-path': node.path,
        'data-dir': isDir ? '1' : undefined,
        'aria-level': String(depth + 1),
        'aria-selected': selected ? 'true' : undefined,
        'aria-expanded': isDir ? String(isOpen) : undefined,
        'aria-label': count ? tn('files.findingsInFile', count, { name: node.name }) : node.name,
        title: node.path,
        style: `--depth:${depth}`,
        onclick: () => (isDir ? toggle(node.path) : open(node)),
      },
      h('span', { class: `twisty${isDir ? '' : ' leaf'}`, 'aria-hidden': 'true' }),
      isDir ? h('span', { class: `folder-icon${isOpen ? ' is-open' : ''}`, 'aria-hidden': 'true' }) : tag,
      h('span', { class: 'name' }, ...highlight(node.name)),
      count
        ? h('span', {
            class: `count ${severityClass(node.maxSeverity ?? 'info')}`,
            text: String(count),
            title: tn('files.countTitle', count, { worst: node.maxSeverity ?? 'info' }),
          })
        : null,
    );
  };

  const render = (): void => {
    const state = useWorkspaceStore.getState();

    if (!state.tree) {
      controls.hidden = true;
      tree.replaceChildren(
        h(
          'div',
          { class: 'empty-state' },
          h('h3', { text: t('files.emptyTitle') }),
          h('p', { text: t('files.emptyDescription') }),
        ),
      );
      return;
    }
    controls.hidden = false;

    const filtering = Boolean(query) || findingsOnly;
    const expanded = new Set(state.expanded);
    const fragment = document.createDocumentFragment();
    let shown = 0;

    const walk = (node: FileNode, depth: number): void => {
      if (filtering && !matches(node)) return;
      // While filtering every matching folder is open, so a hit is never hidden.
      const isOpen = node.type === 'dir' && (filtering || expanded.has(node.path));
      fragment.append(row(node, depth, isOpen));
      shown += 1;
      if (isOpen) for (const child of sorted(node.children)) walk(child, depth + 1);
    };
    const liveRoot = withLiveCounts(state.tree, useFindingsStore.getState().findings);
    for (const child of sorted(liveRoot.children)) walk(child, 0);

    if (!shown) {
      fragment.append(
        h('div', { class: 'tree-empty' }, filtering ? t('files.noMatch') : t('files.noFilesInAudit')),
      );
    }

    const hadFocus = tree.contains(document.activeElement);
    tree.replaceChildren(fragment);

    // Roving tab stop: the focused, selected, or first row takes it.
    const target =
      (focusPath && tree.querySelector<HTMLElement>(`.tree-row[data-path="${CSS.escape(focusPath)}"]`)) ||
      tree.querySelector<HTMLElement>('.tree-row.selected') ||
      tree.querySelector<HTMLElement>('.tree-row');
    if (target) {
      target.tabIndex = 0;
      if (hadFocus) target.focus({ preventScroll: true });
    }
  };

  const rows = (): HTMLElement[] => Array.from(tree.querySelectorAll<HTMLElement>('.tree-row'));

  const moveTo = (el: HTMLElement | undefined): void => {
    if (!el) return;
    for (const r of rows()) r.tabIndex = -1;
    el.tabIndex = 0;
    el.focus();
    el.scrollIntoView({ block: 'nearest' });
    focusPath = el.dataset.path ?? null;
  };

  tree.addEventListener('keydown', (event) => {
    const current = (event.target as HTMLElement).closest<HTMLElement>('.tree-row');
    if (!current) return;
    const all = rows();
    const index = all.indexOf(current);
    const path = current.dataset.path ?? '';
    const isDir = current.dataset.dir === '1';
    const isOpen = current.getAttribute('aria-expanded') === 'true';
    switch (event.key) {
      case 'ArrowDown':
        event.preventDefault();
        moveTo(all[index + 1]);
        break;
      case 'ArrowUp':
        event.preventDefault();
        moveTo(all[index - 1]);
        break;
      case 'ArrowRight':
        event.preventDefault();
        if (isDir && !isOpen) toggle(path);
        else moveTo(all[index + 1]);
        break;
      case 'ArrowLeft': {
        event.preventDefault();
        if (isDir && isOpen) {
          toggle(path);
          break;
        }
        const level = Number(current.getAttribute('aria-level'));
        for (let i = index - 1; i >= 0; i -= 1) {
          if (Number(all[i].getAttribute('aria-level')) < level) {
            moveTo(all[i]);
            break;
          }
        }
        break;
      }
      case 'Home':
        event.preventDefault();
        moveTo(all[0]);
        break;
      case 'End':
        event.preventDefault();
        moveTo(all[all.length - 1]);
        break;
      case 'Enter':
      case ' ':
        event.preventDefault();
        current.click();
        break;
      default:
        // Typing a letter jumps to the next row that starts with it.
        if (event.key.length === 1 && !event.metaKey && !event.ctrlKey && !event.altKey) {
          const ch = event.key.toLowerCase();
          const next = [...all.slice(index + 1), ...all.slice(0, index)].find((r) =>
            (r.querySelector('.name')?.textContent ?? '').toLowerCase().startsWith(ch),
          );
          moveTo(next);
        }
    }
  });

  let filterTimer = 0;
  filter.addEventListener('input', () => {
    window.clearTimeout(filterTimer);
    filterTimer = window.setTimeout(() => {
      query = filter.value.trim().toLowerCase();
      render();
    }, 80);
  });
  filter.addEventListener('keydown', (event) => {
    if (event.key === 'ArrowDown') {
      event.preventDefault();
      moveTo(rows()[0]);
    } else if (event.key === 'Escape' && filter.value) {
      event.preventDefault();
      filter.value = '';
      query = '';
      render();
    }
  });
  onlyFindings.addEventListener('click', () => {
    findingsOnly = !findingsOnly;
    onlyFindings.setAttribute('aria-pressed', String(findingsOnly));
    render();
  });
  expandAll.addEventListener('click', () => {
    const root = useWorkspaceStore.getState().tree;
    if (root) useWorkspaceStore.getState().expand(collectDirs(root));
  });
  collapseAll.addEventListener('click', () => {
    const root = useWorkspaceStore.getState().tree;
    useWorkspaceStore.setState({ expanded: root ? [root.path] : [] });
  });

  const unsubscribe = useWorkspaceStore.subscribe((state, previous) => {
    if (state.tree !== previous.tree || state.expanded !== previous.expanded) {
      render();
      return;
    }
    // A file opened from elsewhere (a finding, a citation) is revealed here.
    if (state.file?.path !== previous.file?.path) {
      const path = state.file?.path;
      if (path && state.tree) {
        const parts = path.split('/');
        const parents = parts.slice(0, -1).map((_, i) => parts.slice(0, i + 1).join('/'));
        const missing = parents.filter((p) => !state.expanded.includes(p));
        if (missing.length) {
          state.expand(missing);
          // expand() re-renders through the branch above.
          requestAnimationFrame(() => tree.querySelector('.tree-row.selected')?.scrollIntoView({ block: 'nearest' }));
          return;
        }
      }
      render();
      tree.querySelector('.tree-row.selected')?.scrollIntoView({ block: 'nearest' });
    }
  });

  const unsubscribeFindings = useFindingsStore.subscribe((state, previous) => {
    if (state.findings !== previous.findings) render();
  });

  render();

  return {
    element: container,
    dispose: () => {
      window.clearTimeout(filterTimer);
      unsubscribe();
      unsubscribeFindings();
    },
  };
}
