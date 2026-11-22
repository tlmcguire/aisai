import type { AppController } from '../app';
import type { ChatMessage, ChatThread, ToolStep } from '../state/chat';
import { h } from '../lib/dom';
import { icon, type IconName } from '../lib/icons';
import { useChatStore } from '../state/chat';
import { useUiStore } from '../state/ui';
import { providerLabel } from './settingsDialog';

const CITATION = /([\w./-]+\.[A-Za-z0-9]+):(\d+)/g;

/** What the status line says while the model has not written anything yet. */
const IDLE_VERBS = ['Thinking', 'Reading the code', 'Searching the index', 'Working it out'];

function renderContent(controller: AppController, message: ChatMessage): HTMLElement {
  const content = h('div', { class: 'content' });
  const text = message.content;
  let lastIndex = 0;
  CITATION.lastIndex = 0;
  let match = CITATION.exec(text);

  while (match) {
    if (match.index > lastIndex) {
      content.appendChild(document.createTextNode(text.slice(lastIndex, match.index)));
    }
    const file = match[1];
    const line = Number(match[2]);
    content.appendChild(
      h(
        'button',
        {
          class: 'citation',
          type: 'button',
          title: `Open ${file} at line ${line}`,
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
    content.appendChild(document.createTextNode(text.slice(lastIndex)));
  }
  return content;
}

/** The text of the shimmering status line, for a turn that is still going. */
function statusText(message: ChatMessage): string {
  const running = message.steps?.find((step) => step.status === 'running');
  if (running) return `Running ${running.name}`;
  if (message.content) return 'Writing the answer';
  if (message.thinking) return 'Thinking';
  const seconds = message.startedAt ? Math.floor((Date.now() - message.startedAt) / 1000) : 0;
  return IDLE_VERBS[Math.floor(seconds / 4) % IDLE_VERBS.length];
}

function relativeTime(then: number): string {
  const minutes = Math.round((Date.now() - then) / 60000);
  if (minutes < 1) return 'just now';
  if (minutes < 60) return `${minutes} min ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours} h ago`;
  return `${Math.round(hours / 24)} d ago`;
}

function argPreview(args: Record<string, unknown>): string {
  const first = Object.values(args)[0];
  if (first === undefined) return '';
  const text = typeof first === 'string' ? first : JSON.stringify(first);
  return text.length > 60 ? `${text.slice(0, 60)}…` : text;
}

export interface ChatPanel {
  element: HTMLElement;
  dispose: () => void;
}

export function createChatPanel(controller: AppController, container: HTMLElement): ChatPanel {
  const toggle = h('button', { class: 'ghost tiny icon-button', type: 'button' }, icon('chevron-down'));
  const chunksUsed = h('span', { class: 'chunks-used', text: '' });
  const modelChip = h('span', { class: 'badge neutral chat-model', text: '' });

  /* ---------------- threads ---------------- */
  const threadButton = h('button', { class: 'tiny thread-button', type: 'button', 'aria-haspopup': 'true', 'aria-expanded': 'false' });
  const newButton = h(
    'button',
    { class: 'ghost tiny icon-button', type: 'button', title: 'New chat', 'aria-label': 'New chat' },
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
    h('span', { text: 'Chat' }),
    threadWrap,
    newButton,
    modelChip,
    toggle,
    h('span', { class: 'grow' }),
    chunksUsed,
  );

  const messages = h('div', { class: 'chat-messages' });
  const selectionChip = h('div', { class: 'selection-chip', hidden: true });
  const input = h('input', {
    type: 'text',
    placeholder: 'Ask about this package… e.g. how does it authenticate?',
    'aria-label': 'Chat question',
  });
  const askButton = h('button', { class: 'primary', type: 'button' }, h('span', { class: 'btn-label', text: 'Ask' }));
  const footer = h('div', { class: 'chat-footer' }, selectionChip, h('div', { class: 'chat-input-row' }, input, askButton));

  container.append(header, messages, footer);

  const setOpen = (open: boolean): void => {
    useUiStore.getState().setChatOpen(open);
    container.classList.toggle('collapsed', !open);
    toggle.replaceChildren(icon(open ? 'chevron-down' : 'chevron-up'));
    toggle.setAttribute('aria-expanded', String(open));
    toggle.setAttribute('aria-label', open ? 'Collapse the chat panel' : 'Expand the chat panel');
    toggle.title = open ? 'Collapse the chat panel' : 'Expand the chat panel';
    footer.hidden = !open;
    messages.hidden = !open;
  };

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
      armed ? 'Delete?' : icon(name),
    );

  const renderMenu = (): void => {
    const state = useChatStore.getState();
    const visible = state.threads.filter((thread) => showArchived || !thread.archived);
    const archivedCount = state.threads.filter((thread) => thread.archived).length;
    const rows = visible.map((thread: ChatThread) => {
      const title =
        renamingId === thread.id
          ? (() => {
              const field = h('input', { class: 'thread-rename', type: 'text', value: thread.title, 'aria-label': 'Chat name' });
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
        rowButton('pencil', 'Rename', () => {
          renamingId = thread.id;
          renderMenu();
        }),
        rowButton(thread.archived ? 'restore' : 'archive', thread.archived ? 'Restore' : 'Archive', () => {
          useChatStore.getState().archiveThread(thread.id, !thread.archived);
          renderMenu();
        }),
        rowButton(
          'trash',
          'Delete',
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
            showArchived ? 'Hide archived' : `Show archived (${archivedCount})`,
          )
        : null;
    threadMenu.replaceChildren(
      ...(rows.length ? rows : [h('p', { class: 'thread-empty muted', text: 'No chats yet. Ask a question to start one.' })]),
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
      ? h('span', { class: 'spinner', 'aria-label': 'running' })
      : icon(step.status === 'error' ? 'x' : 'check');

  const buildSteps = (message: ChatMessage): HTMLElement[] =>
    (message.steps ?? []).map((step) =>
      block(
        `${message.id}:${step.id}`,
        [
          stepIcon(step),
          h('span', { class: 'tool-name', text: step.name }),
          h('span', { class: 'tool-arg muted', text: argPreview(step.args) }),
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
        live ? h('span', { class: 'shimmer', text: 'Thinking…' }) : h('span', { class: 'muted', text: message.thoughtFor ? `Thought for ${message.thoughtFor}s` : 'Thoughts' }),
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
          h('span', { class: 'shimmer status-text', text: `${statusText(message)}…` }),
          h('span', { class: 'status-elapsed muted' }),
        )
      : null;
    return h(
      'div',
      { class: `chat-message ${message.role}${waiting ? ' is-pending' : ''}`, 'data-id': message.id, 'data-started': String(message.startedAt ?? '') },
      h('div', { class: 'role', text: message.role }),
      message.role === 'assistant' ? buildThinking(message) : null,
      ...(message.role === 'assistant' ? buildSteps(message) : []),
      message.content ? renderContent(controller, message) : null,
      status,
      message.chunksUsed && message.chunksUsed.length
        ? h('div', { class: 'chunks-used', text: `chunks used: ${message.chunksUsed.join(', ')}` })
        : null,
    );
  };

  const buildEmpty = (): HTMLElement =>
    h(
      'div',
      { class: 'empty-state' },
      h('h3', { text: 'Ask about this audit' }),
      h('p', {
        text:
          'The assistant answers questions about the scanned workspace and cites the exact ' +
          'file and line it used. Click a citation to jump to it. Select code in the editor to ask about just that.',
      }),
      h('ul', {},
        h('li', { text: '“Where is the hard-coded secret used?”' }),
        h('li', { text: '“Which findings are reachable from a request handler?”' }),
      ),
    );

  const buildLimited = (reason: string): HTMLElement =>
    h(
      'div',
      { class: 'empty-state chat-limited' },
      h('h3', { text: 'Chat is limited: no model' }),
      h('p', {
        text: `${reason} Questions are disabled until a model is available; scanning, the graph, the AST and the static rules still work.`,
      }),
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
        'Choose a model',
      ),
    );

  // One element per message object. Messages are immutable, so an element is
  // rebuilt only when its message changed.
  const rendered = new Map<string, { message: ChatMessage; element: HTMLElement }>();
  let emptyKey = '';
  let lastThread: string | null = null;

  const render = (): void => {
    const state = useChatStore.getState();
    const nearBottom = messages.scrollHeight - messages.scrollTop - messages.clientHeight < 48;
    const model = state.model;

    modelChip.textContent = model.available ? `${model.name} · ${providerLabel(model.provider)}` : 'no model';
    modelChip.classList.toggle('is-bad', !model.available);
    modelChip.classList.toggle('neutral', model.available);
    modelChip.title = model.available ? `Answering with ${model.name}` : model.reason;
    input.disabled = !model.available;
    input.placeholder = model.available
      ? 'Ask about this package… e.g. how does it authenticate?'
      : 'Chat is unavailable: no model';

    // The ask button turns into Stop while a turn is running.
    askButton.disabled = !model.available && !state.busy;
    askButton.classList.toggle('is-stop', state.busy);
    const label = askButton.querySelector('.btn-label');
    if (label) label.textContent = state.busy ? 'Stop' : 'Ask';

    const thread = state.threads.find((t) => t.id === state.activeId);
    threadButton.replaceChildren(
      h('span', { class: 'thread-current', text: thread?.title ?? 'New chat' }),
      icon('chevron-down'),
    );
    threadButton.title = 'Switch chat';
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
        if (!entry || entry.message !== message) {
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
          title: 'Show the selection',
          text: `${sel.file}:${sel.startLine}${sel.endLine > sel.startLine ? `-${sel.endLine}` : ''}`,
          onclick: () => void controller.openFile(sel.file, sel.startLine),
        }),
        h('span', { class: 'muted', text: 'The next question is about this code' }),
        h('button', {
          class: 'ghost tiny icon-button',
          type: 'button',
          title: 'Ask without the selection',
          'aria-label': 'Remove the selection',
          onclick: () => useChatStore.getState().setSelection(null),
        }, icon('x')),
      );
    }

    const last = state.messages[state.messages.length - 1];
    const used = last?.chunksUsed && last.chunksUsed.length ? `chunks: ${last.chunksUsed.join(', ')}` : '';
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
      if (text) text.textContent = `${statusText(message)}…`;
      const elapsed = el.querySelector('.status-elapsed');
      if (elapsed && started) elapsed.textContent = `${Math.floor((Date.now() - started) / 1000)}s`;
    }
  }, 1000);

  // "Ask in chat" from the editor focuses the input.
  const onFocusChat = (): void => {
    setOpen(true);
    input.focus();
  };
  window.addEventListener('aisai:focus-chat', onFocusChat);

  const unsubscribe = useChatStore.subscribe(() => render());

  setOpen(useUiStore.getState().chatOpen);
  render();

  return {
    element: container,
    dispose: () => {
      unsubscribe();
      window.clearInterval(tick);
      document.removeEventListener('click', onDocumentClick);
      window.removeEventListener('aisai:focus-chat', onFocusChat);
      if (armedTimer) clearTimeout(armedTimer);
    },
  };
}
