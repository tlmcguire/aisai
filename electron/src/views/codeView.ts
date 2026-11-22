import type { AppController } from '../app';
import type { Finding } from '../api/types';
import { h } from '../lib/dom';
import { severityClass, severityColor } from '../lib/severity';
import { useAuditStore } from '../state/audit';
import { useFindingsStore } from '../state/findings';
import { brandMark } from '../lib/logo';
import { useWorkspaceStore } from '../state/workspace';

const LINE_HEIGHT = 20;
const OVERSCAN = 12;

export interface CodeView {
  element: HTMLElement;
  dispose: () => void;
}

export function createCodeView(controller: AppController, container: HTMLElement): CodeView {
  const header = h('div', { class: 'file-header' }, h('span', { text: 'No file open' }));
  const scroll = h('div', { class: 'code-scroll', tabindex: '0' });
  const lines = h('div', { class: 'code-lines' });
  scroll.append(lines);
  container.append(header, scroll);

  // What the DOM currently shows. A render whose inputs match is skipped, so
  // scrolling inside the same window and unrelated store ticks never rebuild
  // (and therefore never flash) the lines.
  let shownKey = '';
  let shownHeader = '';

  /** The first screen: two ways in, and the audits already on disk. */
  const welcome = (): HTMLElement => {
    const scanPackage = h('button', { class: 'welcome-action primary-action', type: 'button' },
      h('span', { class: 'welcome-action-title', text: 'Audit a package' }),
      h('span', { class: 'welcome-action-text', text: 'From PyPI, npm or crates.io: resolve its dependencies, download every source archive and scan it.' }),
    );
    scanPackage.addEventListener('click', () => document.getElementById('package-input')?.focus());
    const openFolder = h('button', { class: 'welcome-action', type: 'button' },
      h('span', { class: 'welcome-action-title', text: 'Open a local project' }),
      h('span', { class: 'welcome-action-text', text: 'Scan a folder on this computer. Any of 14 languages. Or drop it on the window.' }),
    );
    openFolder.addEventListener('click', () => void controller.chooseFolder());
    const recent = useAuditStore.getState().audits.slice(0, 6);
    return h(
      'div',
      { class: 'welcome' },
      h('div', { class: 'welcome-mark' }, brandMark()),
      h('h1', { class: 'welcome-title', text: 'What should we look at?' }),
      h('div', { class: 'welcome-actions' }, scanPackage, openFolder),
      h('p', { class: 'welcome-keys' },
        h('kbd', { text: navigator.userAgent.includes('Mac') ? '⌘O' : 'Ctrl+O' }), ' opens a folder · ',
        h('kbd', { text: 'Enter' }), ' in the package field starts a scan',
      ),
      recent.length
        ? h('div', { class: 'welcome-recent' },
            h('h2', { text: 'Recent audits' }),
            h('ul', {}, ...recent.map((audit) =>
              h('li', {},
                h('button', { class: 'welcome-recent-item', type: 'button', onclick: () => void controller.selectAudit(audit.id) },
                  h('span', { class: 'source-name', text: audit.package }),
                  audit.version ? h('span', { class: 'source-version', text: audit.version }) : null,
                  h('span', { class: 'grow' }),
                  h('span', { class: 'muted', text: `${audit.findingCount ?? 0} findings` }),
                ),
              ),
            )),
          )
        : null,
    );
  };

  const render = (): void => {
    const state = useWorkspaceStore.getState();
    const file = state.file;

    const audits = useAuditStore.getState().audits;
    if (!file && !useAuditStore.getState().current) {
      const key = `welcome|${audits.map((a) => a.id).join(',')}`;
      if (shownKey === key) return;
      shownKey = key;
      shownHeader = '';
      header.replaceChildren(h('span', { text: 'Start' }));
      lines.replaceChildren(welcome());
      return;
    }

    if (!file) {
      if (shownKey === 'none') return;
      shownKey = 'none';
      shownHeader = '';
      header.replaceChildren(h('span', { text: 'No file open' }));
      lines.replaceChildren(
        h(
          'div',
          { class: 'empty-state' },
          h('h3', { text: 'Pick a file to read the code' }),
          h('p', {
            text:
              'Choose a file in the tree on the left, or click a finding in the rail to jump ' +
              'straight to the offending line. Findings are highlighted in the gutter and inline.',
          }),
        ),
      );
      return;
    }

    const allFindings = useFindingsStore.getState().findings;
    const findings = [
      ...(file.findings ?? []),
      ...allFindings.filter(
        (finding) => finding.file === file.path && !(file.findings ?? []).some((f) => f.id === finding.id),
      ),
    ];

    const headerKey = `${file.path}|${findings.length}`;
    if (headerKey !== shownHeader) {
      shownHeader = headerKey;
      header.replaceChildren(
      h('span', { text: file.path }),
      h('span', { class: 'badge neutral', text: file.language || 'text' }),
      h('span', {
        class: `badge ${findings.length ? 'bad' : 'ok'}`,
        text: findings.length ? `${findings.length} finding(s)` : 'no findings',
      }),
      );
    }

    const byLine = new Map<number, Finding[]>();
    for (const finding of findings) {
      const list = byLine.get(finding.startLine) ?? [];
      list.push(finding);
      byLine.set(finding.startLine, list);
    }

    const total = file.lines.length;
    const viewHeight = scroll.clientHeight || 600;
    const start = Math.max(0, Math.floor(scroll.scrollTop / LINE_HEIGHT) - OVERSCAN);
    const end = Math.min(total, start + Math.ceil(viewHeight / LINE_HEIGHT) + OVERSCAN * 2);

    const key = `${file.path}|${start}|${end}|${total}|${findings.map((f) => f.id).join(',')}`;
    if (key === shownKey) return;
    shownKey = key;

    const fragment = document.createDocumentFragment();
    if (start > 0) {
      fragment.appendChild(h('div', { style: `height:${start * LINE_HEIGHT}px` }));
    }
    for (let i = start; i < end; i += 1) {
      const lineNumber = i + 1;
      const lineFindings = byLine.get(lineNumber) ?? [];
      const worst = lineFindings[0];
      fragment.appendChild(
        h(
          'div',
          {
            class: `code-line${lineFindings.length ? ' has-finding' : ''}`,
            'data-line': String(lineNumber),
            title: lineFindings.map((f) => f.message).join('\n'),
          },
          h('span', { class: `gutter${lineFindings.length ? ' has-finding' : ''}`, text: String(lineNumber) }),
          worst
            ? h('span', {
                class: `marker ${severityClass(worst.severity)}`,
                style: `background:${severityColor(worst.severity)}`,
              })
            : h('span', { class: 'marker' }),
          h('span', { class: 'text', text: file.lines[i] ?? '' }),
        ),
      );
    }
    if (end < total) {
      fragment.appendChild(h('div', { style: `height:${(total - end) * LINE_HEIGHT}px` }));
    }
    lines.replaceChildren(fragment);
  };

  const scrollToLine = (line: number): void => {
    const target = Math.max(0, (line - 1) * LINE_HEIGHT - scroll.clientHeight / 3);
    scroll.scrollTop = target;
    render();
    const element = lines.querySelector<HTMLElement>(`.code-line[data-line="${line}"]`);
    if (element) {
      element.classList.remove('flash');
      // Force a reflow so the animation restarts for the same line.
      void element.offsetHeight;
      element.classList.add('flash');
      element.scrollIntoView({ block: 'center' });
    }
  };

  let frame = 0;
  scroll.addEventListener(
    'scroll',
    () => {
      if (frame) return;
      frame = requestAnimationFrame(() => {
        frame = 0;
        render();
      });
    },
    { passive: true },
  );

  const unsubscribe = useWorkspaceStore.subscribe((state, previous) => {
    if (state.file !== previous.file) {
      render();
      return;
    }
    if (state.jumpToken !== previous.jumpToken && state.jumpLine) {
      scrollToLine(state.jumpLine);
    }
  });

  const unsubscribeFindings = useFindingsStore.subscribe((state, previous) => {
    if (state.findings !== previous.findings) {
      render();
    }
  });

  const unsubscribeAudit = useAuditStore.subscribe((state, previous) => {
    if (state.current !== previous.current || state.audits !== previous.audits) render();
  });

  render();

  return {
    element: container,
    dispose: () => {
      unsubscribe();
      unsubscribeFindings();
      unsubscribeAudit();
    },
  };
}
