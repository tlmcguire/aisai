import type { AppController } from '../app';
import type { FileNode, Severity } from '../api/types';
import { h } from '../lib/dom';
import { severityColor } from '../lib/severity';
import { useWorkspaceStore } from '../state/workspace';

function row(
  node: FileNode,
  depth: number,
  expanded: string[],
  selectedPath: string | null,
  onToggle: (path: string) => void,
  onOpen: (path: string) => void,
): HTMLElement {
  const isDir = node.type === 'dir';
  const isOpen = expanded.includes(node.path);
  const hasFindings = (node.findingCount ?? 0) > 0;

  const twisty = h('span', { class: 'twisty', text: isDir ? (isOpen ? '▾' : '▸') : '' });

  const marker = isDir && hasFindings
    ? h('span', {
        class: 'tree-dir-marker',
        style: `background:${severityColor((node.maxSeverity ?? 'info') as Severity)}`,
        title: `Highest severity in this directory: ${node.maxSeverity ?? 'info'}`,
      })
    : null;

  const label = h('span', { class: 'name', text: node.name });

  const badge =
    hasFindings && !isDir
      ? h('span', {
          class: 'count',
          text: String(node.findingCount),
          title: `${node.findingCount} finding(s)`,
        })
      : isDir && hasFindings
        ? h('span', { class: 'count', text: String(node.findingCount) })
        : null;

  const element = h(
    'div',
    {
      class: `tree-row${selectedPath === node.path ? ' selected' : ''}`,
      role: 'treeitem',
      tabindex: '0',
      'aria-expanded': isDir ? String(isOpen) : undefined,
      'aria-label': `${node.name}${hasFindings ? `, ${node.findingCount} findings` : ''}`,
      style: `padding-left:${depth * 12 + 4}px`,
      onclick: () => (isDir ? onToggle(node.path) : onOpen(node.path)),
      onkeydown: (event: Event) => {
        const key = (event as KeyboardEvent).key;
        if (key === 'Enter' || key === ' ') {
          event.preventDefault();
          if (isDir) onToggle(node.path);
          else onOpen(node.path);
        }
      },
    },
    twisty,
    marker,
    label,
    badge,
  );

  return element;
}

export interface FileTreeView {
  element: HTMLElement;
  dispose: () => void;
}

export function createFileTree(controller: AppController, container: HTMLElement): FileTreeView {
  const body = h('div', { class: 'pane-body' });
  const tree = h('div', { class: 'tree', role: 'tree' });
  body.append(tree);
  container.append(body);

  let selectedPath: string | null = null;

  const render = (): void => {
    const state = useWorkspaceStore.getState();
    tree.replaceChildren();

    if (!state.tree) {
      tree.append(
        h(
          'div',
          { class: 'empty-state' },
          h('h3', { text: 'No files loaded yet' }),
          h('p', {
            text:
              'Files appear here once an audit is running. Pick a package in the toolbar and press ' +
              'Scan — the workspace is downloaded, indexed and scanned automatically.',
          }),
        ),
      );
      return;
    }

    const expanded = state.expanded;
    const walk = (node: FileNode, depth: number): void => {
      tree.append(row(node, depth, expanded, selectedPath, onToggle, onOpen));
      if (node.type === 'dir' && expanded.includes(node.path)) {
        for (const child of node.children ?? []) walk(child, depth + 1);
      }
    };

    if (!expanded.includes(state.tree.path)) {
      // The root starts expanded.
      for (const child of state.tree.children ?? []) walk(child, 1);
    } else {
      walk(state.tree, 0);
    }
  };

  const onToggle = (path: string): void => {
    useWorkspaceStore.getState().toggleExpanded(path);
  };

  const onOpen = (path: string): void => {
    selectedPath = path;
    void controller.openFile(path);
    render();
  };

  const unsubscribe = useWorkspaceStore.subscribe((state, previous) => {
    if (state.tree !== previous.tree || state.expanded !== previous.expanded) {
      render();
    }
  });

  render();

  return {
    element: container,
    dispose: unsubscribe,
  };
}
