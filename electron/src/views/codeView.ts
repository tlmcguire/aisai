import type { AppController } from '../app';
import type { Finding } from '../api/types';
import { h } from '../lib/dom';
import { severityClass, severityColor } from '../lib/severity';
import { useFindingsStore } from '../state/findings';
import { useWorkspaceStore } from '../state/workspace';

const LINE_HEIGHT = 20;
const OVERSCAN = 12;

export interface CodeView {
  element: HTMLElement;
  dispose: () => void;
}

export function createCodeView(_controller: AppController, container: HTMLElement): CodeView {
  const header = h('div', { class: 'file-header' }, h('span', { text: 'No file open' }));
  const scroll = h('div', { class: 'code-scroll', tabindex: '0' });
  const lines = h('div', { class: 'code-lines' });
  scroll.append(lines);
  container.append(header, scroll);

  const render = (): void => {
    const state = useWorkspaceStore.getState();
    const file = state.file;

    if (!file) {
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

    header.replaceChildren(
      h('span', { text: file.path }),
      h('span', { class: 'badge neutral', text: file.language || 'text' }),
      h('span', {
        class: `badge ${findings.length ? 'bad' : 'ok'}`,
        text: findings.length ? `${findings.length} finding(s)` : 'no findings',
      }),
    );

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

  scroll.addEventListener('scroll', () => render());

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

  render();

  return {
    element: container,
    dispose: () => {
      unsubscribe();
      unsubscribeFindings();
    },
  };
}
