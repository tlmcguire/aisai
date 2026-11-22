/**
 * The Debug view of the bottom panel: the controls, the call stack, the
 * breakpoint list, the variables of the selected frame and a console that
 * prints the program's output and evaluates expressions.
 *
 * It only shows state (state/debug.ts) and asks AppController to act; the
 * program runs in the backend under a small bdb-based driver
 * (internal/debug). Debugging is offered for local Python projects only.
 */

import type { AppController } from '../app';
import { h } from '../lib/dom';
import { icon, type IconName } from '../lib/icons';
import { t, type MessageKey } from '../lib/i18n';
import { useAuditStore } from '../state/audit';
import { useDebugStore, type DebugState, type DebugVariable } from '../state/debug';
import { useWorkspaceStore } from '../state/workspace';

export interface DebugPanel {
  element: HTMLElement;
  /** Puts the focus in the console's input. */
  focus: () => void;
  dispose: () => void;
}

/** The Python file the debugger would start: the open one, else the last one run. */
export function debugTarget(): string | null {
  const file = useWorkspaceStore.getState().file;
  if (file && /\.py$/i.test(file.path)) return file.path;
  return useDebugStore.getState().program;
}

const baseName = (path: string): string => path.split('/').pop() ?? path;

const REASONS: Record<string, MessageKey> = {
  breakpoint: 'debug.reason.breakpoint',
  step: 'debug.reason.step',
  entry: 'debug.reason.entry',
  pause: 'debug.reason.pause',
  exception: 'debug.reason.exception',
};

/**
 * The debug panel: run a Python file of a local project with breakpoints, step
 * through it, read the stack and variables, and evaluate expressions.
 *
 * It lives in the bottom panel next to the terminal. The program only starts
 * when the person asks, and the first time they are told that it runs the
 * project's code on this computer.
 */
