/**
 * Countdown / duration formatting helpers for the Downloads engine-visibility
 * header. Pure functions (no Vue), so they are trivially unit-testable and reused
 * by both the CycleTimers header and the SourceStatusRow strip.
 */

/**
 * Format a remaining duration (milliseconds) as a live countdown clock:
 *   - ≥ 1 hour → "H:MM:SS"  (e.g. 1:52:08 — the next-refresh countdown)
 *   - < 1 hour → "M:SS"     (e.g. 0:43    — the next-download countdown)
 *
 * Negative inputs clamp to 0 ("0:00") so an over-run timer never shows a
 * wrong-looking negative value while the client waits for the next SSE tick.
 */
export function formatCountdown(ms: number): string {
  const totalSeconds = Math.max(0, Math.floor(ms / 1000))
  const hours = Math.floor(totalSeconds / 3600)
  const minutes = Math.floor((totalSeconds % 3600) / 60)
  const seconds = totalSeconds % 60
  const ss = String(seconds).padStart(2, '0')
  if (hours > 0) {
    const mm = String(minutes).padStart(2, '0')
    return `${hours}:${mm}:${ss}`
  }
  return `${minutes}:${ss}`
}

/**
 * Format a whole-number seconds remaining as a COMPACT label for the source
 * strip's cooling badge: "12m", "4m", "45s", "2h". Picks the largest whole unit
 * (hours → minutes → seconds). Negative/zero clamps to "0s".
 */
export function formatCompactDuration(seconds: number): string {
  const s = Math.max(0, Math.floor(seconds))
  if (s >= 3600) return `${Math.floor(s / 3600)}h`
  if (s >= 60) return `${Math.floor(s / 60)}m`
  return `${s}s`
}
