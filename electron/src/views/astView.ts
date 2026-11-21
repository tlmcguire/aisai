import type { AppController } from '../app';
import type { ASTNode, Finding } from '../api/types';
import { h } from '../lib/dom';
import { useFindingsStore } from '../state/findings';
import { useWorkspaceStore } from '../state/workspace';

interface TreeNode {
  node: ASTNode;
  children: TreeNode[];
}

function buildTree(nodes: ASTNode[]): TreeNode[] {
  const byId = new Map<string, TreeNode>();
  for (const node of nodes) {
    byId.set(node.id, { node, children: [] });
  }
  const roots: TreeNode[] = [];
  for (const node of nodes) {
    const entry = byId.get(node.id);
    if (!entry) continue;
    const parent = node.parentId ? byId.get(node.parentId) : undefined;
    if (parent) parent.children.push(entry);
    else roots.push(entry);
  }
  return roots;
}

function covers(findings: Finding[], line: number): boolean {
  return findings.some((finding) => line >= finding.startLine && line <= finding.endLine);
}

export interface AstView {
  element: HTMLElement;
  dispose: () => void;
}

export function createAstView(controller: AppController, container: HTMLElement): AstView {
  const toolbar = h('div', { class: 'diff-toolbar' });
  const body = h('div', { class: 'pane-body' });
  const tree = h('div', { class: 'ast-tree', role: 'tree' });
  body.append(tree);
  container.append(toolbar, body);

  let depthLimit = 4;
  let collapsed = new Set<string>();

  const depthSelect = h(
    'select',
    {
      'aria-label': 'Maximum AST depth to show',
      onchange: (event: Event) => {
        const value = (event.target as HTMLSelectElement).value;
        depthLimit = value === 'all' ? Number.POSITIVE_INFINITY : Number(value);
        render();
      },
    },
    ...[1, 2, 3, 4, 6, 8].map((depth) =>
      h('option', { value: String(depth), selected: depth === depthLimit }, `depth ≤ ${depth}`),
    ),
    h('option', { value: 'all' }, 'all depths'),
  );

  const expandAll = h('button', { class: 'tiny', type: 'button' }, 'Expand all');
  const collapseAll = h('button', { class: 'tiny', type: 'button' }, 'Collapse all');

  expandAll.addEventListener('click', () => {
    collapsed = new Set();
    render();
  });
  collapseAll.addEventListener('click', () => {
    collapsed = new Set(allNodeIds());
    render();
  });

  toolbar.append(
    h('span', { class: 'legend-item', text: 'Depth:' }),
    depthSelect,
    expandAll,
    collapseAll,
  );

  function allNodeIds(): string[] {
    const ast = useWorkspaceStore.getState().ast;
    return (ast?.nodes ?? []).map((node) => node.id);
  }

  const render = (): void => {
    const state = useWorkspaceStore.getState();
    const ast = state.ast;

    toolbar.hidden = !ast;
    if (!ast) {
      tree.replaceChildren(
        h(
          'div',
          { class: 'empty-state' },
          h('h3', { text: 'No syntax tree loaded' }),
          h('p', {
            text:
              'Open a source file and its syntax tree appears here as a collapsible tree. ' +
              'Click any node to jump to the matching source line.',
          }),
        ),
      );
      return;
    }

    const findings = useFindingsStore.getState().findings.filter(
      (finding) => finding.file === ast.path,
    );
    const roots = buildTree(ast.nodes);
    const fragment = document.createDocumentFragment();

    const walk = (entry: TreeNode, depth: number): void => {
      const { node, children } = entry;
      const isOpen = !collapsed.has(node.id);
      const visible = depth <= depthLimit;
      const hasFinding = covers(findings, node.startLine);

      fragment.appendChild(
        h(
          'div',
          {
            class: `ast-row type-${node.type}${hasFinding ? ' has-finding' : ''}`,
            role: 'treeitem',
            tabindex: '0',
            'aria-expanded': children.length ? String(isOpen) : undefined,
            title: hasFinding ? 'A finding is reported on this node' : undefined,
            onclick: () => {
              void controller.openFile(ast.path, node.startLine);
            },
            onkeydown: (event: Event) => {
              const key = (event as KeyboardEvent).key;
              if (key === 'Enter' || key === ' ') {
                event.preventDefault();
                void controller.openFile(ast.path, node.startLine);
              }
            },
          },
          h('span', { class: 'twisty', text: children.length ? (isOpen ? '▾' : '▸') : '·' }),
          h('span', { class: 'type', text: node.type }),
          h('span', { class: 'label', text: node.label }),
          h('span', { class: 'lines', text: `${node.startLine}–${node.endLine}` }),
        ),
      );

      if (children.length && isOpen && visible) {
        for (const child of children) walk(child, depth + 1);
      }
    };

    for (const root of roots) walk(root, 1);
    tree.replaceChildren(fragment);
  };

  const unsubscribe = useWorkspaceStore.subscribe((state, previous) => {
    if (state.ast !== previous.ast) {
      render();
    }
  });

  const unsubscribeFindings = useFindingsStore.subscribe((state, previous) => {
    if (state.findings !== previous.findings) {
      render();
    }
  });

  render();

  return {
    element: container,
    dispose: () => {
      unsubscribe();
      unsubscribeFindings();
    },
  };
}
