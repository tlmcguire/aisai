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
import { setBusy } from './toolbar';
import { confirmDialog } from './confirm';
import { icon } from '../lib/icons';
import { severityClass, severityLabel, severityWeight } from '../lib/severity';
import { useFindingsStore, type FindingSortKey } from '../state/findings';

const SORT_KEYS: Array<{ key: FindingSortKey; label: string }> = [
  { key: 'severity', label: 'Severity' },
  { key: 'confidence', label: 'Confidence' },
  { key: 'cwe', label: 'CWE' },
  { key: 'file', label: 'File' },
  { key: 'tool', label: 'Tool' },
];

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
    placeholder: 'Filter findings…',
    'aria-label': 'Search findings',
  });

  const severityChips = SEVERITIES.map((severity) =>
    h(
      'button',
      {
        class: `chip ${severityClass(severity)}`,
        type: 'button',
        'aria-pressed': 'false',
        onclick: () => useFindingsStore.getState().toggleSeverity(severity),
      },
      severityLabel(severity),
    ),
  );

  const cweSelect = h(
    'select',
    { 'aria-label': 'Filter by CWE', onchange: () => onSelectChange(cweSelect, 'cwe') },
    h('option', { value: '' }, 'All CWEs'),
  );
  const toolSelect = h(
    'select',
    { 'aria-label': 'Filter by tool', onchange: () => onSelectChange(toolSelect, 'tool') },
    h('option', { value: '' }, 'All tools'),
  );
  const stateSelect = h(
    'select',
    { 'aria-label': 'Filter by triage state', onchange: () => onSelectChange(stateSelect, 'state') },
    h('option', { value: '' }, 'Any state'),
    h('option', { value: 'open' }, 'Open'),
    h('option', { value: 'confirmed' }, 'Confirmed'),
    h('option', { value: 'suppressed' }, 'Suppressed'),
    h('option', { value: 'fixed' }, 'Fixed'),
  );

  const expiryInput = h('input', {
    type: 'date',
    'aria-label': 'Suppress until date',
    value: defaultExpiry(),
    title: 'Expiry date used when suppressing a finding',
  });

  const showSuppressed = h('input', {
    type: 'checkbox',
    id: 'show-suppressed',
    onchange: () => useFindingsStore.getState().setShowSuppressed(showSuppressed.checked),
  });

  const sortRow = h(
    'div',
    { class: 'sort-row' },
    h('span', { text: 'Sort:' }),
    ...SORT_KEYS.map(({ key, label }) =>
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
  const pdfButton = h('button', { class: 'tiny', type: 'button', title: 'Save the findings report as a PDF, laid out for paper' }, h('span', { class: 'btn-label', text: 'PDF' }));
  const htmlButton = h('button', { class: 'tiny', type: 'button', title: 'Download the findings report as one HTML page, in the aisai theme' }, h('span', { class: 'btn-label', text: 'HTML' }));
  const exportStatus = h('span', { class: 'export-status', role: 'status', 'aria-live': 'polite' });
  const runExport = async (format: 'pdf' | 'html', button: HTMLButtonElement): Promise<void> => {
    setBusy(button, true);
    exportStatus.className = 'export-status';
    exportStatus.textContent = format === 'pdf' ? 'Making the PDF…' : '';
    const result = await controller.exportReport(format);
    setBusy(button, false);
    if (result.error) {
      exportStatus.className = 'export-status is-error';
      exportStatus.textContent = `Export failed: ${result.error}`;
      return;
    }
    if (!result.saved || !result.path) {
      exportStatus.textContent = '';
      return;
    }
    const saved = result.path;
    exportStatus.replaceChildren(
      `Saved ${saved.split(/[\\/]/).pop() ?? 'the PDF'} · `,
      h('button', { class: 'link-button', type: 'button', text: 'Show', onclick: () => void window.aisai.showInFolder(saved) }),
    );
  };
  // File › Export Report and its shortcut arrive here, so they share the status line.
  window.addEventListener('aisai:export-report', async (event) => {
    const format = (event as CustomEvent<'pdf' | 'html'>).detail === 'html' ? 'html' : 'pdf';
    if (!useFindingsStore.getState().findings.length) {
      const ok = await confirmDialog('There are no findings yet, so the report will be empty. Export it anyway?', { confirmLabel: 'Export anyway', cancelLabel: 'Cancel' });
      if (!ok) return;
    }
    void runExport(format, format === 'pdf' ? pdfButton : htmlButton);
  });
  pdfButton.addEventListener('click', () => void runExport('pdf', pdfButton));
  htmlButton.addEventListener('click', () => void runExport('html', htmlButton));
  const reportRow = h('div', { class: 'row export-row' }, h('span', { class: 'export-label', text: 'Export report' }), pdfButton, htmlButton, exportStatus);

  const controls = h(
    'div',
    { class: 'rail-controls' },
    h('div', { class: 'row' }, searchInput),
    h('div', { class: 'row' }, ...severityChips),
    h('div', { class: 'row' }, cweSelect, toolSelect, stateSelect),
    h(
      'div',
      { class: 'row' },
      h('label', { for: 'show-suppressed', text: 'Show suppressed' }),
      showSuppressed,
      h('span', { text: 'Suppress until' }),
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
        'Confirm',
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
        'Suppress',
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
        'Mark fixed',
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
        'Propose fix',
      ),
    );

    const meta = h(
      'div',
      { class: 'meta' },
      h('span', { text: `${finding.file}:${finding.startLine}` }),
      h('span', { text: `tool: ${finding.tool}` }),
      h('span', { text: `rule: ${finding.rule}` }),
      finding.reachability ? h('span', { text: finding.reachability }) : null,
      finding.state !== 'open'
        ? h('span', {
            text:
              finding.state === 'suppressed' && finding.suppressedUntil
                ? `suppressed until ${finding.suppressedUntil.slice(0, 10)}`
                : finding.state,
          })
        : null,
    );

    const confidence = h(
      'span',
      { class: 'confidence-bar', title: `Confidence ${(finding.confidence * 100).toFixed(0)}%` },
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
        'aria-label': `${severityLabel(finding.severity)} finding in ${finding.file} line ${finding.startLine}`,
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
    syncOptions(cweSelect, cwes, 'All CWEs');
    syncOptions(toolSelect, tools, 'All tools');

    for (const chip of severityChips) {
      const severity = chip.textContent?.toLowerCase() as Severity | undefined;
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
          want ? icon(want === 'up' ? 'arrow-up' : 'arrow-down', want === 'up' ? 'ascending' : 'descending') : '',
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
          h('h3', { text: 'No findings yet' }),
          h('p', {
            text:
              'Pull a package from the toolbar, then run a Static scan or an LLM scan to analyse the downloaded ' +
              'workspace. Findings appear here with severity, confidence, CWE and triage actions.',
          }),
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
          h('h3', { text: 'Nothing matches these filters' }),
          h('p', {
            text: 'Clear the search box or the severity/CWE filters to see the rest of the findings.',
          }),
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
