import { create } from 'zustand';

/** One shell in the terminal panel. */
export interface TerminalSession {
  id: string;
  title: string;
  cwd: string;
  /** Set when the shell ended; the exit code. */
  exited?: number;
}

export interface TerminalState {
  sessions: TerminalSession[];
  activeId: string | null;
  /** Why a shell could not be started, shown in the panel. */
  error: string | null;
  add: (session: TerminalSession) => void;
  remove: (id: string) => void;
  select: (id: string) => void;
  markExited: (id: string, code: number) => void;
  setError: (error: string | null) => void;
}

export const useTerminalStore = create<TerminalState>((set) => ({
  sessions: [],
  activeId: null,
  error: null,
  add: (session) => set((state) => ({ sessions: [...state.sessions, session], activeId: session.id, error: null })),
  remove: (id) =>
    set((state) => {
      const sessions = state.sessions.filter((s) => s.id !== id);
      const activeId = state.activeId === id ? (sessions[sessions.length - 1]?.id ?? null) : state.activeId;
      return { sessions, activeId };
    }),
  select: (id) => set({ activeId: id }),
  markExited: (id, code) =>
    set((state) => ({ sessions: state.sessions.map((s) => (s.id === id ? { ...s, exited: code } : s)) })),
  setError: (error) => set({ error }),
}));

/**
 * Output of the shells. It bypasses the store on purpose: a terminal prints
 * many small chunks a second, and none of them is state the interface shows.
 */
type Listener = (bytes: Uint8Array) => void;
const listeners = new Map<string, Listener>();
const backlog = new Map<string, Uint8Array[]>();

export const terminalBus = {
  /** Output that arrives before the terminal exists is kept and replayed. */
  emit(id: string, bytes: Uint8Array): void {
    const listener = listeners.get(id);
    if (listener) listener(bytes);
    else backlog.set(id, [...(backlog.get(id) ?? []), bytes]);
  },
  listen(id: string, listener: Listener): () => void {
    listeners.set(id, listener);
    for (const chunk of backlog.get(id) ?? []) listener(chunk);
    backlog.delete(id);
    return () => {
      if (listeners.get(id) === listener) listeners.delete(id);
    };
  },
  forget(id: string): void {
    listeners.delete(id);
    backlog.delete(id);
  },
};
