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
import { icon } from '../lib/icons';
import { isMac } from '../lib/shortcuts';
import { useUiStore } from '../state/ui';

interface Step {
  /** The data-tour value of the control to point at; none for a card in the middle. */
  target?: string;
  title: string;
  body: string;
  /** Gets the screen ready for the step. */
  before?: () => void;
}

interface Tour {
  id: string;
  title: string;
  summary: string;
  minutes: number;
  steps: Step[];
}

const mod = (): string => (isMac() ? '⌘' : 'Ctrl+');
const closeSettings = (): void => useUiStore.getState().setSettingsOpen(false);

export const TOURS: Tour[] = [
  {
    id: 'first-audit',
    title: 'Audit your first package',
    summary: 'Pull a package, scan it and read what was found.',
    minutes: 2,
    steps: [
      { title: 'What AISAI does', body: 'AISAI downloads a package’s source (never running it), maps its dependencies, and looks for security problems with static rules and, if you like, a language model. This tour shows the way through one audit.', before: closeSettings },
      { target: 'pull', title: '1 · Pick a package and pull it', body: 'Type a name in the package field to its left (flask, lodash, serde…), choose the index (PyPI, npm or crates.io) and press Pull or Enter. You can also paste a GitHub link. Pulling only downloads; nothing is executed.' },
      { target: 'open-folder', title: 'Or open your own project', body: `Open a folder on this computer instead (${mod()}O, or drop it on the window). AISAI copies it and works on the copy.` },
      { target: 'graph', title: '2 · The dependency graph', body: 'Every package that came along, sized by importance and coloured by the worst finding. Hover for details; click to select.' },
      { target: 'why', title: 'Why is this package here?', body: 'Type or paste any name, a requirement line or a package link. It suggests matches and shows the chain of packages that pulled it in.' },
      { target: 'static-scan', title: '3 · Scan', body: `Static scan runs the rules (fast, ${mod()}⇧S). LLM scan asks the model to review the code as well (slower; needs a model server).` },
      { target: 'findings', title: '4 · Read the findings', body: 'Each finding has a severity, a rule, the file and line. Filter by severity, CWE or state; click one to jump to the code.' },
      { target: 'code', title: '5 · The code', body: 'Findings are marked in the margin and on the line. Select code to ask about it, explain it, or have the model change it.' },
      { title: 'That is an audit', body: 'Next, try “Triage and fix findings” or “Ask the assistant”. Help › Tutorials brings these tours back any time.' },
    ],
  },
  {
    id: 'triage',
    title: 'Triage and fix findings',
    summary: 'Confirm or suppress findings, apply fixes, export a report.',
    minutes: 2,
    steps: [
      { target: 'findings', title: 'Triage', body: 'Open a finding to mark it confirmed, suppressed (optionally until a date) or fixed. Suppressed ones hide unless you show them.', before: closeSettings },
      { target: 'findings', title: 'Propose a fix', body: '“Propose fix” asks the model for a patch. It opens in the Diff tab, where you can read it before applying anything.' },
      { target: 'code', title: 'Review in the editor', body: 'An applied fix types itself into the editor and glows briefly. It is one undo step. Save writes it to the copy of the code.' },
      { target: 'export', title: 'Export a report', body: `PDF for sharing, laid out for paper, or one HTML page in the app’s theme. Also under File › Export Report (${mod()}⇧E).` },
    ],
  },
  {
    id: 'assistant',
    title: 'Ask the assistant',
    summary: 'Chat about the code, change code inline, approve commands.',
    minutes: 2,
    steps: [
      { target: 'chat', title: 'The chat', body: 'Ask about the package in plain words: “where is user input parsed?”, “is this safe to use with untrusted YAML?”. Answers cite the files and lines they used; click a citation to open it.', before: closeSettings },
      { target: 'chat-input', title: 'Edit, branch, retry', body: 'Hover a message to edit your question, branch the conversation from an answer, or ask again. Long conversations are summarised automatically so they fit.' },
      { target: 'code', title: 'Inline chat', body: `Select code and press ${mod()}K (or click Change…) to say how it should change. The proposal comes back as a diff; nothing changes until you Accept.` },
      { target: 'chat', title: 'Tools and approvals', body: 'The assistant can open files and run read-only commands like grep. Anything else asks you first, with Allow and Deny. Web search is off until you switch it on in Settings, and needs a model that can call tools; the chat says when yours cannot.' },
    ],
  },
  {
    id: 'debugger',
    title: 'Debug a local project',
    summary: 'Breakpoints, stepping and variables for Python code you opened.',
    minutes: 2,
    steps: [
      { target: 'open-folder', title: 'Your own code only', body: 'The debugger runs code, so it is offered only for folders you open yourself, never for downloaded packages. Open a Python project to follow along.', before: closeSettings },
      { target: 'code', title: 'Set breakpoints', body: 'Click a line number (or press F9) in a .py file. A red dot marks the breakpoint; click again to remove it.' },
      { target: 'file-header', title: 'Start', body: `Press Debug in the file’s header, or F5. The first time, you are asked to confirm that the program will run on your computer. ${mod()}F10 runs to the cursor line.` },
      {
        target: 'terminal',
        title: 'The Debug panel',
        body: 'It opens at the bottom, beside the terminal: the controls (continue F5, step over F10, into F11, out ⇧F11, stop ⇧F5), the call stack, your breakpoints, the variables, and a console to evaluate expressions.',
        before: () => {
          useUiStore.getState().setBottomView('debug');
          useUiStore.getState().setTerminalOpen(true);
        },
      },
      { target: 'code', title: 'Read values in place', body: 'While paused, the line is highlighted, the values of its variables appear at its end, and hovering a name shows its value.' },
    ],
  },
  {
    id: 'make-it-yours',
    title: 'Make it yours',
    summary: 'Themes, editor colours, shortcuts, accessibility and layout.',
    minutes: 1,
    steps: [
      { target: 'settings', title: 'Settings', body: `Everything below lives in Settings (${mod()},).`, before: closeSettings },
      {
        title: 'Appearance',
        body: 'Light or dark, an accent colour, and an editor theme (One Dark, Dracula, Solarized, GitHub and more), with its font size and line spacing.',
        before: () => {
          useUiStore.getState().setSettingsSection('appearance');
          useUiStore.getState().setSettingsOpen(true);
        },
      },
      {
        title: 'Keyboard',
        body: 'Every shortcut, and a way to change it: click its keys and press new ones.',
        before: () => {
          useUiStore.getState().setSettingsSection('keyboard');
          useUiStore.getState().setSettingsOpen(true);
        },
      },
      {
        title: 'Accessibility',
        body: 'Interface size, higher contrast, colour-blind-friendly severities, strong focus rings, wider text spacing and reduced motion.',
        before: () => {
          useUiStore.getState().setSettingsSection('accessibility');
          useUiStore.getState().setSettingsOpen(true);
        },
      },
      {
        title: 'Layout',
        body: 'Reorder or hide panes, dock the chat at the side, or pick a preset. Drag the dividers, or use the arrows on the terminal and chat handles.',
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
  const chooser = h('dialog', { class: 'tutorials-dialog', 'aria-label': 'Tutorials' });
  root.append(chooser);

  const ring = h('div', { class: 'tour-ring', 'aria-hidden': 'true' });
  const card = h('div', { class: 'tour-card', role: 'dialog', 'aria-modal': 'false', 'aria-live': 'polite' });
  const layer = h('div', { class: 'tour-layer', hidden: true }, ring, card);
  root.append(layer);

  let tour: Tour | null = null;
  let index = 0;

  const openChooser = (): void => {
    const done = finished();
    const close = h('button', { class: 'ghost tiny', type: 'button', onclick: () => chooser.close() }, 'Close');
    chooser.replaceChildren(
      h('div', { class: 'shortcuts-head' }, h('h2', { text: 'Tutorials' }), h('span', { class: 'grow' }), close),
      h('p', { class: 'muted tutorial-lede', text: 'Short tours that point at the real controls. They only explain: nothing is pulled, scanned or run for you. Esc leaves a tour at any time.' }),
      h('ul', { class: 'tutorial-list' },
        ...TOURS.map((t) =>
          h('li', {},
            h('button', { class: 'tutorial-item', type: 'button', onclick: () => start(t) },
              h('span', { class: `tutorial-check${done.has(t.id) ? ' is-done' : ''}`, 'aria-label': done.has(t.id) ? 'Finished' : 'Not taken yet' }, done.has(t.id) ? icon('check') : null),
              h('span', { class: 'tutorial-text' },
                h('strong', { text: t.title }),
                h('span', { class: 'muted', text: t.summary }),
              ),
              h('span', { class: 'muted tutorial-time', text: `${t.minutes} min · ${t.steps.length} steps` }),
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
    const back = h('button', { class: 'ghost tiny', type: 'button', disabled: index === 0 || undefined, onclick: () => go(-1) }, 'Back');
    const next = h('button', { class: 'primary tiny', type: 'button', onclick: () => (last ? stop(true) : go(1)) }, last ? 'Done' : 'Next');
    const leave = h('button', { class: 'ghost tiny icon-button', type: 'button', title: 'Leave the tour (Esc)', 'aria-label': 'Leave the tour', onclick: () => stop(false) }, icon('x'));
    card.setAttribute('aria-label', `${tour.title}, step ${index + 1} of ${tour.steps.length}`);
    card.replaceChildren(
      h('div', { class: 'tour-head' }, h('span', { class: 'tour-progress', text: `${tour.title} · ${index + 1}/${tour.steps.length}` }), h('span', { class: 'grow' }), leave),
      h('h3', { class: 'tour-title', text: step.title }),
      h('p', { class: 'tutorial-body', text: step.body }),
      h('div', { class: 'tour-dots', 'aria-hidden': 'true' }, ...tour.steps.map((_, i) => h('span', { class: `tour-dot${i === index ? ' is-current' : i < index ? ' is-past' : ''}` }))),
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

  const start = (t: Tour): void => {
    chooser.close();
    tour = t;
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
