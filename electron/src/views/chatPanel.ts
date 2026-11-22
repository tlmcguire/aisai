import type { AppController } from '../app';
import type { ChatMessage } from '../state/chat';
import { h } from '../lib/dom';
import { icon } from '../lib/icons';
import { useChatStore } from '../state/chat';
import { useUiStore } from '../state/ui';
import { setBusy } from './toolbar';

const CITATION = /([\w./-]+\.[A-Za-z0-9]+):(\d+)/g;

function renderContent(
  controller: AppController,
  message: ChatMessage,
): HTMLElement {
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

export interface ChatPanel {
  element: HTMLElement;
  dispose: () => void;
}

export function createChatPanel(controller: AppController, container: HTMLElement): ChatPanel {
  const toggle = h('button', { class: 'ghost tiny icon-button', type: 'button' }, icon('chevron-down'));
  const chunksUsed = h('span', { class: 'chunks-used', text: '' });
  const header = h(
    'div',
    { class: 'chat-header' },
    h('span', { text: 'Chat' }),
    toggle,
    h('span', { class: 'grow' }),
    chunksUsed,
  );

  const messages = h('div', { class: 'chat-messages' });
  const input = h('input', {
    type: 'text',
    placeholder: 'Ask about this package… e.g. how does it authenticate?',
    'aria-label': 'Chat question',
  });
  const askButton = h('button', { class: 'primary', type: 'button' }, 'Ask');
  const footer = h('div', { class: 'chat-footer' }, input, askButton);

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
    const question = input.value.trim();
    if (!question) return;
    input.value = '';
    void controller.ask(question);
  };

  askButton.addEventListener('click', submit);
  input.addEventListener('keydown', (event) => {
    if (event.key === 'Enter') {
      event.preventDefault();
      submit();
    }
  });

  // One element per message object. Messages are immutable, so an element is
  // rebuilt only when its message changed (the streaming one, token by token)
  // and every other message stays untouched.
  const rendered = new Map<string, { message: ChatMessage; element: HTMLElement }>();
  let emptyShown = false;

  const buildMessage = (message: ChatMessage): HTMLElement =>
    h(
      'div',
      { class: `chat-message ${message.role}` },
      h('div', { class: 'role', text: message.role }),
      renderContent(controller, message),
      message.chunksUsed && message.chunksUsed.length
        ? h('div', { class: 'chunks-used', text: `chunks used: ${message.chunksUsed.join(', ')}` })
        : null,
    );

  const buildEmpty = (): HTMLElement =>
    h(
      'div',
      { class: 'empty-state' },
      h('h3', { text: 'Ask about this audit' }),
      h('p', {
        text:
          'The assistant answers questions about the scanned workspace and cites the exact ' +
          'file and line it used. Click a citation to jump to it.',
      }),
      h('ul', {},
        h('li', { text: '“Where is the hard-coded secret used?”' }),
        h('li', { text: '“Which findings are reachable from a request handler?”' }),
      ),
    );

  const render = (): void => {
    const state = useChatStore.getState();
    const nearBottom = messages.scrollHeight - messages.scrollTop - messages.clientHeight < 48;

    if (!state.messages.length) {
      if (!emptyShown) {
        rendered.clear();
        messages.replaceChildren(buildEmpty());
        emptyShown = true;
      }
    } else {
      if (emptyShown) {
        messages.replaceChildren();
        emptyShown = false;
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

    const last = state.messages[state.messages.length - 1];
    setBusy(askButton, state.busy, 'Ask');
    const used = last?.chunksUsed && last.chunksUsed.length ? `chunks: ${last.chunksUsed.join(', ')}` : '';
    if (chunksUsed.textContent !== used) chunksUsed.textContent = used;
  };

  const unsubscribe = useChatStore.subscribe(() => render());

  setOpen(useUiStore.getState().chatOpen);
  render();

  return {
    element: container,
    dispose: unsubscribe,
  };
}
