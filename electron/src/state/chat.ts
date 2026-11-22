import { create } from 'zustand';

export interface ChatMessage {
  id: string;
  role: 'user' | 'assistant';
  content: string;
  streaming?: boolean;
  citations?: string[];
  chunksUsed?: string[];
}

/** The model that answers chat questions, and whether one can. */
export interface ChatModelInfo {
  /** The model name, e.g. "qwen3.5:4b"; empty when none is selected. */
  name: string;
  /** The kind of the provider serving it (ollama, openai, …). */
  provider: string;
  /** Whether a question can be answered right now. */
  available: boolean;
  /** Why chat is limited, shown to the user when it is not available. */
  reason: string;
}

export const NO_CHAT_MODEL: ChatModelInfo = {
  name: '',
  provider: '',
  available: false,
  reason: 'Checking for a model…',
};

export interface ChatState {
  model: ChatModelInfo;
  setModel: (model: ChatModelInfo) => void;
  messages: ChatMessage[];
  busy: boolean;
  error: string | null;
  jobId: string | null;
  pushUser: (text: string) => void;
  startAssistant: (jobId: string) => void;
  appendToken: (jobId: string, delta: string) => void;
  finishAssistant: (jobId: string, citations: string[], chunksUsed: string[]) => void;
  failAssistant: (jobId: string, error: string) => void;
  clear: () => void;
}

let counter = 0;
function nextId(prefix: string): string {
  counter += 1;
  return `${prefix}-${counter}`;
}

export const useChatStore = create<ChatState>((set) => ({
  model: NO_CHAT_MODEL,
  setModel: (model) => set({ model }),
  messages: [],
  busy: false,
  error: null,
  jobId: null,
  pushUser: (text) =>
    set((state) => ({
      messages: [...state.messages, { id: nextId('user'), role: 'user', content: text }],
    })),
  startAssistant: (jobId) =>
    set((state) => ({
      busy: true,
      jobId,
      error: null,
      messages: [
        ...state.messages,
        { id: nextId('assistant'), role: 'assistant', content: '', streaming: true },
      ],
    })),
  appendToken: (jobId, delta) =>
    set((state) => {
      if (state.jobId !== jobId) return state;
      const messages = state.messages.slice();
      for (let i = messages.length - 1; i >= 0; i -= 1) {
        const message = messages[i];
        if (message.role === 'assistant' && message.streaming) {
          messages[i] = { ...message, content: message.content + delta };
          break;
        }
      }
      return { messages };
    }),
  finishAssistant: (jobId, citations, chunksUsed) =>
    set((state) => {
      if (state.jobId !== jobId) return state;
      const messages = state.messages.slice();
      for (let i = messages.length - 1; i >= 0; i -= 1) {
        const message = messages[i];
        if (message.role === 'assistant' && message.streaming) {
          messages[i] = { ...message, streaming: false, citations, chunksUsed };
          break;
        }
      }
      return { messages, busy: false, jobId: null };
    }),
  failAssistant: (jobId, error) =>
    set((state) => {
      if (state.jobId && state.jobId !== jobId) return state;
      const messages = state.messages.slice();
      for (let i = messages.length - 1; i >= 0; i -= 1) {
        const message = messages[i];
        if (message.role === 'assistant' && message.streaming) {
          messages[i] = {
            ...message,
            streaming: false,
            content: message.content || `The assistant could not answer: ${error}`,
          };
          break;
        }
      }
      return { messages, busy: false, error, jobId: null };
    }),
  clear: () => set({ messages: [], busy: false, error: null, jobId: null }),
}));
