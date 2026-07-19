/**
 * timeFormat.ts — the shared time/number formatters for the Source Health
 * Console's event log. Relative + absolute timestamps and a duration formatter
 * are needed all over the report (event rows, recent errors, the forensic modal,
 * source rows), so they live here once (§2 DRY) instead of the ad-hoc `rel()` /
 * `fmtLatency()` each row used to hand-roll.
 *
 * All functions are PURE and take an explicit `nowMs` so a live label re-derives
 * against the shared clock (useNow) each tick and a test is deterministic.
 */

const MINUTE = 60_000
const HOUR = 3_600_000
const DAY = 86_400_000

/**
 * relativeTime — a short "how long ago" label for a past timestamp.
 *   null / ""   → "never"
 *   ≤ 45s ago   → "just now"
 *   < 1h        → "Nm ago"
 *   < 1d        → "Nh ago"
 *   < 30d       → "Nd ago"
 *   otherwise   → the absolute date (older than the reporting window)
 * A future timestamp reads "just now" (clock skew) rather than a negative label.
 */
export function relativeTime(iso: string | null, nowMs: number = Date.now()): string {
  if (iso == null || iso === '') return 'never'
  const then = Date.parse(iso)
  if (Number.isNaN(then)) return 'never'
  const diff = nowMs - then
  if (diff < 45_000) return 'just now'
  if (diff < HOUR) return `${Math.floor(diff / MINUTE)}m ago`
  if (diff < DAY) return `${Math.floor(diff / HOUR)}h ago`
  if (diff < 30 * DAY) return `${Math.floor(diff / DAY)}d ago`
  return absoluteTime(iso)
}

/**
 * absoluteTime — the full local date+time for a timestamp, for the forensic modal
 * and row tooltips (the precise "at 14:32" answer). Returns "—" for an
 * absent/unparseable value.
 */
export function absoluteTime(iso: string | null): string {
  if (iso == null || iso === '') return '—'
  const ms = Date.parse(iso)
  if (Number.isNaN(ms)) return '—'
  return new Date(ms).toLocaleString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
}

/**
 * formatDurationMs — a compact latency/duration label:
 *   ≤ 0      → "—" (not timed / unmeasured)
 *   < 1000ms → "320ms"
 *   < 60s    → "1.2s"
 *   ≥ 60s    → "1m 5s"
 */
export function formatDurationMs(ms: number): string {
  if (ms <= 0) return '—'
  if (ms < 1000) return `${Math.round(ms)}ms`
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)}s`
  const s = Math.round(ms / 1000)
  return `${Math.floor(s / 60)}m ${s % 60}s`
}
