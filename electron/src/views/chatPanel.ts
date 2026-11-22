import type { AppController } from '../app';
import type { ChatMessage } from '../state/chat';
import { h } from '../lib/dom';
import { icon } from '../lib/icons';
import { useChatStore } from '../state/chat';
import { useUiStore } from '../state/ui';
import { setBusy } from './toolbar';
import { providerLabel } from './settingsDialog';

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
  const modelChip = h('span', { class: 'badge neutral chat-model', text: '' });
  const header = h(
    'div',
    { class: 'chat-header' },
    h('span', { text: 'Chat' }),
    modelChip,
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
    if (!question || !useChatStore.getState().model.available) return;
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
  let emptyKey = '';

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

    const model = state.model;
    modelChip.textContent = model.available
      ? `${model.name} · ${providerLabel(model.provider)}`
      : 'no model';
    modelChip.classList.toggle('is-bad', !model.available);
    modelChip.classList.toggle('neutral', model.available);
    modelChip.title = model.available ? `Answering with ${model.name}` : model.reason;
    input.disabled = !model.available;
    askButton.disabled = !model.available || state.busy;
    input.placeholder = model.available
      ? 'Ask about this package… e.g. how does it authenticate?'
      : 'Chat is unavailable: no model';

    if (!state.messages.length) {
      const key = model.available ? 'ready' : `limited:${model.reason}`;
      if (!emptyShown || emptyKey !== key) {
        rendered.clear();
        messages.replaceChildren(model.available ? buildEmpty() : buildLimited(model.reason));
        emptyShown = true;
        emptyKey = key;
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
    if (!model.available) askButton.disabled = true;
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
