import type { AppController } from '../app';
import type { ASTNode, Finding } from '../api/types';
import { h } from '../lib/dom';
import { icon } from '../lib/icons';
import { useFindingsStore } from '../state/findings';
import { useWorkspaceStore } from '../state/workspace';
import { loadVis, type VisDataSet, type VisEdgeData, type VisNetwork, type VisNodeData } from '../lib/vis';

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
  const graphWrap = h('div', { class: 'ast-graph', hidden: true });
  const graphNote = h('div', { class: 'ast-graph-note', role: 'status', hidden: true });
  graphWrap.append(graphNote);
  const graphCanvas = h('div', { class: 'graph-canvas ast-graph-canvas', role: 'img', 'aria-label': 'Syntax tree graph' });
  graphWrap.append(graphCanvas);
  container.append(toolbar, body, graphWrap);

  /** 'tree' is the collapsible list; 'graph' draws the nodes and their parent links. */
  let mode: 'tree' | 'graph' = 'tree';
  /** The most nodes the graph draws; a syntax tree of a big file has thousands. */
  const GRAPH_NODE_CAP = 500;
  let network: VisNetwork | null = null;
  let graphNodes: VisDataSet<VisNodeData> | null = null;
  let graphEdges: VisDataSet<VisEdgeData> | null = null;
  let graphAst: unknown = null;
  let graphDepth = -1;
  // A pane that is resized or moved gets its graph fitted again.
  const graphObserver = new ResizeObserver(() => {
    if (mode === 'graph') network?.fit({ animation: false });
  });
  graphObserver.observe(graphCanvas);

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
        if (mode === 'graph') renderGraph();
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

  const modeToggle = h('div', { class: 'segmented', role: 'group', 'aria-label': 'How to show the syntax tree' });
  const modeButtons = (['tree', 'graph'] as const).map((id) =>
    h(
      'button',
      {
        class: 'tiny',
        type: 'button',
        'aria-pressed': String(id === mode),
        onclick: () => setMode(id),
      },
      id === 'tree' ? 'Tree' : 'Graph',
    ),
  );
  modeToggle.append(...modeButtons);

  const setMode = (next: 'tree' | 'graph'): void => {
    mode = next;
    modeButtons.forEach((button, index) => button.setAttribute('aria-pressed', String((index === 0) === (next === 'tree'))));
    body.hidden = next !== 'tree';
    graphWrap.hidden = next !== 'graph';
    expandAll.hidden = collapseAll.hidden = next !== 'tree';
    if (next === 'graph') renderGraph();
  };

  const languageBadge = h('span', { class: 'badge neutral', text: '', hidden: true });

  toolbar.append(
    modeToggle,
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

  /** Draws the syntax tree as a top-down node-link diagram. */
  const renderGraph = (): void => {
    const ast = useWorkspaceStore.getState().ast;
    graphNote.hidden = true;
    if (!ast) {
      network?.destroy();
      network = null;
      graphAst = null;
      return;
    }
    if (graphAst === ast && graphDepth === depthLimit && network) return;
    graphAst = ast;
    graphDepth = depthLimit;

    const byId = new Map(ast.nodes.map((node) => [node.id, node]));
    const depthOf = (node: ASTNode): number => {
      let depth = 1;
      for (let parent = node.parentId ? byId.get(node.parentId) : undefined; parent; parent = parent.parentId ? byId.get(parent.parentId) : undefined) {
        depth += 1;
        if (depth > 200) break;
      }
      return depth;
    };
    const findings = useFindingsStore.getState().findings.filter((finding) => finding.file === ast.path);
    const shown: ASTNode[] = [];
    let omitted = 0;
    for (const node of ast.nodes) {
      if (depthOf(node) > depthLimit || shown.length >= GRAPH_NODE_CAP) omitted += 1;
      else shown.push(node);
    }
    const ids = new Set(shown.map((node) => node.id));
    const nodes: VisNodeData[] = shown.map((node) => {
      const bad = covers(findings, node.startLine);
      return {
        id: node.id,
        label: node.label && node.label !== node.type ? `${node.type}\n${node.label}` : node.type,
        title: `${node.type} · lines ${node.startLine}–${node.endLine}`,
        shape: 'box',
        widthConstraint: { maximum: 150 },
        color: bad
          ? { background: '#5a2a2e', border: '#e5484d' }
          : { background: '#1b2b2e', border: '#35494c' },
        font: { color: '#dde8e3', size: 11, face: 'JetBrains Mono Variable, monospace', multi: true },
      };
    });
    const edges: VisEdgeData[] = shown
      .filter((node) => node.parentId && ids.has(node.parentId))
      .map((node) => ({ from: node.parentId as string, to: node.id }));

    graphNote.hidden = omitted === 0;
    graphNote.textContent = `${omitted} deeper or surplus node(s) are not drawn. Lower the depth or use the tree.`;

    void loadVis()
      .then(() => {
        if (mode !== 'graph' || graphAst !== ast || !window.vis) return;
        network?.destroy();
        graphNodes = new window.vis.DataSet<VisNodeData>(nodes);
        graphEdges = new window.vis.DataSet<VisEdgeData>(edges);
        // Own network and own DataSets: the dependency graph keeps the
        // shared globals, and this view must not take them over.
        network = new window.vis.Network(graphCanvas, { nodes: graphNodes, edges: graphEdges }, {
          layout: { hierarchical: { enabled: true, direction: 'LR', sortMethod: 'directed', levelSeparation: 190, nodeSpacing: 46 } },
          physics: { enabled: false },
          edges: { color: { color: '#35494c' }, smooth: { type: 'cubicBezier' }, arrows: { to: { enabled: true, scaleFactor: 0.5 } } },
          interaction: { hover: true, tooltipDelay: 120, keyboard: false },
        });
        network.on('click', (params) => {
          const id = (params as { nodes?: string[] }).nodes?.[0];
          const node = id ? byId.get(id) : undefined;
          if (node) void controller.openFile(ast.path, node.startLine);
        });
        // Fit once the pane has its final size.
        setTimeout(() => network?.fit({ animation: false }), 150);
      })
      .catch((err: unknown) => {
        graphNote.hidden = false;
        graphNote.textContent = `The graph could not be drawn: ${err instanceof Error ? err.message : String(err)}`;
      });
  };

  const unsubscribe = useWorkspaceStore.subscribe((state, previous) => {
    if (state.ast !== previous.ast) {
      render();
      if (mode === 'graph') renderGraph();
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
      graphObserver.disconnect();
      network?.destroy();
      network = null;
    },
  };
}
