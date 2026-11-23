/**
 * The chat panel: conversations about the open audit.
 *
 * Each answer streams in token by token (chat.token events) and is drawn
 * from markdown, with citations of files and lines turned into buttons and
 * cited web pages into link chips. While it streams, only the text node
 * that grew is updated, so the rest of the message does not flicker. Tool
 * steps (files read, commands run) show under the answer, with Allow and
 * Deny when a command needs approval. Messages can be edited, branched into
 * a new thread or asked again; failures show a plain reason and Try again.
 * State lives in state/chat.ts; the backend calls go through AppController.
 */

import type { AppController } from '../app';
import type { ChatMessage, ChatThread, ToolStep } from '../state/chat';
import { h } from '../lib/dom';
import { t, tn } from '../lib/i18n';
import { icon, type IconName } from '../lib/icons';
import { useChatStore } from '../state/chat';
import { DEFAULT_LAYOUT, useUiStore } from '../state/ui';
import { providerLabel } from './settingsDialog';

const CITATION = /([\w./-]+\.[A-Za-z0-9]+):(\d+)/g;
/** A web address the model cited, bare or in angle brackets. */
const WEB_LINK = /<?(https?:\/\/[^\s<>()]+[^\s<>().,;:!?'"])>?/g;

/** Text with the web addresses in it turned into links that open in the browser. */
function linkify(text: string): Node[] {
  const out: Node[] = [];
  let last = 0;
  WEB_LINK.lastIndex = 0;
  for (let m = WEB_LINK.exec(text); m; m = WEB_LINK.exec(text)) {
    if (m.index > last) out.push(document.createTextNode(text.slice(last, m.index)));
    const url = m[1];
    let host = url;
    try {
      host = new URL(url).host;
    } catch {
      /* shown as written */
    }
    out.push(
      h('button', { class: 'citation web-citation', type: 'button', title: t('chat.openLinkInBrowser', { url }), onclick: () => void window.aisai.openExternal(url) }, icon('globe'), host),
    );
    last = m.index + m[0].length;
  }
  if (last < text.length) out.push(document.createTextNode(text.slice(last)));
  return out;
}

/** What a tool call does, in words: "Reading src/auth.py". */
export function stepLabel(step: ToolStep): string {
  const a = step.args ?? {};
  const s = (k: string): string => (typeof a[k] === 'string' ? (a[k] as string) : '');
  switch (step.name) {
    case 'get_file':
      return t('chat.stepReadFile', { path: s('path') });
    case 'get_ast':
      return t('chat.stepReadAst', { path: s('path') });
    case 'search_symbols':
      return t('chat.stepSearchSymbols', { query: s('query') });
    case 'list_findings':
      return t('chat.stepListFindings');
    case 'get_finding':
      return t('chat.stepOpenFinding', { id: s('id') });
    case 'run_command':
      return t('chat.stepRunCommand', { command: s('command') });
    case 'web_search':
      return t('chat.stepWebSearch', { query: s('query') });
    case 'fetch_url':
      return t('chat.stepFetchUrl', { url: s('url') });
    default:
      return step.name;
  }
}

/** The same words in the present tense, for a tool step that is still running. */
function stepRunningLabel(step: ToolStep): string {
  const a = step.args ?? {};
  const s = (k: string): string => (typeof a[k] === 'string' ? (a[k] as string) : '');
  switch (step.name) {
    case 'get_file':
      return t('chat.runningReadFile', { path: s('path') });
    case 'get_ast':
      return t('chat.runningReadAst', { path: s('path') });
    case 'search_symbols':
      return t('chat.runningSearchSymbols', { query: s('query') });
    case 'list_findings':
      return t('chat.runningListFindings');
    case 'get_finding':
      return t('chat.runningOpenFinding', { id: s('id') });
    case 'run_command':
      return t('chat.runningRunCommand', { command: s('command') });
    case 'web_search':
      return t('chat.runningWebSearch', { query: s('query') });
    case 'fetch_url':
      return t('chat.runningFetchUrl', { url: s('url') });
    default:
      return t('chat.runningTool', { tool: step.name });
  }
}

/** What the status line says while the model has not written anything yet. */
function idleVerbs(): string[] {
  return [
    t('chat.statusThinking'),
    t('chat.idleReadingCode'),
    t('chat.idleSearchingIndex'),
    t('chat.idleWorkingItOut'),
  ];
}

/** An error from the model server, in words a person can act on. */
function friendlyChatError(error: string): string {
  if (/context|too long|maximum.*tokens|num_ctx/i.test(error)) return t('chat.errorContextTooLong');
  if (/connection refused|ECONNREFUSED|no such host|dial tcp/i.test(error)) return t('chat.errorServerUnreachable');
  if (/401|403|api key|unauthori[sz]ed/i.test(error)) return t('chat.errorApiKeyRefused');
  if (/429|rate limit/i.test(error)) return t('chat.errorRateLimited');
  if (/timeout|deadline exceeded/i.test(error)) return t('chat.errorTimeout');
  if (/not found|unknown model|pull/i.test(error)) return t('chat.errorModelNotInstalled', { error });
  return error;
}

/** Whether the new message differs from the old only in its answer text. */
function onlyTextGrew(before: ChatMessage, after: ChatMessage): boolean {
  return (
    Boolean(before.streaming) &&
    Boolean(after.streaming) &&
    before.thinking === after.thinking &&
    before.steps === after.steps &&
    before.chunksUsed === after.chunksUsed &&
    before.thoughtFor === after.thoughtFor &&
    Boolean(before.content) === Boolean(after.content) &&
    after.content.startsWith(before.content)
  );
}

function renderContent(controller: AppController, message: ChatMessage): HTMLElement {
  const content = h('div', { class: 'content' });
  const text = message.content;
  let lastIndex = 0;
  CITATION.lastIndex = 0;
  let match = CITATION.exec(text);

  while (match) {
    if (match.index > lastIndex) {
      content.append(...linkify(text.slice(lastIndex, match.index)));
    }
    const file = match[1];
    const line = Number(match[2]);
    content.appendChild(
      h(
        'button',
        {
          class: 'citation',
          type: 'button',
          title: t('chat.openFileAtLine', { file, line }),
          onclick: () => {
            void controller.openFile(file, line);
          },
        },
        `${file}:${line}`,
      ),
    );
    lastIndex = match.index + match[0].length;
    match = CITATION.exec(text);
  }
  if (lastIndex < text.length) {
    content.append(...linkify(text.slice(lastIndex)));
  }
  return content;
}

/** The text of the shimmering status line, for a turn that is still going. */
function statusText(message: ChatMessage): string {
  if (message.compacting) return t('chat.statusCompacting');
  if (message.steps?.some((step) => step.status === 'waiting')) return t('chat.statusWaitingApproval');
  const running = message.steps?.find((step) => step.status === 'running');
  if (running) return stepRunningLabel(running);
  if (message.content) return t('chat.statusWriting');
  if (message.thinking) return t('chat.statusThinking');
  const seconds = message.startedAt ? Math.floor((Date.now() - message.startedAt) / 1000) : 0;
  const verbs = idleVerbs();
  return verbs[Math.floor(seconds / 4) % verbs.length];
}

function relativeTime(then: number): string {
  const minutes = Math.round((Date.now() - then) / 60000);
  if (minutes < 1) return t('chat.justNow');
  if (minutes < 60) return tn('chat.minutesAgo', minutes);
  const hours = Math.round(minutes / 60);
  if (hours < 24) return tn('chat.hoursAgo', hours);
  return tn('chat.daysAgo', Math.round(hours / 24));
}

export interface ChatPanel {
  element: HTMLElement;
  /** The bar at the top of the panel, where the grab handle stands. */
  header: HTMLElement;
  dispose: () => void;
}

export function createChatPanel(controller: AppController, container: HTMLElement): ChatPanel {
  const toggle = h('button', { class: 'ghost tiny icon-button', type: 'button' }, icon('chevron-down'));
  toggle.dataset.role = 'chat-toggle';
  const growButton = h('button', { class: 'ghost tiny icon-button', type: 'button' }, icon('arrow-up'));
  const chunksUsed = h('span', { class: 'chunks-used', text: '' });
  const modelChip = h('span', { class: 'badge neutral chat-model', text: '' });
  // Shown when web search is on but the model cannot call tools to use it.
  const webBadge = h('span', { class: 'badge web-badge', hidden: true, role: 'status' }, icon('globe'), h('span', { class: 'web-badge-text' }));
  webBadge.addEventListener('click', () => {
    useUiStore.getState().setSettingsSection('server');
    useUiStore.getState().setSettingsOpen(true);
  });

  /* ---------------- threads ---------------- */
  const threadButton = h('button', { class: 'tiny thread-button', type: 'button', 'aria-haspopup': 'true', 'aria-expanded': 'false' });
  const newButton = h(
    'button',
    { class: 'ghost tiny icon-button', type: 'button', title: t('chat.newChat'), 'aria-label': t('chat.newChat') },
    icon('plus'),
  );
  const threadMenu = h('div', { class: 'thread-menu', role: 'menu', hidden: true });
  const threadWrap = h('div', { class: 'thread-wrap' }, threadButton, threadMenu);
  let showArchived = false;
  let renamingId: string | null = null;
  let armedDelete: string | null = null;
  let armedTimer: ReturnType<typeof setTimeout> | null = null;

  const header = h(
    'div',
    { class: 'chat-header' },
    h('span', { text: t('chat.title') }),
    threadWrap,
    newButton,
    modelChip,
    webBadge,
    growButton,
    toggle,
    h('span', { class: 'grow' }),
    chunksUsed,
  );

  const messages = h('div', { class: 'chat-messages' });
  const selectionChip = h('div', { class: 'selection-chip', hidden: true });
  const input = h('input', {
    type: 'text',
    placeholder: t('chat.inputPlaceholder'),
    'aria-label': t('chat.inputLabel'),
  });
  const askButton = h('button', { class: 'primary', type: 'button' }, h('span', { class: 'btn-label', text: t('chat.ask') }));
  const footer = h('div', { class: 'chat-footer' }, selectionChip, h('div', { class: 'chat-input-row' }, input, askButton));

  container.dataset.tour = 'chat';
  footer.dataset.tour = 'chat-input';
  container.append(header, messages, footer);

  const setOpen = (open: boolean): void => {
    useUiStore.getState().setChatOpen(open);
    container.classList.toggle('collapsed', !open);
    toggle.replaceChildren(icon(open ? 'chevron-down' : 'chevron-up'));
    toggle.setAttribute('aria-expanded', String(open));
    toggle.setAttribute('aria-label', open ? t('chat.collapsePanel') : t('chat.expandPanel'));
    toggle.title = open ? t('chat.collapsePanel') : t('chat.expandPanel');
    footer.hidden = !open;
    messages.hidden = !open;
  };

  // The up/down arrow makes the panel larger or brings it back to its usual size.
  const syncGrow = (): void => {
    const ui = useUiStore.getState();
    const right = ui.chatDock === 'right';
    const big = right ? ui.chatWidth >= 560 : ui.chatHeight >= 440;
    growButton.replaceChildren(icon(big ? 'arrow-down' : 'arrow-up'));
    const label = big ? t('chat.shrinkPanel') : t('chat.enlargePanel');
    growButton.title = label;
    growButton.setAttribute('aria-label', label);
  };
  growButton.addEventListener('click', () => {
    const ui = useUiStore.getState();
    if (!ui.chatOpen) setOpen(true);
    if (ui.chatDock === 'right') ui.setChatWidth(ui.chatWidth >= 560 ? DEFAULT_LAYOUT.chatWidth : 600);
    else ui.setChatHeight(ui.chatHeight >= 440 ? DEFAULT_LAYOUT.chatHeight : 480);
  });
  useUiStore.subscribe(syncGrow);
  syncGrow();

  toggle.addEventListener('click', () => {
    setOpen(!useUiStore.getState().chatOpen);
  });

  const submit = (): void => {
    const state = useChatStore.getState();
    if (state.busy) return;
    const question = input.value.trim();
    if (!question || !state.model.available) return;
    input.value = '';
    void controller.ask(question);
  };

  askButton.addEventListener('click', () => {
    if (useChatStore.getState().busy) useChatStore.getState().stop();
    else submit();
  });
  input.addEventListener('keydown', (event) => {
    if (event.key === 'Enter') {
      event.preventDefault();
      submit();
    }
  });
  newButton.addEventListener('click', () => {
    useChatStore.getState().newThread();
    input.focus();
  });

  /* ---------------- thread menu ---------------- */
  const closeMenu = (): void => {
    threadMenu.hidden = true;
    threadButton.setAttribute('aria-expanded', 'false');
    renamingId = null;
    armedDelete = null;
  };
  const openMenu = (): void => {
    threadMenu.hidden = false;
    threadButton.setAttribute('aria-expanded', 'true');
    renderMenu();
  };
  threadButton.addEventListener('click', () => (threadMenu.hidden ? openMenu() : closeMenu()));
  const onDocumentClick = (event: MouseEvent): void => {
    if (!threadMenu.hidden && !threadWrap.contains(event.target as Node)) closeMenu();
  };
  document.addEventListener('click', onDocumentClick);
  threadMenu.addEventListener('keydown', (event) => {
    if (event.key === 'Escape') {
      closeMenu();
      threadButton.focus();
    }
  });

  const rowButton = (name: IconName, label: string, onClick: () => void, armed = false): HTMLElement =>
    h(
      'button',
      {
        class: `ghost tiny icon-button${armed ? ' is-armed' : ''}`,
        type: 'button',
        title: label,
        'aria-label': label,
        onclick: (event: Event) => {
          event.stopPropagation();
          onClick();
        },
      },
      armed ? t('chat.confirmDelete') : icon(name),
    );

  const renderMenu = (): void => {
    const state = useChatStore.getState();
    const visible = state.threads.filter((thread) => showArchived || !thread.archived);
    const archivedCount = state.threads.filter((thread) => thread.archived).length;
    const rows = visible.map((thread: ChatThread) => {
      const title =
        renamingId === thread.id
          ? (() => {
              const field = h('input', { class: 'thread-rename', type: 'text', value: thread.title, 'aria-label': t('chat.threadNameLabel') });
              const finish = (save: boolean): void => {
                if (save) useChatStore.getState().renameThread(thread.id, field.value);
                renamingId = null;
                renderMenu();
              };
              field.addEventListener('keydown', (event) => {
                event.stopPropagation();
                if (event.key === 'Enter') finish(true);
                if (event.key === 'Escape') finish(false);
              });
              field.addEventListener('blur', () => finish(true));
              queueMicrotask(() => {
                field.focus();
                field.select();
              });
              return field;
            })()
          : h(
              'button',
              {
                class: 'thread-title',
                type: 'button',
                role: 'menuitem',
                onclick: () => {
                  useChatStore.getState().selectThread(thread.id);
                  closeMenu();
                },
              },
              h('span', { class: 'thread-name', text: thread.title }),
              h('span', { class: 'thread-time muted', text: relativeTime(thread.updatedAt) }),
            );
      return h(
        'div',
        { class: `thread-row${thread.id === state.activeId ? ' is-active' : ''}${thread.archived ? ' is-archived' : ''}` },
        title,
        rowButton('pencil', t('chat.rename'), () => {
          renamingId = thread.id;
          renderMenu();
        }),
        rowButton(thread.archived ? 'restore' : 'archive', thread.archived ? t('chat.restore') : t('chat.archive'), () => {
          useChatStore.getState().archiveThread(thread.id, !thread.archived);
          renderMenu();
        }),
        rowButton(
          'trash',
          t('chat.delete'),
          () => {
            // Deleting a conversation cannot be undone, so it asks twice.
            if (armedDelete !== thread.id) {
              armedDelete = thread.id;
              if (armedTimer) clearTimeout(armedTimer);
              armedTimer = setTimeout(() => {
                armedDelete = null;
                if (!threadMenu.hidden) renderMenu();
              }, 4000);
              renderMenu();
              return;
            }
            armedDelete = null;
            useChatStore.getState().deleteThread(thread.id);
            renderMenu();
          },
          armedDelete === thread.id,
        ),
      );
    });
    const archivedToggle =
      archivedCount > 0
        ? h(
            'button',
            {
              class: 'ghost tiny thread-archived-toggle',
              type: 'button',
              onclick: () => {
                showArchived = !showArchived;
                renderMenu();
              },
            },
            showArchived ? t('chat.hideArchived') : t('chat.showArchived', { count: archivedCount }),
          )
        : null;
    threadMenu.replaceChildren(
      ...(rows.length ? rows : [h('p', { class: 'thread-empty muted', text: t('chat.noChats') })]),
      ...(archivedToggle ? [archivedToggle] : []),
    );
  };

  /* ---------------- messages ---------------- */
  // Open/closed state of the thinking and tool blocks survives the rebuilds
  // that every streamed token causes.
  const openBlocks = new Set<string>();
  const block = (key: string, summary: Node[], body: Node, className: string): HTMLElement => {
    const details = h('details', { class: className }, h('summary', {}, ...summary), body);
    details.open = openBlocks.has(key);
    details.addEventListener('toggle', () => {
      if (details.open) openBlocks.add(key);
      else openBlocks.delete(key);
    });
    return details;
  };

  const stepIcon = (step: ToolStep): Node =>
    step.status === 'running'
      ? h('span', { class: 'spinner', 'aria-label': t('chat.stepRunning') })
      : step.status === 'waiting'
        ? h('span', { class: 'approval-dot', 'aria-label': t('chat.stepWaitingApproval') })
        : icon(step.status === 'error' ? 'x' : 'check');

  /** A tool call that waits for the user: what it will do, and Allow / Deny. */
  const buildApproval = (step: ToolStep): HTMLElement => {
    const allow = h('button', { class: 'primary tiny', type: 'button' }, t('chat.allow'));
    const deny = h('button', { class: 'tiny', type: 'button' }, t('chat.deny'));
    allow.addEventListener('click', () => void controller.answerApproval(step.id, true));
    deny.addEventListener('click', () => void controller.answerApproval(step.id, false));
    return h(
      'div',
      { class: 'chat-approval', role: 'group', 'aria-label': t('chat.approvalNeeded') },
      h('div', { class: 'approval-title' }, icon(step.name === 'web_search' || step.name === 'fetch_url' ? 'globe' : 'terminal'), h('strong', { text: t('chat.allowThis') })),
      h('p', { class: 'approval-summary', text: step.summary ?? stepLabel(step) }),
      step.name === 'run_command' && typeof step.args.command === 'string'
        ? h('pre', { class: 'approval-command', text: step.args.command })
        : null,
      h('div', { class: 'approval-actions' }, allow, deny, h('span', { class: 'muted approval-note', text: t('chat.approvalTimeoutNote') })),
    );
  };

  const buildSteps = (message: ChatMessage): HTMLElement[] =>
    (message.steps ?? []).map((step) =>
      step.status === 'waiting' && message.streaming
        ? buildApproval(step)
        : block(
        `${message.id}:${step.id}`,
        [
          stepIcon(step),
          h('span', { class: 'tool-name', text: stepLabel(step) }),
          ...(step.approval === 'expired'
            ? [h('span', { class: 'badge is-bad', text: t('chat.approvalExpiredBadge'), title: t('chat.approvalExpiredTitle') }), h('span', { class: 'muted', text: t('chat.approvalExpiredText') })]
            : step.approval === 'denied'
            ? [h('span', { class: 'badge is-bad', text: t('chat.approvalDenied') })]
            : step.approval === 'approved'
              ? [h('span', { class: 'badge neutral', text: t('chat.approvalAllowed') })]
              : []),
        ],
        h(
          'div',
          { class: 'tool-body' },
          Object.keys(step.args).length ? h('pre', { text: JSON.stringify(step.args, null, 2) }) : null,
          step.error ? h('pre', { class: 'tool-error', text: step.error }) : null,
          step.output ? h('pre', { text: step.output }) : null,
        ),
        `chat-tool is-${step.status}`,
      ),
    );

  const buildThinking = (message: ChatMessage): HTMLElement | null => {
    if (!message.thinking) return null;
    const live = Boolean(message.streaming) && !message.content;
    return block(
      `${message.id}:thinking`,
      [
        live ? h('span', { class: 'shimmer', text: t('chat.statusThinking') }) : h('span', { class: 'muted', text: message.thoughtFor ? t('chat.thoughtFor', { seconds: message.thoughtFor }) : t('chat.thoughts') }),
      ],
      h('div', { class: 'thinking-body', text: message.thinking }),
      'chat-thinking',
    );
  };

  const buildMessage = (message: ChatMessage): HTMLElement => {
    const waiting = Boolean(message.streaming);
    const status = waiting
      ? h(
          'div',
          { class: 'chat-status', role: 'status' },
          h('span', { class: 'shimmer status-text', text: statusText(message) }),
          h('span', { class: 'status-elapsed muted' }),
        )
      : null;
    if (editingId === message.id) return buildEditor(message);
    const busy = useChatStore.getState().busy;
    const actions: HTMLElement[] = [];
    if (!waiting && !busy) {
      if (message.role === 'user') {
        actions.push(actionButton('pencil', t('chat.editAndAskAgain'), () => {
          editingId = message.id;
          render(true);
        }));
      } else {
        actions.push(actionButton('branch', t('chat.branchHint'), () => {
          useChatStore.getState().forkFrom(message.id);
        }));
        actions.push(actionButton('restore', t('chat.askAgain'), () => void controller.retry(message.id)));
      }
    }
    return h(
      'div',
      { class: `chat-message ${message.role}${waiting ? ' is-pending' : ''}${message.error ? ' is-failed' : ''}`, 'data-id': message.id, 'data-started': String(message.startedAt ?? '') },
      h('div', { class: 'role' }, h('span', { text: message.role === 'user' ? t('chat.roleUser') : t('chat.roleAssistant') }), actions.length ? h('span', { class: 'message-actions' }, ...actions) : null),
      message.notice ? h('div', { class: 'chat-notice', role: 'note' }, icon('globe'), h('span', { text: message.notice })) : null,
      message.compacted || message.compacting
        ? h('div', { class: 'chat-compacted muted' }, message.compacting ? h('span', { class: 'spinner', 'aria-hidden': 'true' }) : icon('archive'), message.compacting ? t('chat.compactingConversation') : message.compacted ?? '')
        : null,
      message.role === 'assistant' ? buildThinking(message) : null,
      ...(message.role === 'assistant' ? buildSteps(message) : []),
      message.content ? renderContent(controller, message) : null,
      message.error
        ? h(
            'div',
            { class: 'chat-error', role: 'alert' },
            h('strong', { text: t('chat.errorTitle') }),
            h('span', {}, ' ', t('chat.errorReason', { reason: friendlyChatError(message.error) })),
            h('button', { class: 'tiny', type: 'button', onclick: () => void controller.retry(message.id) }, t('chat.tryAgain')),
          )
        : null,
      status,
      message.chunksUsed && message.chunksUsed.length
        ? h('div', { class: 'chunks-used', text: t('chat.chunksUsed', { chunks: message.chunksUsed.join(', ') }) })
        : null,
    );
  };

  /* ---------------- editing a question ---------------- */
  let editingId: string | null = null;

  const actionButton = (name: IconName, label: string, onClick: () => void): HTMLElement =>
    h('button', { class: 'ghost tiny icon-button', type: 'button', title: label, 'aria-label': label, onclick: onClick }, icon(name));

  const buildEditor = (message: ChatMessage): HTMLElement => {
    const field = h('textarea', { class: 'chat-edit', rows: '3', 'aria-label': t('chat.editQuestionLabel') }) as HTMLTextAreaElement;
    field.value = message.content.replace(/\n\[[^\]]+:\d+-\d+\]$/, '');
    const send = h('button', { class: 'primary tiny', type: 'button' }, t('chat.askAgain'));
    const cancel = h('button', { class: 'tiny', type: 'button' }, t('chat.cancel'));
    const done = (): void => {
      editingId = null;
      render(true);
    };
    send.addEventListener('click', () => {
      const text = field.value.trim();
      editingId = null;
      if (text) void controller.editAndResend(message.id, text);
      else render(true);
    });
    cancel.addEventListener('click', done);
    field.addEventListener('keydown', (event) => {
      if (event.key === 'Enter' && !event.shiftKey) {
        event.preventDefault();
        send.click();
      } else if (event.key === 'Escape') done();
    });
    requestAnimationFrame(() => field.focus());
    return h(
      'div',
      { class: 'chat-message user is-editing', 'data-id': message.id },
      h('div', { class: 'role', text: t('chat.editRoleHint') }),
      field,
      h('div', { class: 'approval-actions' }, send, cancel),
    );
  };

  const buildEmpty = (): HTMLElement =>
    h(
      'div',
      { class: 'empty-state' },
      h('h3', { text: t('chat.emptyTitle') }),
      h('p', { text: t('chat.emptyDescription') }),
      h('ul', {},
        h('li', { text: t('chat.emptyExampleSecret') }),
        h('li', { text: t('chat.emptyExampleFindings') }),
      ),
    );

  const buildLimited = (reason: string): HTMLElement =>
    h(
      'div',
      { class: 'empty-state chat-limited' },
      h('h3', { text: t('chat.limitedTitle') }),
      h('p', { text: t('chat.limitedDescription', { reason }) }),
      h(
        'button',
        {
          class: 'primary tiny',
          type: 'button',
          onclick: () => {
            useUiStore.getState().setSettingsSection('models');
            useUiStore.getState().setSettingsOpen(true);
          },
        },
        t('chat.chooseModel'),
      ),
    );

  // One element per message object. Messages are immutable, so an element is
  // rebuilt only when its message changed.
  const rendered = new Map<string, { message: ChatMessage; element: HTMLElement }>();
  let emptyKey = '';
  let lastThread: string | null = null;

  let lastBusy = false;
  const render = (force = false): void => {
    const state = useChatStore.getState();
    // The edit / branch / retry buttons depend on whether a turn is running.
    if (force || state.busy !== lastBusy) {
      lastBusy = state.busy;
      for (const entry of rendered.values()) entry.message = { ...entry.message, id: '' } as ChatMessage;
    }
    const nearBottom = messages.scrollHeight - messages.scrollTop - messages.clientHeight < 48;
    const model = state.model;

    modelChip.textContent = model.available
      ? t('chat.modelChip', { model: model.name, provider: providerLabel(model.provider) })
      : t('chat.noModel');
    modelChip.classList.toggle('is-bad', !model.available);
    modelChip.classList.toggle('neutral', model.available);
    modelChip.title = model.available
      ? model.tools === 'no'
        ? t('chat.modelTooltipNoTools', { model: model.name })
        : model.tools === 'yes'
          ? t('chat.modelTooltipWithTools', { model: model.name })
          : t('chat.modelTooltip', { model: model.name })
      : model.reason;
    const noWeb = model.available && model.tools === 'no';
    webBadge.hidden = !noWeb;
    webBadge.classList.toggle('is-warn', noWeb && Boolean(model.webSearch));
    const webText = webBadge.querySelector('.web-badge-text');
    if (webText) webText.textContent = model.webSearch ? t('chat.noWebSearch') : t('chat.noTools');
    webBadge.title = model.webSearch
      ? t('chat.webBadgeTitleSearchOn', { model: model.name })
      : t('chat.webBadgeTitleNoTools', { model: model.name });
    webBadge.setAttribute('role', 'button');
    webBadge.tabIndex = 0;
    input.disabled = !model.available;
    input.placeholder = model.available ? t('chat.inputPlaceholder') : t('chat.unavailableNoModel');

    // The ask button turns into Stop while a turn is running.
    askButton.disabled = !model.available && !state.busy;
    askButton.classList.toggle('is-stop', state.busy);
    const label = askButton.querySelector('.btn-label');
    if (label) label.textContent = state.busy ? t('chat.stop') : t('chat.ask');

    const thread = state.threads.find((t) => t.id === state.activeId);
    threadButton.replaceChildren(
      h('span', { class: 'thread-current', text: thread?.title ?? t('chat.newChat') }),
      icon('chevron-down'),
    );
    threadButton.title = t('chat.switchChat');
    if (!threadMenu.hidden && renamingId === null) renderMenu();

    if (state.activeId !== lastThread) {
      lastThread = state.activeId;
      rendered.clear();
      messages.replaceChildren();
      emptyKey = '';
    }

    if (!state.messages.length) {
      const key = model.available ? 'ready' : `limited:${model.reason}`;
      if (emptyKey !== key) {
        rendered.clear();
        messages.replaceChildren(model.available ? buildEmpty() : buildLimited(model.reason));
        emptyKey = key;
      }
    } else {
      if (emptyKey) {
        messages.replaceChildren();
        emptyKey = '';
      }
      const live = new Set<string>();
      state.messages.forEach((message, index) => {
        live.add(message.id);
        let entry = rendered.get(message.id);
        if (entry && entry.message !== message && onlyTextGrew(entry.message, message)) {
          // A streaming answer only grew: change its text in place. Rebuilding
          // the whole message on every word made it flicker and replay its
          // entrance animation.
          const next = renderContent(controller, message);
          const current = entry.element.querySelector(':scope > .content');
          if (current) current.replaceChildren(...Array.from(next.childNodes));
          else entry.element.querySelector(':scope > .role')?.after(next);
          entry = { message, element: entry.element };
          rendered.set(message.id, entry);
        } else if (!entry || entry.message !== message) {
          const element = buildMessage(message);
          if (entry) entry.element.replaceWith(element);
          entry = { message, element };
          rendered.set(message.id, entry);
        }
        if (messages.children[index] !== entry.element) {
          messages.insertBefore(entry.element, messages.children[index] ?? null);
        }
      });
      for (const [id, entry] of rendered) {
        if (!live.has(id)) {
          entry.element.remove();
          rendered.delete(id);
        }
      }
      if (nearBottom) messages.scrollTop = messages.scrollHeight;
    }

    // The code the next question is about.
    const sel = state.selection;
    selectionChip.hidden = !sel;
    if (sel) {
      selectionChip.replaceChildren(
        h('button', {
          class: 'citation',
          type: 'button',
          title: t('chat.showSelection'),
          text: `${sel.file}:${sel.startLine}${sel.endLine > sel.startLine ? `-${sel.endLine}` : ''}`,
          onclick: () => void controller.openFile(sel.file, sel.startLine),
        }),
        h('span', { class: 'muted', text: t('chat.selectionHint') }),
        h('button', {
          class: 'ghost tiny icon-button',
          type: 'button',
          title: t('chat.askWithoutSelection'),
          'aria-label': t('chat.removeSelection'),
          onclick: () => useChatStore.getState().setSelection(null),
        }, icon('x')),
      );
    }

    const last = state.messages[state.messages.length - 1];
    const used = last?.chunksUsed && last.chunksUsed.length ? t('chat.chunks', { chunks: last.chunksUsed.join(', ') }) : '';
    if (chunksUsed.textContent !== used) chunksUsed.textContent = used;
  };

  // The elapsed time of the running turn ticks without rebuilding the message.
  const tick = window.setInterval(() => {
    for (const el of messages.querySelectorAll<HTMLElement>('.chat-message.is-pending')) {
      const started = Number(el.dataset.started);
      const state = useChatStore.getState();
      const message = state.messages.find((m) => m.id === el.dataset.id);
      if (!message) continue;
      const text = el.querySelector('.status-text');
      if (text) text.textContent = statusText(message);
      const elapsed = el.querySelector('.status-elapsed');
      if (elapsed && started) elapsed.textContent = t('chat.elapsedSeconds', { seconds: Math.floor((Date.now() - started) / 1000) });
    }
  }, 1000);

  // "Ask in chat" from the editor focuses the input.
  // "Explain" in the editor asks straight away; other callers just focus the box.
  const onFocusChat = (event: Event): void => {
    setOpen(true);
    const ask = (event as CustomEvent<{ ask?: string } | undefined>).detail?.ask;
    if (ask && !useChatStore.getState().busy) {
      input.value = ask;
      submit();
    }
    input.focus();
  };
  window.addEventListener('aisai:focus-chat', onFocusChat);

  const unsubscribe = useChatStore.subscribe(() => render());

  setOpen(useUiStore.getState().chatOpen);
  render();

  return {
    element: container,
    header,
    dispose: () => {
      unsubscribe();
      window.clearInterval(tick);
      document.removeEventListener('click', onDocumentClick);
      window.removeEventListener('aisai:focus-chat', onFocusChat);
      if (armedTimer) clearTimeout(armedTimer);
    },
  };
}
