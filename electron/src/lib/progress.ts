import { formatBytes } from './format';

/** "45s", "3m 05s", "1h 02m". */
export function formatDuration(seconds: number): string {
  const s = Math.max(0, Math.round(seconds));
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${String(s % 60).padStart(2, '0')}s`;
  return `${Math.floor(m / 60)}h ${String(m % 60).padStart(2, '0')}m`;
}

export interface ProgressInput {
  now: number;
  /** When the job started. */
  startedAt: number;
  /** When the current phase started, and the percent (0–1) it started at. */
  phaseStartedAt: number;
  phaseStartPercent: number;
  percent: number;
  /** Package downloads, when the job is downloading them. */
  downloadsDone: number;
  downloadsTotal: number;
  downloadedBytes: number;
}

/**
 * The stats line of a running job: what is done, how fast, how long it has
 * run and how long it should take. An estimate is only given once there is
 * enough progress to extrapolate from, so it never starts as a wild guess.
 */
export function progressStats(input: ProgressInput): string {
  const parts: string[] = [];
  const elapsed = (input.now - input.startedAt) / 1000;
  const downloading = input.downloadsTotal > 0 && input.downloadsDone < input.downloadsTotal;

  let eta: number | null = null;
  if (input.downloadsTotal > 0) {
    parts.push(`${input.downloadsDone}/${input.downloadsTotal} packages`);
    if (input.downloadedBytes > 0) {
      parts.push(formatBytes(input.downloadedBytes));
      if (elapsed >= 1) parts.push(`${formatBytes(input.downloadedBytes / elapsed)}/s`);
    }
    if (downloading && input.downloadsDone >= 1 && elapsed >= 1) {
      eta = (elapsed / input.downloadsDone) * (input.downloadsTotal - input.downloadsDone);
    }
  } else {
    parts.push(`${Math.round(input.percent * 100)}%`);
    const phaseElapsed = (input.now - input.phaseStartedAt) / 1000;
    const gained = input.percent - input.phaseStartPercent;
    if (gained > 0.03 && phaseElapsed >= 2 && input.percent < 1) {
      eta = (phaseElapsed / gained) * (1 - input.percent);
    }
  }
  parts.push(`${formatDuration(elapsed)} elapsed`);
  if (eta !== null && Number.isFinite(eta)) parts.push(`about ${formatDuration(eta)} left`);
  return parts.join(' · ');
}
