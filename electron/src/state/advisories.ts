/**
 * The vulnerabilities of the open audit's dependencies, from the advisory
 * database (OSV). Each entry names the affected package and the chain of
 * packages that pulls it in (api/types.ts, Advisory.via).
 *
 * "Nothing found" and "not checked yet" are different answers, so the store
 * keeps both: a local project is only checked when the person asks, because
 * checking sends its dependency names and versions to osv.dev.
 */

import { create } from 'zustand';

import type { Advisory } from '../api/types';

export type AdvisoryStatus = 'unknown' | 'checking' | 'ready' | 'error';

export interface AdvisoryState {
  list: Advisory[];
  status: AdvisoryStatus;
  /** How many packages the last check looked up. */
  checked: number;
  error: string | null;
  setList: (list: Advisory[], checked: number, fetched: boolean) => void;
  setChecking: () => void;
  setError: (error: string) => void;
  reset: () => void;
}

export const useAdvisoryStore = create<AdvisoryState>((set) => ({
  list: [],
  status: 'unknown',
  checked: 0,
  error: null,
  setList: (list, checked, fetched) => set({ list, checked, status: fetched ? 'ready' : 'unknown', error: null }),
  setChecking: () => set({ status: 'checking', error: null }),
  setError: (error) => set({ status: 'error', error }),
  reset: () => set({ list: [], status: 'unknown', checked: 0, error: null }),
}));

const SEVERITY_ORDER = ['critical', 'high', 'medium', 'low', 'info'];

/** Advisories ordered worst first, then by package, so the list reads top-down. */
export function sortAdvisories(list: Advisory[]): Advisory[] {
  return [...list].sort((a, b) => {
    const s = SEVERITY_ORDER.indexOf(a.severity) - SEVERITY_ORDER.indexOf(b.severity);
    if (s !== 0) return s;
    return (a.package ?? '').localeCompare(b.package ?? '') || a.id.localeCompare(b.id);
  });
}

/** A package name and version out of a PURL ("pkg:cargo/time@0.1.43" is "time 0.1.43"). */
export function packageLabel(purl: string | undefined): string {
  if (!purl) return '';
  const body = purl.replace(/^pkg:[^/]+\//, '');
  const at = body.lastIndexOf('@');
  return at > 0 ? `${body.slice(0, at)} ${body.slice(at + 1)}` : body;
}
