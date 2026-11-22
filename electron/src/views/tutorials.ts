/**
 * Tutorials: short guided tours of the app.
 *
 * A tour is a list of steps. Each step points at a control (found by its
 * data-tour attribute, set where the control is built) with a ring around it
 * and a card beside it that explains what it does. Steps can prepare the
 * screen first (open the terminal's Debug view, a Settings section). A step
 * whose control is not on screen (a pane is hidden, nothing is open yet)
 * shows its card in the middle instead, so a tour never gets stuck.
 *
 * Tours only point and explain: they never pull, scan, run or change
 * anything themselves. Finished tours are remembered on this computer.
 */

import { h } from '../lib/dom';
import { t, tn } from '../lib/i18n';
import { icon } from '../lib/icons';
import { isMac } from '../lib/shortcuts';
import { useUiStore } from '../state/ui';

interface Step {
  /** The data-tour value of the control to point at; none for a card in the middle. */
  target?: string;
  /** The heading of the card; a function, so it is looked up when the step is shown. */
  title: () => string;
  /** The text of the card; a function, for the same reason. */
  body: () => string;
  /** Gets the screen ready for the step. */
  before?: () => void;
}

interface Tour {
  id: string;
  title: () => string;
  summary: () => string;
  minutes: number;
  steps: Step[];
}

const mod = (): string => (isMac() ? '⌘' : 'Ctrl+');
const closeSettings = (): void => useUiStore.getState().setSettingsOpen(false);

/**
 * The tours. They are built when they are shown, so the text is looked up in
 * the language of the moment.
 */
export const TOURS = (): Tour[] => [
  {
    id: 'first-audit',
    title: () => t('tour.firstAuditTitle'),
    summary: () => t('tour.firstAuditSummary'),
    minutes: 2,
    steps: [
      {
        title: () => t('tour.firstAuditStep1Title'),
        body: () => t('tour.firstAuditStep1Body'),
        before: closeSettings,
      },
      {
        target: 'pull',
        title: () => t('tour.firstAuditStep2Title'),
        body: () => t('tour.firstAuditStep2Body'),
      },
      {
        target: 'open-folder',
        title: () => t('tour.firstAuditStep3Title'),
        body: () => t('tour.firstAuditStep3Body', { mod: mod() }),
      },
      {
        target: 'graph',
        title: () => t('tour.firstAuditStep4Title'),
        body: () => t('tour.firstAuditStep4Body'),
      },
      {
        target: 'why',
        title: () => t('tour.firstAuditStep5Title'),
        body: () => t('tour.firstAuditStep5Body'),
      },
      {
        target: 'static-scan',
        title: () => t('tour.firstAuditStep6Title'),
        body: () => t('tour.firstAuditStep6Body', { mod: mod() }),
      },
      {
        target: 'findings',
        title: () => t('tour.firstAuditStep7Title'),
        body: () => t('tour.firstAuditStep7Body'),
      },
      {
        target: 'code',
        title: () => t('tour.firstAuditStep8Title'),
        body: () => t('tour.firstAuditStep8Body'),
      },
      {
        title: () => t('tour.firstAuditStep9Title'),
        body: () => t('tour.firstAuditStep9Body'),
      },
    ],
  },
  {
    id: 'triage',
    title: () => t('tour.triageTitle'),
    summary: () => t('tour.triageSummary'),
    minutes: 2,
    steps: [
      {
        target: 'findings',
        title: () => t('tour.triageStep1Title'),
        body: () => t('tour.triageStep1Body'),
        before: closeSettings,
      },
      {
        target: 'findings',
        title: () => t('tour.triageStep2Title'),
        body: () => t('tour.triageStep2Body'),
      },
      {
        target: 'code',
        title: () => t('tour.triageStep3Title'),
        body: () => t('tour.triageStep3Body'),
      },
      {
        target: 'export',
        title: () => t('tour.triageStep4Title'),
        body: () => t('tour.triageStep4Body', { mod: mod() }),
      },
    ],
  },
  {
    id: 'assistant',
    title: () => t('tour.assistantTitle'),
    summary: () => t('tour.assistantSummary'),
    minutes: 2,
    steps: [
      {
        target: 'chat',
        title: () => t('tour.assistantStep1Title'),
        body: () => t('tour.assistantStep1Body'),
        before: closeSettings,
      },
      {
        target: 'chat-input',
        title: () => t('tour.assistantStep2Title'),
        body: () => t('tour.assistantStep2Body'),
      },
      {
        target: 'code',
        title: () => t('tour.assistantStep3Title'),
        body: () => t('tour.assistantStep3Body', { mod: mod() }),
      },
      {
        target: 'chat',
        title: () => t('tour.assistantStep4Title'),
        body: () => t('tour.assistantStep4Body'),
      },
    ],
  },
  {
    id: 'debugger',
    title: () => t('tour.debuggerTitle'),
    summary: () => t('tour.debuggerSummary'),
    minutes: 2,
    steps: [
      {
        target: 'open-folder',
        title: () => t('tour.debuggerStep1Title'),
        body: () => t('tour.debuggerStep1Body'),
        before: closeSettings,
      },
      {
        target: 'code',
        title: () => t('tour.debuggerStep2Title'),
        body: () => t('tour.debuggerStep2Body'),
      },
      {
        target: 'file-header',
        title: () => t('tour.debuggerStep3Title'),
        body: () => t('tour.debuggerStep3Body', { mod: mod() }),
      },
      {
        target: 'terminal',
        title: () => t('tour.debuggerStep4Title'),
        body: () => t('tour.debuggerStep4Body'),
        before: () => {
          useUiStore.getState().setBottomView('debug');
          useUiStore.getState().setTerminalOpen(true);
        },
      },
      {
        target: 'code',
        title: () => t('tour.debuggerStep5Title'),
        body: () => t('tour.debuggerStep5Body'),
      },
    ],
  },
  {
    id: 'make-it-yours',
    title: () => t('tour.makeItYoursTitle'),
    summary: () => t('tour.makeItYoursSummary'),
    minutes: 1,
    steps: [
      {
        target: 'settings',
        title: () => t('tour.makeItYoursStep1Title'),
        body: () => t('tour.makeItYoursStep1Body', { mod: mod() }),
        before: closeSettings,
      },
      {
        title: () => t('tour.makeItYoursStep2Title'),
        body: () => t('tour.makeItYoursStep2Body'),
        before: () => {
          useUiStore.getState().setSettingsSection('appearance');
          useUiStore.getState().setSettingsOpen(true);
        },
      },
      {
        title: () => t('tour.makeItYoursStep3Title'),
        body: () => t('tour.makeItYoursStep3Body'),
        before: () => {
          useUiStore.getState().setSettingsSection('keyboard');
          useUiStore.getState().setSettingsOpen(true);
        },
      },
      {
        title: () => t('tour.makeItYoursStep4Title'),
        body: () => t('tour.makeItYoursStep4Body'),
        before: () => {
          useUiStore.getState().setSettingsSection('accessibility');
          useUiStore.getState().setSettingsOpen(true);
        },
      },
      {
        title: () => t('tour.makeItYoursStep5Title'),
        body: () => t('tour.makeItYoursStep5Body'),
        before: () => {
          useUiStore.getState().setSettingsSection('layout');
          useUiStore.getState().setSettingsOpen(true);
        },
      },
    ],
  },
];

