import { create } from 'zustand';

import type { CodeSelection } from '../api/types';

/** One tool the model ran while answering. */
export interface ToolStep {
  id: string;
  name: string;
  args: Record<string, unknown>;
  /** 'waiting' while the user is asked to allow the call. */
  status: 'running' | 'done' | 'error' | 'waiting';
  output?: string;
  error?: string;
  /** What the call will do, shown when it needs approval. */
  summary?: string;
  approval?: 'pending' | 'approved' | 'denied';
}

export interface ChatMessage {
  id: string;
  role: 'user' | 'assistant';
  content: string;
  streaming?: boolean;
  citations?: string[];
  chunksUsed?: string[];
  /** The model's reasoning, for a model that shows it. */
  thinking?: string;
  /** Seconds the model spent thinking, once it started answering. */
  thoughtFor?: number;
  /** When the turn started, for the elapsed time of the status line. */
  startedAt?: number;
  steps?: ToolStep[];
  /** Why the turn failed, when it did. The answer text is kept apart from it. */
  error?: string;
  /** True while older turns are being summarised to fit the context. */
  compacting?: boolean;
  /** A note that the history was compacted for this turn. */
  compacted?: string;
}

/** A conversation about one audit. */
export interface ChatThread {
  id: string;
  title: string;
  /** True until the user renames it; only then may the model rename it. */
  autoTitle: boolean;
  archived: boolean;
  createdAt: number;
  updatedAt: number;
  messages: ChatMessage[];
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

const STORAGE_PREFIX = 'aisai.chat.v2:';
/** The most threads and messages kept on disk per audit. */
const MAX_THREADS = 50;
const MAX_MESSAGES = 200;

interface Persisted {
  threads: ChatThread[];
  activeId: string | null;
}

function load(auditId: string): Persisted {
  try {
    const raw = localStorage.getItem(STORAGE_PREFIX + auditId);
    if (!raw) return { threads: [], activeId: null };
    const parsed = JSON.parse(raw) as Persisted;
    const threads = (parsed.threads ?? []).map((thread) => ({
      ...thread,
      // A turn that was streaming when the app closed will never finish.
      messages: thread.messages.map((m) => (m.streaming ? { ...m, streaming: false } : m)),
    }));
    return { threads, activeId: parsed.activeId ?? null };
  } catch {
    return { threads: [], activeId: null };
  }
}

function save(auditId: string | null, threads: ChatThread[], activeId: string | null): void {
  if (!auditId) return;
  try {
    const trimmed = threads.slice(0, MAX_THREADS).map((thread) => ({
      ...thread,
      messages: thread.messages.slice(-MAX_MESSAGES),
    }));
    localStorage.setItem(STORAGE_PREFIX + auditId, JSON.stringify({ threads: trimmed, activeId }));
  } catch {
    /* private mode or quota: the conversation just is not remembered */
  }
}

let counter = 0;
function nextId(prefix: string): string {
  counter += 1;
  return `${prefix}-${Date.now().toString(36)}-${counter}`;
}

export interface ChatState {
  /** Code the next question is about, picked in the editor. */
  selection: CodeSelection | null;
  setSelection: (selection: CodeSelection | null) => void;
  model: ChatModelInfo;
  setModel: (model: ChatModelInfo) => void;

  /** The audit the threads belong to. */
  auditId: string | null;
  threads: ChatThread[];
  activeId: string | null;
  /** Messages of the active thread, kept as a field so views can subscribe to it. */
  messages: ChatMessage[];
  busy: boolean;
  error: string | null;
  jobId: string | null;

  /** Switch to the threads of another audit. */
  setAudit: (auditId: string | null) => void;
  newThread: () => string;
  selectThread: (id: string) => void;
  renameThread: (id: string, title: string) => void;
  /** Set a title the model chose, unless the user named the thread. */
  autoTitleThread: (id: string, title: string) => void;
  archiveThread: (id: string, archived: boolean) => void;
  deleteThread: (id: string) => void;
  activeThread: () => ChatThread | null;

