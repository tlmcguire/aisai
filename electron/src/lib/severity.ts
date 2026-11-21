import type { Severity } from '../api/types';

/** CSS class suffix for a severity, e.g. `sev-critical`. */
export function severityClass(severity: Severity): string {
  return `sev-${severity}`;
}

/** Human readable severity label. */
export function severityLabel(severity: Severity): string {
  return severity.charAt(0).toUpperCase() + severity.slice(1);
}

/** Numeric weight, mirroring model.severityRank (higher is worse). */
export function severityWeight(severity: Severity): number {
  switch (severity) {
    case 'critical':
      return 5;
    case 'high':
      return 4;
    case 'medium':
      return 3;
    case 'low':
      return 2;
    case 'info':
      return 1;
    default:
      return 0;
  }
}

/** Colour used for graph nodes and severity accents. */
export function severityColor(severity: Severity): string {
  switch (severity) {
    case 'critical':
      return '#ff7b72';
    case 'high':
      return '#ffa657';
    case 'medium':
      return '#d29922';
    case 'low':
      return '#58a6ff';
    case 'info':
      return '#8b949e';
    default:
      return '#8b949e';
  }
}

/** Dark fill used behind the severity colour on graph nodes. */
export function severityFill(severity: Severity): string {
  switch (severity) {
    case 'critical':
      return '#402c30';
    case 'high':
      return '#40342c';
    case 'medium':
      return '#383222';
    case 'low':
      return '#22344a';
    case 'info':
      return '#2b3138';
    default:
      return '#2b3138';
  }
}
