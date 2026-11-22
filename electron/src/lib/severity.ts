import type { Severity } from '../api/types';
import { t } from './i18n';

/** CSS class suffix for a severity, e.g. `sev-critical`. */
export function severityClass(severity: Severity): string {
  return `sev-${severity}`;
}

/**
 * Human readable severity label, in the language of the interface.
 *
 * The severity itself is a value from the backend and stays as it is; only
 * the word shown to a person is translated.
 */
export function severityLabel(severity: Severity): string {
  return t(`severity.${severity}`);
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
      return '#ff7a6b';
    case 'high':
      return '#ff9f4a';
    case 'medium':
      return '#f0c93a';
    case 'low':
      return '#5fd3c4';
    case 'info':
      return '#8ea5a0';
    default:
      return '#8ea5a0';
  }
}

/** Dark fill used behind the severity colour on graph nodes. */
export function severityFill(severity: Severity): string {
  switch (severity) {
    case 'critical':
      return '#352826';
    case 'high':
      return '#352e21';
    case 'medium':
      return '#33341e';
    case 'low':
      return '#1c3634';
    case 'info':
      return '#232f2f';
    default:
      return '#232f2f';
  }
}
