/**
 * Format a byte count into a human-readable string.
 * @example formatBytes(0) → "0 B"
 * @example formatBytes(1536) → "1.5 KB"
 * @example formatBytes(5368709120) → "5.0 GB"
 */
export function formatBytes(bytes: number): string {
  if (bytes <= 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  const value = bytes / Math.pow(1024, i);
  return `${value.toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}

/**
 * Format a duration in seconds into a compact human-readable string.
 * @example formatDuration(0) → "0s"
 * @example formatDuration(150) → "2m 30s"
 * @example formatDuration(3900) → "1h 5m"
 */
export function formatDuration(seconds: number): string {
  if (seconds < 0) return '0s';
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = Math.floor(seconds % 60);
  if (h > 0) return `${h}h ${m}m`;
  if (m > 0) return `${m}m ${s}s`;
  return `${s}s`;
}

/**
 * Format a speed in bytes-per-second into a human-readable string.
 * @example formatSpeed(0) → "0 B/s"
 * @example formatSpeed(47447941) → "45.3 MB/s"
 */
export function formatSpeed(bps: number): string {
  if (bps <= 0) return '0 B/s';
  return `${formatBytes(bps)}/s`;
}

/**
 * Format an ISO timestamp into a relative time string.
 * @example formatRelativeTime("2026-06-28T12:00:00Z") → "5 minutes ago"
 */
export function formatRelativeTime(iso: string): string {
  const now = Date.now();
  const then = new Date(iso).getTime();
  const diff = Math.floor((now - then) / 1000);
  if (diff < 60) return 'just now';
  if (diff < 3600) return `${Math.floor(diff / 60)} minutes ago`;
  if (diff < 86400) return `${Math.floor(diff / 3600)} hours ago`;
  return `${Math.floor(diff / 86400)} days ago`;
}

/**
 * Format two ISO timestamps into a duration string.
 * @example formatDurationBetween(start, end) → "2m 30s"
 */
export function formatDurationBetween(startISO: string, endISO: string): string {
  const start = new Date(startISO).getTime();
  const end = new Date(endISO).getTime();
  return formatDuration(Math.floor((end - start) / 1000));
}

/**
 * Format an ISO timestamp into a short date-time string.
 * @example formatDateTime("2026-06-28T12:00:00Z") → "Jun 28, 2026 12:00"
 */
export function formatDateTime(iso: string): string {
  const d = new Date(iso);
  return d.toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' }) +
    ' ' +
    d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false });
}
