/**
 * The Diff tab: a proposed patch shown side by side, from the unified diff
 * the backend returns (parsed in lib/diff.ts), with Apply to write it to the
 * copy of the code and Save to export it as a .patch file for git apply.
 */

import type { AppController } from '../app';
import type { Finding } from '../api/types';
import { diffStats, parseUnifiedDiff, toSideBySide, type DiffHunk } from '../lib/diff';
import { h, type Child } from '../lib/dom';
import { severityClass, severityLabel } from '../lib/severity';
import { useAuditStore } from '../state/audit';
import { useFindingsStore } from '../state/findings';
import { useWorkspaceStore } from '../state/workspace';
import { setBusy } from './toolbar';

type DiffMode = 'split' | 'unified';

export interface DiffView {
  element: HTMLElement;
  dispose: () => void;
}

export function createDiffView(controller: AppController, container: HTMLElement): DiffView {
  const toolbar = h('div', { class: 'diff-toolbar patch-toolbar' });
  const body = h('div', { class: 'diff-body' });
  container.append(toolbar, body);

  let mode: DiffMode = 'split';

  const splitButton = h('button', { class: 'tiny', type: 'button' }, 'Side-by-side');
  const unifiedButton = h('button', { class: 'tiny', type: 'button' }, 'Unified');
  const applyButton = h('button', { class: 'primary', type: 'button' }, 'Apply');
  const rejectButton = h('button', { class: 'tiny', type: 'button' }, 'Reject');
  const copyButton = h('button', { class: 'tiny', type: 'button' }, 'Copy');
  const saveButton = h('button', { class: 'tiny', type: 'button' }, 'Save .patch');
  const summary = h('span', { class: 'legend-item', text: '' });

  splitButton.addEventListener('click', () => {
    mode = 'split';
    render();
  });
  unifiedButton.addEventListener('click', () => {
    mode = 'unified';
    render();
  });

  applyButton.addEventListener('click', () => {
    const state = useWorkspaceStore.getState();
    const patch = state.patch;
    if (!patch) return;
    const finding = useFindingsStore
      .getState()
      .findings.find((f) => f.id === patch.findingId);
    const path = finding?.file ?? state.file?.path ?? '';
    if (!path) return;
    setBusy(applyButton, true);
    void controller.applyPatch(path).then((result) => {
      setBusy(applyButton, false);
      if (result?.applied) {
        summary.textContent = `Applied. ${result.rescan.resolved.length} finding(s) resolved, ${result.rescan.unchanged} unchanged.`;
      }
    });
  });

  rejectButton.addEventListener('click', () => {
    useWorkspaceStore.getState().setPatch(null);
    summary.textContent = 'Patch rejected.';
    render();
  });

  copyButton.addEventListener('click', () => {
    const patch = useWorkspaceStore.getState().patch;
    if (!patch) return;
    void navigator.clipboard
      ?.writeText(patch.unified)
      .then(() => {
        summary.textContent = 'Diff copied to the clipboard.';
      })
      .catch(() => {
        summary.textContent = 'Could not copy the diff — select it manually.';
      });
  });

  saveButton.addEventListener('click', () => {
    const patch = useWorkspaceStore.getState().patch;
    if (!patch) return;
    const blob = new Blob([patch.unified], { type: 'text/x-patch' });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement('a');
    anchor.href = url;
    anchor.download = 'aisai-fix.patch';
    document.body.appendChild(anchor);
    anchor.click();
    anchor.remove();
    setTimeout(() => URL.revokeObjectURL(url), 5000);
  });

  toolbar.append(splitButton, unifiedButton, h('span', { class: 'grow' }), summary, copyButton, saveButton, rejectButton, applyButton);

  function hunkHeader(hunk: DiffHunk, finding: Finding | undefined): HTMLElement {
    const children: Child[] = [h('span', { class: 'finding-id', text: hunk.header || 'patch' })];
    if (finding) {
      children.push(
        h('span', { class: 'badge neutral', text: finding.id }),
        h('span', { class: `badge ${severityClass(finding.severity)}`, text: severityLabel(finding.severity) }),
        finding.cwe ? h('span', { class: 'badge neutral', text: finding.cwe }) : null,
        h('span', {
          class: 'legend-item',
          text: `confidence ${(finding.confidence * 100).toFixed(0)}%`,
        }),
        h('span', { class: 'legend-item', text: finding.tool }),
      );
    } else {
      children.push(h('span', { class: 'legend-item', text: 'no matching finding' }));
    }
    return h('div', { class: 'hunk-header' }, ...children);
  }

  const render = (): void => {
    const patch = useWorkspaceStore.getState().patch;
    toolbar.hidden = !patch;
    splitButton.setAttribute('aria-pressed', String(mode === 'split'));
    unifiedButton.setAttribute('aria-pressed', String(mode === 'unified'));

    if (!patch) {
      body.replaceChildren(
        h(
          'div',
          { class: 'empty-state' },
          h('h3', { text: 'No proposed fix yet' }),
          h('p', {
            text:
              'Pick a finding in the rail and choose “Propose fix”. The model proposes a patch and ' +
              'it is shown here as a unified diff you can review, copy or apply.',
          }),
          h('ol', {},
            h('li', { text: 'Select a finding in the findings rail.' }),
            h('li', { text: 'Press “Propose fix”.' }),
            h('li', { text: 'Review the red/green diff, then Apply or Reject.' }),
          ),
        ),
      );
      return;
    }

    const finding = useFindingsStore.getState().findings.find((f) => f.id === patch.findingId);
    const hunks = parseUnifiedDiff(patch.unified);
    const stats = diffStats(hunks);

    const fragment = document.createDocumentFragment();
    fragment.append(
      h('div', { class: 'hunk-header' },
        h('span', { class: 'finding-id', text: patch.summary || 'Proposed fix' }),
        h('span', { class: 'badge ok', text: `+${stats.added}` }),
        h('span', { class: 'badge bad', text: `-${stats.removed}` }),
        patch.valid
          ? h('span', { class: 'badge ok', text: 'valid patch' })
          : h('span', { class: 'badge bad', text: `invalid: ${patch.error}` }),
      ),
    );

    for (const hunk of hunks) {
      const section = h('div', { class: 'hunk' }, hunkHeader(hunk, finding));
      if (mode === 'unified') {
        for (const line of hunk.lines) {
          section.appendChild(
            h(
              'div',
              { class: `diff-line ${line.kind}` },
              h('span', { class: 'no', text: line.newNo ? String(line.newNo) : '' }),
              h('span', { class: 'sign', text: signFor(line.kind) }),
              h('span', { class: 'text', text: line.text }),
            ),
          );
        }
      } else {
        const sides = h('div', { class: 'diff-side' });
        const left = h('div', {});
        const right = h('div', {});
        for (const row of toSideBySide(hunk)) {
          left.appendChild(
            h(
              'div',
              { class: `diff-line ${row.left?.kind ?? 'meta'}` },
              h('span', { class: 'no', text: row.left?.oldNo ? String(row.left.oldNo) : '' }),
              h('span', { class: 'sign', text: row.left ? signFor(row.left.kind) : '' }),
              h('span', { class: 'text', text: row.left?.text ?? '' }),
            ),
          );
          right.appendChild(
            h(
              'div',
              { class: `diff-line ${row.right?.kind ?? 'meta'}` },
              h('span', { class: 'no', text: row.right?.newNo ? String(row.right.newNo) : '' }),
              h('span', { class: 'sign', text: row.right ? signFor(row.right.kind) : '' }),
              h('span', { class: 'text', text: row.right?.text ?? '' }),
            ),
          );
        }
        sides.append(left, right);
        section.appendChild(sides);
      }
      fragment.appendChild(section);
    }

    body.replaceChildren(fragment);
  };

  function signFor(kind: string): string {
    if (kind === 'add') return '+';
    if (kind === 'remove') return '-';
    return ' ';
  }

  const unsubscribe = useWorkspaceStore.subscribe((state, previous) => {
    if (state.patch !== previous.patch) {
      render();
    }
  });

  const unsubscribeFindings = useFindingsStore.subscribe((state, previous) => {
    if (state.findings !== previous.findings) {
      render();
    }
  });

  // Progress ticks arrive many times a second; only a change of job matters.
  const unsubscribeAudit = useAuditStore.subscribe((state, previous) => {
    if (state.busy !== previous.busy || state.current !== previous.current) render();
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
