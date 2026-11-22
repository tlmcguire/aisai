/**
 * The findings rail: every finding of the audit as a card, with search,
 * severity chips, CWE, tool and state filters, sorting, and triage actions
 * (confirm, suppress until a date, mark fixed, propose a fix). Its bottom
 * row exports the report as PDF or HTML; File › Export Report and its
 * shortcut arrive here as aisai:export-report events.
 */

import type { AppController } from '../app';
import { SEVERITIES, type Finding, type FindingState, type Severity } from '../api/types';
import { h } from '../lib/dom';
import { t } from '../lib/i18n';
import { setBusy } from './toolbar';
import { confirmDialog } from './confirm';
import { icon } from '../lib/icons';
import { severityClass, severityLabel, severityWeight } from '../lib/severity';
import { useFindingsStore, type FindingSortKey } from '../state/findings';

/** The sort keys, with their labels in the current language. */
function sortKeys(): Array<{ key: FindingSortKey; label: string }> {
  return [
    { key: 'severity', label: t('findings.sortSeverity') },
    { key: 'confidence', label: t('findings.sortConfidence') },
    { key: 'cwe', label: t('findings.sortCwe') },
    { key: 'file', label: t('findings.sortFile') },
    { key: 'tool', label: t('findings.sortTool') },
  ];
}

function defaultExpiry(): string {
  const date = new Date();
  date.setDate(date.getDate() + 30);
  return date.toISOString().slice(0, 10);
}

export interface FindingsRail {
  element: HTMLElement;
  dispose: () => void;
}

