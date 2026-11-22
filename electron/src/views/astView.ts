import type { AppController } from '../app';
import type { ASTNode, Finding } from '../api/types';
import { h } from '../lib/dom';
import { icon } from '../lib/icons';
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
    revealed.clear();
    render();
  });
  collapseAll.addEventListener('click', () => {
    collapsed = new Set(allNodeIds());
    revealed.clear();
    render();
  });

  const languageBadge = h('span', { class: 'badge neutral', text: '', hidden: true });

  toolbar.append(
    h('span', { class: 'legend-item', text: 'Depth:' }),
    depthSelect,
    expandAll,
    collapseAll,
    h('span', { class: 'grow' }),
    languageBadge,
  );

  function allNodeIds(): string[] {
    const ast = useWorkspaceStore.getState().ast;
    return (ast?.nodes ?? []).map((node) => node.id);
  }

  let activeId: string | null = null;
  /** Nodes opened by hand past the depth limit. */
  const revealed = new Set<string>();
  let lastAst: unknown = null;

  const toggleNode = (id: string): void => {
    if (collapsed.has(id)) collapsed.delete(id);
    else collapsed.add(id);
    render();
  };

  const focusRow = (row: Element | null | undefined): void => {
    if (row instanceof HTMLElement) row.focus();
  };

  const onTreeKey = (event: KeyboardEvent): void => {
    const row = (event.target as HTMLElement).closest<HTMLElement>('.ast-row');
    if (!row) return;
    const id = row.dataset.id ?? '';
    const expandable = row.hasAttribute('aria-expanded');
    const open = row.getAttribute('aria-expanded') === 'true';
    switch (event.key) {
      case 'ArrowDown':
        event.preventDefault();
        focusRow(row.nextElementSibling);
        break;
      case 'ArrowUp':
        event.preventDefault();
        focusRow(row.previousElementSibling);
        break;
      case 'ArrowRight':
        event.preventDefault();
        // The twisty knows whether the node is collapsed or only past the
        // depth limit, so the key goes through it.
        if (expandable && !open) {
          row.querySelector<HTMLButtonElement>('.twisty:not(.leaf)')?.click();
          requestAnimationFrame(() => focusRow(tree.querySelector(`.ast-row[data-id="${CSS.escape(id)}"]`)));
        } else focusRow(row.nextElementSibling);
        break;
      case 'ArrowLeft':
        event.preventDefault();
        if (expandable && open) {
          toggleNode(id);
          requestAnimationFrame(() => focusRow(tree.querySelector(`.ast-row[data-id="${CSS.escape(id)}"]`)));
        }
        break;
      case 'Home':
        event.preventDefault();
        focusRow(tree.firstElementChild);
        break;
      case 'End':
        event.preventDefault();
        focusRow(tree.lastElementChild);
        break;
      default:
    }
  };
  tree.addEventListener('keydown', onTreeKey);

  const render = (): void => {
    const state = useWorkspaceStore.getState();
    const ast = state.ast;

    toolbar.hidden = !ast;
    languageBadge.hidden = !ast?.language;
    languageBadge.textContent = ast?.language ?? '';
    if (!ast) {
      lastAst = null;
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

    // Rows animate in only when a new tree arrives, never on a toggle.
    const fresh = lastAst !== ast;
    if (fresh) {
      lastAst = ast;
      activeId = null;
      revealed.clear();
    }
    tree.classList.toggle('static', !fresh);

    const findings = useFindingsStore.getState().findings.filter(
      (finding) => finding.file === ast.path,
    );
    const roots = buildTree(ast.nodes);
    const fragment = document.createDocumentFragment();
    let index = 0;

    const walk = (entry: TreeNode, depth: number): void => {
      const { node, children } = entry;
      const isOpen = !collapsed.has(node.id);
      // Past the depth limit a node stays shut until it is opened by hand.
      const visible = depth < depthLimit || revealed.has(node.id);
      const hasFinding = covers(findings, node.startLine);
      const expandable = children.length > 0;
      const open = (): void => {
        activeId = node.id;
        for (const el of Array.from(tree.querySelectorAll('.ast-row.is-active'))) {
          el.classList.remove('is-active');
        }
        tree.querySelector(`.ast-row[data-id="${CSS.escape(node.id)}"]`)?.classList.add('is-active');
        void controller.openFile(ast.path, node.startLine);
      };

      const twisty = expandable
        ? h('button', {
            class: 'twisty',
            type: 'button',
            tabindex: '-1',
            'aria-label': `${isOpen ? 'Collapse' : 'Expand'} ${node.label}`,
            onclick: (event: Event) => {
              event.stopPropagation();
              if (!visible) {
                revealed.add(node.id);
                collapsed.delete(node.id);
                render();
              } else {
                toggleNode(node.id);
              }
            },
          })
        : h('span', { class: 'twisty leaf', 'aria-hidden': 'true' });

      const row = h(
        'div',
        {
          class:
            `ast-row type-${node.type}${hasFinding ? ' has-finding' : ''}` +
            (activeId === node.id ? ' is-active' : ''),
          role: 'treeitem',
          tabindex: index === 0 ? '0' : '-1',
          'data-id': node.id,
          'aria-level': String(depth),
          'aria-expanded': expandable ? String(isOpen && visible) : undefined,
          title: hasFinding ? 'A finding is reported on this node' : undefined,
          style: `--depth:${depth - 1};--i:${index}`,
          onclick: open,
          ondblclick: () => {
            if (!expandable) return;
            if (!visible) {
              revealed.add(node.id);
              collapsed.delete(node.id);
              render();
            } else {
              toggleNode(node.id);
            }
          },
          onkeydown: (event: Event) => {
            const key = (event as KeyboardEvent).key;
            if (key === 'Enter' || key === ' ') {
              event.preventDefault();
              open();
            }
          },
        },
        twisty,
        h('span', { class: 'type', text: node.type }),
        // Most inner nodes are labelled with their own type; say it once.
        h('span', { class: 'label', text: node.label === node.type ? '' : node.label }),
        expandable && (!isOpen || !visible)
          ? h('span', { class: 'kids', title: `${children.length} hidden child node(s)` }, String(children.length), icon('chevron-right'))
          : null,
        h('span', { class: 'lines', text: `${node.startLine}–${node.endLine}` }),
      );
      index += 1;
      fragment.appendChild(row);

      if (expandable && isOpen && visible) {
        for (const child of children) walk(child, depth + 1);
      }
    };

    for (const root of roots) walk(root, 1);

    // Keep keyboard focus on the same node across a re-render.
    const focused = document.activeElement?.closest?.('.ast-row') as HTMLElement | null;
    const focusId = focused && tree.contains(focused) ? focused.dataset.id : undefined;
    tree.replaceChildren(fragment);
    if (focusId) focusRow(tree.querySelector(`.ast-row[data-id="${CSS.escape(focusId)}"]`));
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