const DONE_KEY = 'aisai.tutorials.done';

function finished(): Set<string> {
  try {
    return new Set(JSON.parse(localStorage.getItem(DONE_KEY) ?? '[]') as string[]);
  } catch {
    return new Set();
  }
}

function markFinished(id: string): void {
  const done = finished();
  done.add(id);
  try {
    localStorage.setItem(DONE_KEY, JSON.stringify(Array.from(done)));
  } catch {
    /* remembered for this session only */
  }
}

/** Whether the user has not taken any tour yet, for a nudge on the welcome screen. */
export function noTourTakenYet(): boolean {
  return finished().size === 0;
}

/** Installs the tutorials: the chooser dialog and the tour overlay. Returns a cleanup. */
export function installTutorials(root: HTMLElement): () => void {
  const chooser = h('dialog', { class: 'tutorials-dialog', 'aria-label': t('tour.tutorialsAria') });
  root.append(chooser);

  const ring = h('div', { class: 'tour-ring', 'aria-hidden': 'true' });
  const card = h('div', { class: 'tour-card', role: 'dialog', 'aria-modal': 'false', 'aria-live': 'polite' });
  const layer = h('div', { class: 'tour-layer', hidden: true }, ring, card);
  root.append(layer);

  let tour: Tour | null = null;
  let index = 0;

  const openChooser = (): void => {
    const done = finished();
    const tours = TOURS();
    const close = h('button', {
      class: 'ghost tiny',
      type: 'button',
      onclick: () => chooser.close(),
    }, t('tour.close'));
    chooser.replaceChildren(
      h('div', { class: 'shortcuts-head' }, h('h2', { text: t('tour.tutorials') }), h('span', { class: 'grow' }), close),
      h('p', { class: 'muted tutorial-lede', text: t('tour.lede') }),
      h('ul', { class: 'tutorial-list' },
        ...tours.map((item) =>
          h('li', {},
            h('button', { class: 'tutorial-item', type: 'button', onclick: () => start(item) },
              h('span', {
                class: `tutorial-check${done.has(item.id) ? ' is-done' : ''}`,
                'aria-label': done.has(item.id) ? t('tour.finished') : t('tour.notTaken'),
              }, done.has(item.id) ? icon('check') : null),
              h('span', { class: 'tutorial-text' },
                h('strong', { text: item.title() }),
                h('span', { class: 'muted', text: item.summary() }),
              ),
              h('span', {
                class: 'muted tutorial-time',
                text: tn('tour.duration', item.steps.length, { minutes: item.minutes }),
              }),
            ),
          ),
        ),
      ),
    );
    if (!chooser.open) chooser.showModal();
  };

  const stop = (completed: boolean): void => {
    if (tour && completed) markFinished(tour.id);
    tour = null;
    layer.hidden = true;
    if (layer.parentElement !== root) root.append(layer);
    document.body.classList.remove('is-touring');
    window.removeEventListener('keydown', onKey, true);
    window.removeEventListener('resize', place);
  };

  /** Where the ring and the card go for the current step. */
  const place = (): void => {
    if (!tour) return;
    const step = tour.steps[index];
    const el = step.target ? document.querySelector<HTMLElement>(`[data-tour="${step.target}"]`) : null;
    const rect = el && el.offsetParent !== null ? el.getBoundingClientRect() : null;
    const visible = rect && rect.width > 0 && rect.height > 0;
    ring.hidden = !visible;
    card.classList.toggle('is-centered', !visible);
    if (!visible || !rect) {
      card.style.left = '';
      card.style.top = '';
      return;
    }
    const pad = 6;
    Object.assign(ring.style, {
      left: `${rect.left - pad}px`,
      top: `${rect.top - pad}px`,
      width: `${rect.width + pad * 2}px`,
      height: `${rect.height + pad * 2}px`,
    });
    // The card goes below the control if there is room, else above, and
    // stays inside the window.
    const cardRect = card.getBoundingClientRect();
    const below = rect.bottom + 14 + cardRect.height < window.innerHeight;
    const top = below ? rect.bottom + 14 : Math.max(12, rect.top - 14 - cardRect.height);
    const left = Math.min(Math.max(12, rect.left), window.innerWidth - cardRect.width - 12);
    card.style.left = `${left}px`;
    card.style.top = `${top}px`;
  };

  const show = (): void => {
    if (!tour) return;
    const step = tour.steps[index];
    step.before?.();
    const last = index === tour.steps.length - 1;
    const title = tour.title();
    const back = h('button', {
      class: 'ghost tiny',
      type: 'button',
      disabled: index === 0 || undefined,
      onclick: () => go(-1),
    }, t('tour.back'));
    const next = h('button', { class: 'primary tiny', type: 'button', onclick: () => (last ? stop(true) : go(1)) },
      last ? t('tour.done') : t('tour.next'));
    const leave = h('button', {
      class: 'ghost tiny icon-button',
      type: 'button',
      title: t('tour.leaveTitle'),
      'aria-label': t('tour.leaveAria'),
      onclick: () => stop(false),
    }, icon('x'));
    card.setAttribute('aria-label', t('tour.cardAria', { title, step: index + 1, total: tour.steps.length }));
    card.replaceChildren(
      h('div', { class: 'tour-head' },
        h('span', {
          class: 'tour-progress',
          text: t('tour.progress', { title, step: index + 1, total: tour.steps.length }),
        }),
        h('span', { class: 'grow' }),
        leave),
      h('h3', { class: 'tour-title', text: step.title() }),
      h('p', { class: 'tutorial-body', text: step.body() }),
      h('div', { class: 'tour-dots', 'aria-hidden': 'true' },
        ...tour.steps.map((_, i) => h('span', { class: `tour-dot${i === index ? ' is-current' : i < index ? ' is-past' : ''}` }))),
      h('div', { class: 'tour-actions' }, back, h('span', { class: 'grow' }), next),
    );
    layer.hidden = false;
    // Wait for whatever the step opened to be laid out.
    requestAnimationFrame(() => {
      // A modal dialog (Settings) makes the rest of the page inert and covers
      // it, so the tour moves inside the dialog while one is open.
      const host = document.querySelector<HTMLElement>('dialog:modal') ?? root;
      if (layer.parentElement !== host) host.append(layer);
    });
    requestAnimationFrame(() => requestAnimationFrame(() => {
      place();
      next.focus();
    }));
  };

  const go = (delta: number): void => {
    if (!tour) return;
    index = Math.max(0, Math.min(tour.steps.length - 1, index + delta));
    show();
  };

  const onKey = (event: KeyboardEvent): void => {
    if (!tour) return;
    if (event.key === 'Escape') {
      event.preventDefault();
      event.stopPropagation();
      stop(false);
    } else if (event.key === 'ArrowRight' && !(event.target as HTMLElement | null)?.closest('input, textarea')) {
      event.preventDefault();
      if (index === tour.steps.length - 1) stop(true);
      else go(1);
    } else if (event.key === 'ArrowLeft' && !(event.target as HTMLElement | null)?.closest('input, textarea')) {
      event.preventDefault();
      go(-1);
    }
  };

  const start = (item: Tour): void => {
    chooser.close();
    tour = item;
    index = 0;
    document.body.classList.add('is-touring');
    window.addEventListener('keydown', onKey, true);
    window.addEventListener('resize', place);
    show();
  };

  const onOpen = (): void => openChooser();
  window.addEventListener('aisai:tutorials', onOpen);
  return () => {
    stop(false);
    window.removeEventListener('aisai:tutorials', onOpen);
    chooser.remove();
    layer.remove();
  };
}