export function createFindingsRail(controller: AppController, container: HTMLElement): FindingsRail {
  const searchInput = h('input', {
    type: 'search',
    placeholder: t('findings.filterPlaceholder'),
    'aria-label': t('findings.searchLabel'),
  });

  const severityChips = SEVERITIES.map((severity) =>
    h(
      'button',
      {
        class: `chip ${severityClass(severity)}`,
        type: 'button',
        'aria-pressed': 'false',
        // The severity is a value, so it travels in the markup rather than
        // in the label, which is translated.
        dataset: { severity },
        onclick: () => useFindingsStore.getState().toggleSeverity(severity),
      },
      severityLabel(severity),
    ),
  );

  const cweSelect = h(
    'select',
    { 'aria-label': t('findings.filterByCwe'), onchange: () => onSelectChange(cweSelect, 'cwe') },
    h('option', { value: '' }, t('findings.allCwes')),
  );
  const toolSelect = h(
    'select',
    { 'aria-label': t('findings.filterByTool'), onchange: () => onSelectChange(toolSelect, 'tool') },
    h('option', { value: '' }, t('findings.allTools')),
  );
  const stateSelect = h(
    'select',
    { 'aria-label': t('findings.filterByState'), onchange: () => onSelectChange(stateSelect, 'state') },
    h('option', { value: '' }, t('findings.anyState')),
    h('option', { value: 'open' }, t('findings.stateOpen')),
    h('option', { value: 'confirmed' }, t('findings.stateConfirmed')),
    h('option', { value: 'suppressed' }, t('findings.stateSuppressed')),
    h('option', { value: 'fixed' }, t('findings.stateFixed')),
  );

  const expiryInput = h('input', {
    type: 'date',
    'aria-label': t('findings.suppressUntilDateLabel'),
    value: defaultExpiry(),
    title: t('findings.expiryDateTitle'),
  });

  const showSuppressed = h('input', {
    type: 'checkbox',
    id: 'show-suppressed',
    onchange: () => useFindingsStore.getState().setShowSuppressed(showSuppressed.checked),
  });

  const sortRow = h(
    'div',
    { class: 'sort-row' },
    h('span', { text: t('findings.sortLabel') }),
    ...sortKeys().map(({ key, label }) =>
      h(
        'button',
        {
          class: 'chip',
          type: 'button',
          dataset: { sort: key },
          onclick: () => useFindingsStore.getState().setSort(key),
        },
        h('span', { class: 'sort-label', text: label }),
        h('span', { class: 'sort-dir' }),
      ),
    ),
  );

  const list = h('ul', { class: 'finding-list' });
  // The report, as a PDF to share or as the HTML page it is printed from.
  const pdfButton = h('button', { class: 'tiny', type: 'button', title: t('findings.exportPdfTitle') }, h('span', { class: 'btn-label', text: 'PDF' }));
  const htmlButton = h('button', { class: 'tiny', type: 'button', title: t('findings.exportHtmlTitle') }, h('span', { class: 'btn-label', text: 'HTML' }));
  const exportStatus = h('span', { class: 'export-status', role: 'status', 'aria-live': 'polite' });
  const runExport = async (format: 'pdf' | 'html', button: HTMLButtonElement): Promise<void> => {
    setBusy(button, true);
    exportStatus.className = 'export-status';
    exportStatus.textContent = format === 'pdf' ? t('findings.makingPdf') : '';
    const result = await controller.exportReport(format);
    setBusy(button, false);
    if (result.error) {
      exportStatus.className = 'export-status is-error';
      exportStatus.textContent = t('findings.exportFailed', { error: result.error });
      return;
    }
    if (!result.saved || !result.path) {
      exportStatus.textContent = '';
      return;
    }
    const saved = result.path;
    exportStatus.replaceChildren(
      t('findings.savedFile', { name: saved.split(/[\\/]/).pop() ?? t('findings.thePdf') }),
      h('button', { class: 'link-button', type: 'button', text: t('findings.show'), onclick: () => void window.aisai.showInFolder(saved) }),
    );
  };
  // File › Export Report and its shortcut arrive here, so they share the status line.
  window.addEventListener('aisai:export-report', async (event) => {
    const format = (event as CustomEvent<'pdf' | 'html'>).detail === 'html' ? 'html' : 'pdf';
    if (!useFindingsStore.getState().findings.length) {
      const ok = await confirmDialog(t('findings.exportEmptyConfirm'), { confirmLabel: t('findings.exportAnyway'), cancelLabel: t('findings.cancel') });
      if (!ok) return;
    }
    void runExport(format, format === 'pdf' ? pdfButton : htmlButton);
  });
  pdfButton.addEventListener('click', () => void runExport('pdf', pdfButton));
  htmlButton.addEventListener('click', () => void runExport('html', htmlButton));
  const reportRow = h('div', { class: 'row export-row' }, h('span', { class: 'export-label', text: t('findings.exportReport') }), pdfButton, htmlButton, exportStatus);

  const controls = h(
    'div',
    { class: 'rail-controls' },
    h('div', { class: 'row' }, searchInput),
    h('div', { class: 'row' }, ...severityChips),
    h('div', { class: 'row' }, cweSelect, toolSelect, stateSelect),
    h(
      'div',
      { class: 'row' },
      h('label', { for: 'show-suppressed', text: t('findings.showSuppressed') }),
      showSuppressed,
      h('span', { text: t('findings.suppressUntil') }),
      expiryInput,
    ),
    sortRow,
    reportRow,
  );

  const body = h('div', { class: 'pane-body' }, list);
  container.dataset.tour = 'findings';
  reportRow.dataset.tour = 'export';
  container.append(controls, body);

  function onSelectChange(select: HTMLSelectElement, kind: 'cwe' | 'tool' | 'state'): void {
    const value = select.value;
    const store = useFindingsStore.getState();
    if (kind === 'cwe') {
      store.toggleCwe(value);
      select.value = '';
    } else if (kind === 'tool') {
      store.toggleTool(value);
      select.value = '';
    } else {
      store.toggleState(value as FindingState);
      select.value = '';
    }
  }

  function visibleFindings(): Finding[] {
    const state = useFindingsStore.getState();
    const search = state.search.trim().toLowerCase();
    const list = state.findings.filter((finding) => {
      if (!state.showSuppressed && finding.state === 'suppressed') return false;
      if (state.severities.length && !state.severities.includes(finding.severity)) return false;
      if (state.cwes.length && !(finding.cwe && state.cwes.includes(finding.cwe))) return false;
      if (state.tools.length && !state.tools.includes(finding.tool)) return false;
      if (state.packages.length && !(finding.package && state.packages.includes(finding.package)))
        return false;
      if (state.states.length && !state.states.includes(finding.state)) return false;
      if (!search) return true;
      return (
        finding.message.toLowerCase().includes(search) ||
        finding.file.toLowerCase().includes(search) ||
        finding.id.toLowerCase().includes(search) ||
        (finding.cwe ?? '').toLowerCase().includes(search) ||
        finding.rule.toLowerCase().includes(search)
      );
    });

    const direction = state.sortDir === 'asc' ? 1 : -1;
    const weight = (finding: Finding): number | string => {
      switch (state.sortKey) {
        case 'severity':
          return severityWeight(finding.severity);
        case 'confidence':
          return finding.confidence;
        case 'cwe':
          return finding.cwe ?? '';
        case 'file':
          return finding.file;
        case 'tool':
          return finding.tool;
        default:
          return 0;
      }
    };
    return list.sort((a, b) => {
      const left = weight(a);
      const right = weight(b);
      if (left === right) return a.id.localeCompare(b.id);
      return (left > right ? 1 : -1) * direction;
    });
  }

  function findingCard(finding: Finding): HTMLElement {
    const store = useFindingsStore.getState();
    const selected = store.selectedId === finding.id;

    const actions = h(
      'div',
      { class: 'actions' },
      h(
        'button',
        {
          class: 'tiny',
          type: 'button',
          onclick: (event: Event) => {
            event.stopPropagation();
            void controller.setFindingState(finding.id, 'confirmed');
          },
        },
        t('findings.confirm'),
      ),
      h(
        'button',
        {
          class: 'tiny',
          type: 'button',
          onclick: (event: Event) => {
            event.stopPropagation();
            const date = expiryInput.value || defaultExpiry();
            void controller.setFindingState(
              finding.id,
              'suppressed',
              new Date(`${date}T00:00:00Z`).toISOString(),
            );
          },
        },
        t('findings.suppress'),
      ),
      h(
        'button',
        {
          class: 'tiny',
          type: 'button',
          onclick: (event: Event) => {
            event.stopPropagation();
            void controller.setFindingState(finding.id, 'fixed');
          },
        },
        t('findings.markFixed'),
      ),
      h(
        'button',
        {
          class: 'tiny',
          type: 'button',
          onclick: (event: Event) => {
            event.stopPropagation();
            void controller.proposeFix(finding.id);
          },
        },
        t('findings.proposeFix'),
      ),
    );

    const meta = h(
      'div',
      { class: 'meta' },
      h('span', { text: `${finding.file}:${finding.startLine}` }),
      h('span', { text: t('findings.toolLabel', { tool: finding.tool }) }),
      h('span', { text: t('findings.ruleLabel', { rule: finding.rule }) }),
      finding.reachability ? h('span', { text: finding.reachability }) : null,
      finding.state !== 'open'
        ? h('span', {
            text:
              finding.state === 'suppressed' && finding.suppressedUntil
                ? t('findings.suppressedUntil', { date: finding.suppressedUntil.slice(0, 10) })
                : finding.state,
          })
        : null,
    );

    const confidence = h(
      'span',
      { class: 'confidence-bar', title: t('findings.confidenceTitle', { confidence: (finding.confidence * 100).toFixed(0) }) },
      h('span', { class: 'confidence-track' },
        h('span', {
          class: 'confidence-fill',
          style: `width:${Math.round(finding.confidence * 100)}%`,
        }),
      ),
      `${(finding.confidence * 100).toFixed(0)}%`,
    );

    return h(
      'li',
      {
        class: `finding-card ${severityClass(finding.severity)}${selected ? ' selected' : ''}`,
        tabindex: '0',
        role: 'button',
        'aria-label': t('findings.cardLabel', { severity: severityLabel(finding.severity), file: finding.file, line: finding.startLine }),
        onclick: () => {
          useFindingsStore.getState().select(finding.id);
          void controller.openFile(finding.file, finding.startLine);
        },
        onkeydown: (event: Event) => {
          const key = (event as KeyboardEvent).key;
          if (key === 'Enter' || key === ' ') {
            event.preventDefault();
            useFindingsStore.getState().select(finding.id);
            void controller.openFile(finding.file, finding.startLine);
          }
        },
      },
      h(
        'div',
        { class: 'head' },
        h('span', { class: `badge ${severityClass(finding.severity)}`, text: severityLabel(finding.severity) }),
        finding.cwe ? h('span', { class: 'badge neutral', text: finding.cwe }) : null,
        h('span', { class: 'badge neutral', text: finding.tool }),
        confidence,
      ),
      h('div', { class: 'message', text: finding.message }),
      meta,
      actions,
    );
  }

  const cards = new Map<string, { signature: string; element: HTMLElement }>();
  let listMode: 'none' | 'filtered' | 'list' | '' = '';

  const render = (): void => {
    const state = useFindingsStore.getState();
    const findings = visibleFindings();

    // Filter dropdown options reflect what is actually present.
    const cwes = Array.from(new Set(state.findings.map((f) => f.cwe).filter((c): c is string => !!c)));
    const tools = Array.from(new Set(state.findings.map((f) => f.tool)));
    syncOptions(cweSelect, cwes, t('findings.allCwes'));
    syncOptions(toolSelect, tools, t('findings.allTools'));

    for (const chip of severityChips) {
      const severity = chip.dataset.severity as Severity | undefined;
      chip.setAttribute(
        'aria-pressed',
        String(severity ? state.severities.includes(severity) : false),
      );
    }
    for (const button of Array.from(sortRow.querySelectorAll<HTMLButtonElement>('button'))) {
      const key = button.dataset.sort as FindingSortKey | undefined;
      const active = key === state.sortKey;
      button.setAttribute('aria-pressed', String(active));
      // Only the active key shows its direction; the arrow is redrawn only
      // when the direction changes.
      const slot = button.querySelector<HTMLElement>('.sort-dir');
      const want = active ? (state.sortDir === 'asc' ? 'up' : 'down') : '';
      if (slot && slot.dataset.dir !== want) {
        slot.dataset.dir = want;
        slot.replaceChildren(
          want ? icon(want === 'up' ? 'arrow-up' : 'arrow-down', want === 'up' ? t('findings.sortAscending') : t('findings.sortDescending')) : '',
        );
      }
    }

    if (!state.findings.length) {
      if (listMode === 'none') return;
      listMode = 'none';
      cards.clear();
      list.replaceChildren(
        h(
          'li',
          { class: 'empty-state' },
          h('h3', { text: t('findings.emptyTitle') }),
          h('p', { text: t('findings.emptyDescription') }),
        ),
      );
      return;
    }

    if (!findings.length) {
      if (listMode === 'filtered') return;
      listMode = 'filtered';
      cards.clear();
      list.replaceChildren(
        h(
          'li',
          { class: 'empty-state' },
          h('h3', { text: t('findings.filteredTitle') }),
          h('p', { text: t('findings.filteredDescription') }),
        ),
      );
      return;
    }

    if (listMode !== 'list') {
      list.replaceChildren();
      listMode = 'list';
    }
    // Reconcile by finding id: a card is rebuilt only when what it shows
    // changed, and the rest keep their element, hover and focus.
    const live = new Set<string>();
    findings.forEach((finding, index) => {
      live.add(finding.id);
      const signature = JSON.stringify([
        finding.severity, finding.state, finding.message, finding.confidence, finding.cwe,
        finding.tool, finding.rule, finding.reachability, finding.suppressedUntil,
        finding.file, finding.startLine, state.selectedId === finding.id,
      ]);
      let entry = cards.get(finding.id);
      if (!entry || entry.signature !== signature) {
        const element = findingCard(finding);
        if (entry) entry.element.replaceWith(element);
        entry = { signature, element };
        cards.set(finding.id, entry);
      }
      if (list.children[index] !== entry.element) {
        list.insertBefore(entry.element, list.children[index] ?? null);
      }
    });
    for (const [id, entry] of cards) {
      if (!live.has(id)) {
        entry.element.remove();
        cards.delete(id);
      }
    }
  };

  function syncOptions(select: HTMLSelectElement, values: string[], placeholder: string): void {
    const current = Array.from(select.options).map((option) => option.value);
    const wanted = ['', ...values];
    if (current.length === wanted.length && current.every((value, index) => value === wanted[index])) {
      return;
    }
    const selected = select.value;
    select.replaceChildren(
      h('option', { value: '' }, placeholder),
      ...values.map((value) => h('option', { value }, value)),
    );
    select.value = selected;
  }

  searchInput.addEventListener('input', () => {
    useFindingsStore.getState().setSearch(searchInput.value);
  });

  const unsubscribe = useFindingsStore.subscribe(() => render());

  render();

  return {
    element: container,
    dispose: unsubscribe,
  };
}