  pushUser: (text: string) => void;
  startAssistant: (jobId: string) => void;
  appendToken: (jobId: string, delta: string) => void;
  appendThinking: (jobId: string, delta: string) => void;
  upsertStep: (jobId: string, step: ToolStep) => void;
  finishAssistant: (jobId: string, citations: string[], chunksUsed: string[]) => void;
  failAssistant: (jobId: string, error: string) => void;
  setCompacting: (jobId: string, compacting: boolean, note?: string) => void;
  /** Remove a message and everything after it (to edit or retry from there). */
  truncateFrom: (messageId: string) => void;
  /** Start a new thread with the conversation up to and including a message. */
  forkFrom: (messageId: string) => string | null;
  /** Stop waiting for the running turn. */
  stop: () => void;
  clear: () => void;
}

/** Applies a change to the active thread and keeps the derived fields and the disk in step. */
function withActive(
  state: ChatState,
  change: (thread: ChatThread) => ChatThread,
  extra: Partial<ChatState> = {},
): Partial<ChatState> {
  if (!state.activeId) return extra;
  const threads = state.threads.map((thread) =>
    thread.id === state.activeId ? { ...change(thread), updatedAt: Date.now() } : thread,
  );
  const active = threads.find((thread) => thread.id === state.activeId);
  save(state.auditId, threads, state.activeId);
  return { threads, messages: active?.messages ?? [], ...extra };
}

/** Rewrites the last streaming assistant message. */
function mapStreaming(messages: ChatMessage[], change: (m: ChatMessage) => ChatMessage): ChatMessage[] {
  const out = messages.slice();
  for (let i = out.length - 1; i >= 0; i -= 1) {
    if (out[i].role === 'assistant' && out[i].streaming) {
      out[i] = change(out[i]);
      break;
    }
  }
  return out;
}

export const useChatStore = create<ChatState>((set, get) => ({
  selection: null,
  setSelection: (selection) => set({ selection }),
  model: NO_CHAT_MODEL,
  setModel: (model) => set({ model }),

  auditId: null,
  threads: [],
  activeId: null,
  messages: [],
  busy: false,
  error: null,
  jobId: null,

  setAudit: (auditId) => {
    if (get().auditId === auditId) return;
    const loaded = auditId ? load(auditId) : { threads: [], activeId: null };
    const open = loaded.threads.filter((thread) => !thread.archived);
    const activeId =
      loaded.threads.find((thread) => thread.id === loaded.activeId && !thread.archived)?.id ?? open[0]?.id ?? null;
    set({
      auditId,
      threads: loaded.threads,
      activeId,
      messages: loaded.threads.find((thread) => thread.id === activeId)?.messages ?? [],
      busy: false,
      error: null,
      jobId: null,
      selection: null,
    });
  },

  newThread: () => {
    const now = Date.now();
    const thread: ChatThread = {
      id: nextId('thread'),
      title: 'New chat',
      autoTitle: true,
      archived: false,
      createdAt: now,
      updatedAt: now,
      messages: [],
    };
    set((state) => {
      const threads = [thread, ...state.threads];
      save(state.auditId, threads, thread.id);
      return { threads, activeId: thread.id, messages: [], busy: false, error: null, jobId: null };
    });
    return thread.id;
  },

  selectThread: (id) =>
    set((state) => {
      const thread = state.threads.find((candidate) => candidate.id === id);
      if (!thread) return state;
      save(state.auditId, state.threads, id);
      return { activeId: id, messages: thread.messages, busy: false, error: null, jobId: null };
    }),

  renameThread: (id, title) =>
    set((state) => {
      const clean = title.trim();
      if (!clean) return state;
      const threads = state.threads.map((t) => (t.id === id ? { ...t, title: clean, autoTitle: false } : t));
      save(state.auditId, threads, state.activeId);
      return { threads };
    }),

  autoTitleThread: (id, title) =>
    set((state) => {
      const clean = title.trim();
      const threads = state.threads.map((t) => (t.id === id && t.autoTitle && clean ? { ...t, title: clean } : t));
      save(state.auditId, threads, state.activeId);
      return { threads };
    }),

  archiveThread: (id, archived) =>
    set((state) => {
      const threads = state.threads.map((t) => (t.id === id ? { ...t, archived } : t));
      let activeId = state.activeId;
      let messages = state.messages;
      if (archived && activeId === id) {
        activeId = threads.find((t) => !t.archived)?.id ?? null;
        messages = threads.find((t) => t.id === activeId)?.messages ?? [];
      }
      save(state.auditId, threads, activeId);
      return { threads, activeId, messages };
    }),

  deleteThread: (id) =>
    set((state) => {
      const threads = state.threads.filter((t) => t.id !== id);
      let activeId = state.activeId;
      let messages = state.messages;
      if (activeId === id) {
        activeId = threads.find((t) => !t.archived)?.id ?? null;
        messages = threads.find((t) => t.id === activeId)?.messages ?? [];
      }
      save(state.auditId, threads, activeId);
      return { threads, activeId, messages, ...(state.activeId === id ? { busy: false, jobId: null } : {}) };
    }),

  activeThread: () => get().threads.find((t) => t.id === get().activeId) ?? null,

  pushUser: (text) => {
    if (!get().activeId) get().newThread();
    set((state) =>
      withActive(state, (thread) => {
        const messages = [...thread.messages, { id: nextId('user'), role: 'user' as const, content: text }];
        // Until the model names it, a thread is called by the start of its first question.
        const title =
          thread.autoTitle && thread.messages.length === 0
            ? text.replace(/\s+/g, ' ').trim().slice(0, 40) || thread.title
            : thread.title;
        return { ...thread, messages, title };
      }),
    );
  },

  startAssistant: (jobId) =>
    set((state) =>
      withActive(
        state,
        (thread) => ({
          ...thread,
          messages: [
            ...thread.messages,
            { id: nextId('assistant'), role: 'assistant', content: '', streaming: true, startedAt: Date.now() },
          ],
        }),
        { busy: true, jobId, error: null },
      ),
    ),

  appendToken: (jobId, delta) =>
    set((state) => {
      if (state.jobId !== jobId) return state;
      return withActive(state, (thread) => ({
        ...thread,
        messages: mapStreaming(thread.messages, (m) => ({
          ...m,
          content: m.content + delta,
          // The first answer text ends the thinking phase.
          thoughtFor:
            m.thinking && m.thoughtFor === undefined && m.startedAt
              ? Math.max(1, Math.round((Date.now() - m.startedAt) / 1000))
              : m.thoughtFor,
        })),
      }));
    }),

  appendThinking: (jobId, delta) =>
    set((state) => {
      if (state.jobId !== jobId) return state;
      return withActive(state, (thread) => ({
        ...thread,
        messages: mapStreaming(thread.messages, (m) => ({ ...m, thinking: (m.thinking ?? '') + delta })),
      }));
    }),

  upsertStep: (jobId, step) =>
    set((state) => {
      if (state.jobId !== jobId) return state;
      return withActive(state, (thread) => ({
        ...thread,
        messages: mapStreaming(thread.messages, (m) => {
          const steps = (m.steps ?? []).slice();
          const at = steps.findIndex((s) => s.id === step.id);
          if (at >= 0) {
            // A later report only adds to what is known: it never blanks the
            // name, the arguments or the approval summary.
            const prev = steps[at];
            steps[at] = {
              ...prev,
              ...step,
              name: step.name || prev.name,
              args: step.args && Object.keys(step.args).length ? step.args : prev.args,
              summary: step.summary ?? prev.summary,
              approval: step.approval ?? prev.approval,
            };
          } else steps.push(step);
          return { ...m, steps };
        }),
      }));
    }),

  finishAssistant: (jobId, citations, chunksUsed) =>
    set((state) => {
      if (state.jobId !== jobId) return state;
      return withActive(
        state,
        (thread) => ({
          ...thread,
          messages: mapStreaming(thread.messages, (m) => ({
            ...m,
            streaming: false,
            citations,
            chunksUsed,
            // Tools that never reported back are not still running.
            steps: m.steps?.map((s) => (s.status === 'running' ? { ...s, status: 'done' as const } : s)),
          })),
        }),
        { busy: false, jobId: null },
      );
    }),

  failAssistant: (jobId, error) =>
    set((state) => {
      if (state.jobId && state.jobId !== jobId) return state;
      return withActive(
        state,
        (thread) => ({
          ...thread,
          messages: mapStreaming(thread.messages, (m) => ({
            ...m,
            streaming: false,
            error,
            compacting: false,
            steps: m.steps?.map((s) => (s.status === 'running' || s.status === 'waiting' ? { ...s, status: 'error' as const } : s)),
          })),
        }),
        { busy: false, error, jobId: null },
      );
    }),

  setCompacting: (jobId, compacting, note) =>
    set((state) => {
      if (state.jobId !== jobId) return state;
      return withActive(state, (thread) => ({
        ...thread,
        messages: mapStreaming(thread.messages, (m) => ({ ...m, compacting, compacted: note ?? m.compacted })),
      }));
    }),

  truncateFrom: (messageId) =>
    set((state) =>
      withActive(
        state,
        (thread) => {
          const at = thread.messages.findIndex((m) => m.id === messageId);
          return at < 0 ? thread : { ...thread, messages: thread.messages.slice(0, at) };
        },
        { busy: false, jobId: null, error: null },
      ),
    ),

  forkFrom: (messageId) => {
    const source = get().activeThread();
    if (!source) return null;
    const at = source.messages.findIndex((m) => m.id === messageId);
    if (at < 0) return null;
    const now = Date.now();
    const thread: ChatThread = {
      id: nextId('thread'),
      title: `${source.title} (branch)`,
      autoTitle: false,
      archived: false,
      createdAt: now,
      updatedAt: now,
      messages: source.messages.slice(0, at + 1).map((m) => ({ ...m, id: nextId(m.role), streaming: false })),
    };
    set((state) => {
      const threads = [thread, ...state.threads];
      save(state.auditId, threads, thread.id);
      return { threads, activeId: thread.id, messages: thread.messages, busy: false, error: null, jobId: null };
    });
    return thread.id;
  },

  stop: () =>
    set((state) =>
      withActive(
        state,
        (thread) => ({
          ...thread,
          messages: mapStreaming(thread.messages, (m) => ({
            ...m,
            streaming: false,
            content: m.content || 'Stopped.',
          })),
        }),
        { busy: false, jobId: null },
      ),
    ),

  clear: () => {
    const id = get().activeId;
    if (id) get().deleteThread(id);
    set({ busy: false, error: null, jobId: null });
  },
}));