export function createDebugPanel(controller: AppController): DebugPanel {
  const status = h('span', { class: 'debug-status', role: 'status', 'aria-live': 'polite' });
  const toolButton = (name: IconName, label: string, keys: string, run: () => void): HTMLButtonElement => {
    const button = h('button', { class: 'ghost tiny icon-button debug-tool', type: 'button', title: `${label} (${keys})`, 'aria-label': label }, icon(name));
    button.addEventListener('click', run);
    return button;
  };
  const startButton = h('button', { class: 'primary tiny debug-start', type: 'button' }, icon('bug'), h('span', { class: 'btn-label', text: t('debug.debug') }));
  const continueButton = toolButton('play', t('debug.continue'), 'F5', () => controller.debugCommand('continue'));
  const pauseButton = toolButton('pause', t('debug.pause'), 'F6', () => controller.debugCommand('pause'));
  const overButton = toolButton('step-over', t('debug.stepOver'), 'F10', () => controller.debugCommand('next'));
  const intoButton = toolButton('step-into', t('debug.stepInto'), 'F11', () => controller.debugCommand('step'));
  const outButton = toolButton('step-out', t('debug.stepOut'), 'Shift+F11', () => controller.debugCommand('out'));
  const cursorButton = toolButton('step-into', t('debug.runToCursor'), 'Mod+F10', () => window.dispatchEvent(new Event('aisai:run-to-cursor')));
  cursorButton.classList.add('debug-run-to-cursor');
  const restartButton = toolButton('restart', t('debug.restart'), 'Mod+Shift+F5', () => void controller.restartDebug());
  const stopButton = toolButton('stop', t('debug.stop'), 'Shift+F5', () => void controller.stopDebug());
  const clearBreakpoints = h('button', { class: 'ghost tiny', type: 'button', title: t('debug.removeAllBreakpoints') }, t('debug.clearBreakpoints'));
  clearBreakpoints.addEventListener('click', () => useDebugStore.getState().clearBreakpoints());

  const bar = h(
    'div',
    { class: 'debug-bar' },
    startButton,
    h('span', { class: 'debug-tools', role: 'toolbar', 'aria-label': t('debug.controls') }, continueButton, pauseButton, overButton, intoButton, outButton, cursorButton, restartButton, stopButton),
    status,
    h('span', { class: 'grow' }),
    clearBreakpoints,
  );

  const notice = h('div', { class: 'debug-notice', hidden: true });
  const stackList = h('ol', { class: 'debug-stack' });
  const breakpointList = h('ul', { class: 'debug-breakpoints' });
  const variables = h('div', { class: 'debug-vars' });
  const output = h('div', { class: 'debug-output', role: 'log', 'aria-label': t('debug.programOutput') });
  const input = h('input', {
    class: 'debug-eval',
    type: 'text',
    placeholder: t('debug.evalPlaceholder'),
    'aria-label': t('debug.evalLabel'),
    spellcheck: 'false',
  });
  const history: string[] = [];
  let historyAt = 0;
  input.addEventListener('keydown', (event) => {
    if (event.key === 'Enter') {
      event.preventDefault();
      const text = input.value.trim();
      if (!text) return;
      history.push(text);
      historyAt = history.length;
      input.value = '';
      controller.debugEvaluate(text);
    } else if (event.key === 'ArrowUp' && history.length) {
      event.preventDefault();
      historyAt = Math.max(0, historyAt - 1);
      input.value = history[historyAt] ?? '';
    } else if (event.key === 'ArrowDown' && history.length) {
      event.preventDefault();
      historyAt = Math.min(history.length, historyAt + 1);
      input.value = history[historyAt] ?? '';
    }
  });
  const clearConsole = h('button', { class: 'ghost tiny', type: 'button', title: t('debug.clearConsole') }, t('debug.clear'));
  clearConsole.addEventListener('click', () => useDebugStore.getState().clearConsole());

  const body = h(
    'div',
    { class: 'debug-body' },
    h('section', { class: 'debug-section' }, h('h3', { text: t('debug.callStack') }), stackList, h('h3', { class: 'debug-subhead', text: t('debug.breakpoints') }), breakpointList),
    h('section', { class: 'debug-section' }, h('h3', { text: t('debug.variables') }), variables),
    h(
      'section',
      { class: 'debug-section debug-console' },
      h('div', { class: 'debug-section-head' }, h('h3', { text: t('debug.console') }), h('span', { class: 'grow' }), clearConsole),
      output,
      h('div', { class: 'debug-eval-row' }, h('span', { class: 'debug-prompt', text: '›', 'aria-hidden': 'true' }), input),
    ),
  );

  const element = h('div', { class: 'debug-panel' }, bar, notice, body);

  const start = (): void => {
    const program = debugTarget();
    if (!program) return;
    void controller.startDebug(program);
  };
  const requestStart = (): void => {
    if (!controller.canDebug() || !debugTarget()) {
      render();
      return;
    }
    if (!useDebugStore.getState().consented) {
      showConsent();
      return;
    }
    start();
  };
  startButton.addEventListener('click', requestStart);
  // F5 from anywhere: start, or continue a paused program.
  const onShortcut = (): void => {
    const state = useDebugStore.getState();
    if (state.status === 'paused') controller.debugCommand('continue');
    else if (state.status !== 'running' && state.status !== 'starting') requestStart();
  };
  window.addEventListener('aisai:debug-start', onShortcut);

  /** The one-time explanation before anything runs. */
  const showConsent = (): void => {
    const program = debugTarget();
    if (!program) return;
    const run = h('button', { class: 'primary tiny', type: 'button' }, t('debug.runFile', { file: baseName(program) }));
    const cancel = h('button', { class: 'tiny', type: 'button' }, t('debug.cancel'));
    run.addEventListener('click', () => {
      useDebugStore.getState().consent();
      start();
    });
    cancel.addEventListener('click', () => {
      notice.className = 'debug-notice';
      render();
    });
    notice.hidden = false;
    notice.className = 'debug-notice is-consent';
    notice.replaceChildren(
      h('p', {}, t('debug.consentIntro', { program })),
      h('div', { class: 'debug-notice-actions' }, cancel, run),
    );
    run.focus();
  };

  const variableRows = (list: DebugVariable[]): HTMLElement =>
    list.length
      ? h('table', { class: 'debug-var-table' },
          h('tbody', {}, ...list.map((v) =>
            h('tr', {},
              h('th', { scope: 'row', text: v.name, title: v.type }),
              h('td', { class: 'debug-var-value', text: v.value, title: `${v.type}: ${v.value}` }),
            ),
          )),
        )
      : h('p', { class: 'muted debug-empty', text: t('debug.noVariables') });

  let renderedConsole: DebugState['console'] | null = null;

  const renderConsole = (state: DebugState): void => {
    if (renderedConsole === state.console) return;
    renderedConsole = state.console;
    const atBottom = output.scrollHeight - output.scrollTop - output.clientHeight < 24;
    output.replaceChildren(
      ...state.console.map((line) =>
        h('pre', { class: `debug-line is-${line.kind}`, text: line.kind === 'input' ? `› ${line.text}` : line.text }),
      ),
    );
    if (atBottom) output.scrollTop = output.scrollHeight;
  };

  const render = (): void => {
    const state = useDebugStore.getState();
    const audit = useAuditStore.getState().current;
    const local = controller.canDebug();
    const program = debugTarget();
    const active = state.status === 'running' || state.status === 'paused' || state.status === 'starting';

    // While a program runs, the tools carry restart and stop.
    startButton.hidden = active;
    startButton.disabled = !local || !program || state.status === 'starting';
    const label = startButton.querySelector('.btn-label');
    if (label) label.textContent = program ? t(active ? 'debug.restartLabel' : 'debug.startLabel', { file: baseName(program) }) : t('debug.debug');
    startButton.title = !local
      ? t('debug.localOnlyTitle')
      : program
        ? t('debug.runUnderTitle', { program })
        : t('debug.openPythonTitle');
    continueButton.disabled = state.status !== 'paused';
    pauseButton.disabled = state.status !== 'running';
    overButton.disabled = state.status !== 'paused';
    intoButton.disabled = state.status !== 'paused';
    outButton.disabled = state.status !== 'paused';
    restartButton.disabled = !active || !state.program;
    stopButton.disabled = !active;
    clearBreakpoints.disabled = Object.keys(state.breakpoints).length === 0;

    const frame = state.frames[state.frame];
    status.className = `debug-status is-${state.status}`;
    status.textContent =
      state.status === 'paused' && frame
        ? t('debug.statusPaused', {
            reason: t(REASONS[state.reason] ?? 'debug.reason.pause'),
            file: baseName(frame.file),
            line: frame.line,
          })
        : state.status === 'running'
          ? t('debug.statusRunning', { program: state.program ? baseName(state.program) : '' })
          : state.status === 'starting'
            ? t('debug.statusStarting')
            : state.status === 'exited'
              ? t('debug.statusExited', { code: state.exitCode ?? 0 })
              : '';

    // The notice says why nothing can run, or what went wrong.
    if (!notice.classList.contains('is-consent') || active) {
      let text = '';
      if (!audit) text = t('debug.noticeNoProject');
      else if (audit.source !== 'local') text = t('debug.noticeDownloaded', { package: audit.package });
      else if (audit.sourcesDeleted) text = t('debug.noticeCopyDeleted');
      else if (state.error) text = t('debug.noticeStartFailed', { error: state.error });
      else if (!program) text = t('debug.noticeHowToStart');
      notice.className = `debug-notice${state.error ? ' is-error' : ''}`;
      if (state.status === 'exited' && !text) notice.hidden = true;
      notice.hidden = !text;
      notice.textContent = text;
    }
    if (state.status === 'paused' && state.reason === 'exception' && state.description) {
      notice.hidden = false;
      notice.className = 'debug-notice is-error';
      notice.textContent = state.description;
    }

    stackList.replaceChildren(
      ...(state.frames.length
        ? state.frames.map((f, index) =>
            h('li', {},
              h('button', {
                class: `debug-frame${index === state.frame ? ' is-current' : ''}${f.external ? ' is-external' : ''}`,
                type: 'button',
                title: f.external ? t('debug.frameOutside', { file: f.file, line: f.line }) : `${f.file}:${f.line}`,
                'aria-current': index === state.frame ? 'true' : undefined,
                onclick: () => controller.debugSelectFrame(index),
              },
                h('span', { class: 'debug-frame-name', text: f.name === '<module>' ? 'module' : f.name }),
                h('span', { class: 'debug-frame-file', text: `${baseName(f.file)}:${f.line}` }),
              ),
            ),
          )
        : [h('li', { class: 'muted debug-empty', text: state.status === 'running' ? t('debug.stackRunning') : t('debug.notPaused') })]),
    );

    // Every breakpoint, to jump to or remove; the hint says how to add one.
    const entries = Object.entries(state.breakpoints).flatMap(([file, lines]) => lines.map((line) => ({ file, line })));
    breakpointList.replaceChildren(
      ...(entries.length
        ? entries.map(({ file, line }) =>
            h('li', { class: 'debug-breakpoint' },
              h('span', { class: 'debug-bp-dot', 'aria-hidden': 'true' }),
              h('button', { class: 'debug-bp-go', type: 'button', title: t('debug.goToBreakpoint', { file, line }), onclick: () => void controller.openFile(file, line) }, `${baseName(file)}:${line}`),
              h('button', {
                class: 'ghost tiny icon-button',
                type: 'button',
                title: t('debug.removeBreakpoint'),
                'aria-label': t('debug.removeBreakpointAt', { file: baseName(file), line }),
                onclick: () => useDebugStore.getState().toggleBreakpoint(file, line),
              }, icon('x')),
            ),
          )
        : [h('li', { class: 'muted debug-empty', text: t('debug.noBreakpointsYet') })]),
    );
    cursorButton.disabled = !local || !program || state.status === 'running' || state.status === 'starting';
    cursorButton.title = state.status === 'paused' ? t('debug.continueToCursor') : t('debug.runUntilCursor');

    variables.replaceChildren(
      ...(state.status === 'paused'
        ? [
            h('h4', { text: t('debug.locals') }),
            variableRows(state.locals),
            ...(state.globals.length
              ? [h('details', { class: 'debug-globals' }, h('summary', { text: t('debug.globals', { n: state.globals.length }) }), variableRows(state.globals))]
              : []),
          ]
        : [h('p', { class: 'muted debug-empty', text: t('debug.variablesHint') })]),
    );

    renderConsole(state);
  };

  const offDebug = useDebugStore.subscribe(render);
  const offAudit = useAuditStore.subscribe((state, previous) => {
    if (state.current !== previous.current) render();
  });
  const offFile = useWorkspaceStore.subscribe((state, previous) => {
    if (state.file?.path !== previous.file?.path) render();
  });
  render();

  return {
    element,
    focus: () => input.focus(),
    dispose: () => {
      window.removeEventListener('aisai:debug-start', onShortcut);
      offDebug();
      offAudit();
      offFile();
    },
  };
}
