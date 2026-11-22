import { create } from 'zustand';

/** One frame of the call stack, innermost first. */
export interface DebugFrame {
  file: string;
  line: number;
  name: string;
  /** The file is outside the project (the standard library, a package). */
  external?: boolean;
}

export interface DebugVariable {
  name: string;
  type: string;
  value: string;
}

/** A line of the debug console: program output, an evaluation, a note. */
export interface ConsoleLine {
  kind: 'stdout' | 'stderr' | 'input' | 'result' | 'error' | 'info';
  text: string;
}

export type DebugStatus = 'idle' | 'starting' | 'running' | 'paused' | 'exited';

export interface DebugState {
  /** Breakpoint lines per workspace-relative file. */
  breakpoints: Record<string, number[]>;
  /** The audit the breakpoints belong to. */
  auditId: string | null;

  sessionId: string | null;
  status: DebugStatus;
  program: string | null;
  python: string | null;
  exitCode: number | null;
  reason: string;
  description: string;
  frames: DebugFrame[];
  frame: number;
  locals: DebugVariable[];
  globals: DebugVariable[];
  console: ConsoleLine[];
  /** Why the debugger could not start. */
  error: string | null;
  /** The user has seen and accepted that debugging runs the project's code. */
  consented: boolean;

  setAudit: (auditId: string | null) => void;
  setBreakpoints: (file: string, lines: number[]) => void;
  toggleBreakpoint: (file: string, line: number) => void;
  clearBreakpoints: () => void;
  starting: (program: string) => void;
  started: (sessionId: string, python: string) => void;
  failed: (error: string) => void;
  stopped: (event: { reason?: string; description?: string; frames?: DebugFrame[]; frame?: number; locals?: DebugVariable[]; globals?: DebugVariable[] }) => void;
  scope: (frame: number, locals: DebugVariable[], globals: DebugVariable[]) => void;
  continued: () => void;
  exited: (code: number) => void;
  print: (line: ConsoleLine) => void;
  clearConsole: () => void;
  consent: () => void;
}

/** The console keeps this many characters, the oldest dropped first. */
const CONSOLE_LIMIT = 200_000;

function sortedUnique(lines: number[]): number[] {
  return Array.from(new Set(lines.filter((n) => Number.isInteger(n) && n > 0))).sort((a, b) => a - b);
}

/** Appends a line, joining output chunks of the same stream and capping the total. */
export function appendConsole(lines: ConsoleLine[], line: ConsoleLine): ConsoleLine[] {
  const next = lines.slice();
  const last = next[next.length - 1];
  if (last && last.kind === line.kind && (line.kind === 'stdout' || line.kind === 'stderr')) {
    next[next.length - 1] = { kind: last.kind, text: last.text + line.text };
  } else {
    next.push(line);
  }
  let total = next.reduce((sum, l) => sum + l.text.length, 0);
  while (total > CONSOLE_LIMIT && next.length > 1) {
    total -= next[0].text.length;
    next.shift();
  }
  if (total > CONSOLE_LIMIT) next[0] = { ...next[0], text: next[0].text.slice(total - CONSOLE_LIMIT) };
  return next;
}

const idleSession = {
  sessionId: null,
  status: 'idle' as DebugStatus,
  exitCode: null,
  reason: '',
  description: '',
  frames: [],
  frame: 0,
  locals: [],
  globals: [],
};

export const useDebugStore = create<DebugState>((set) => ({
  breakpoints: {},
  auditId: null,
  ...idleSession,
  program: null,
  python: null,
  console: [],
  error: null,
  consented: false,

  setAudit: (auditId) =>
    set((state) => (state.auditId === auditId ? {} : { auditId, breakpoints: {}, ...idleSession, program: null, console: [], error: null })),
  setBreakpoints: (file, lines) =>
    set((state) => {
      const next = sortedUnique(lines);
      const have = state.breakpoints[file] ?? [];
      if (have.length === next.length && have.every((n, i) => n === next[i])) return {};
      const breakpoints = { ...state.breakpoints };
      if (next.length) breakpoints[file] = next;
      else delete breakpoints[file];
      return { breakpoints };
    }),
  toggleBreakpoint: (file, line) =>
    set((state) => {
      const have = state.breakpoints[file] ?? [];
      const next = have.includes(line) ? have.filter((n) => n !== line) : sortedUnique([...have, line]);
      const breakpoints = { ...state.breakpoints };
      if (next.length) breakpoints[file] = next;
      else delete breakpoints[file];
      return { breakpoints };
    }),
  clearBreakpoints: () => set({ breakpoints: {} }),
  starting: (program) => set({ ...idleSession, status: 'starting', program, error: null, console: [] }),
  started: (sessionId, python) => set((state) => ({ sessionId, python, status: state.status === 'starting' ? 'running' : state.status })),
  failed: (error) => set({ ...idleSession, error }),
  stopped: (event) =>
    set({
      status: 'paused',
      reason: event.reason ?? 'step',
      description: event.description ?? '',
      frames: event.frames ?? [],
      frame: event.frame ?? 0,
      locals: event.locals ?? [],
      globals: event.globals ?? [],
    }),
  scope: (frame, locals, globals) => set({ frame, locals, globals }),
  continued: () => set((state) => (state.status === 'paused' ? { status: 'running', frames: [], locals: [], globals: [], reason: '', description: '' } : {})),
  exited: (code) => set({ status: 'exited', exitCode: code, frames: [], locals: [], globals: [], reason: '', description: '' }),
  print: (line) => set((state) => ({ console: appendConsole(state.console, line) })),
  clearConsole: () => set({ console: [] }),
  consent: () => set({ consented: true }),
}));

/** The line the program is paused on, if it is in this file. */
export function pausedLineIn(state: DebugState, file: string): number | null {
  if (state.status !== 'paused') return null;
  const frame = state.frames[state.frame];
  return frame && !frame.external && frame.file === file ? frame.line : null;
}
