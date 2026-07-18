/**
 * formatRetryEta — a short, live "time until retry" label computed CLIENT-SIDE
 * from a deferral timestamp, so the Downloads UI counts down without a refetch.
 *
 * The backend sends the raw `deferredUntil` ISO timestamp (never a pre-formatted
 * string); formatting it here means the label re-derives on every tick against the
 * current clock instead of freezing at fetch time.
 *
 * Given an ISO 8601 next-attempt time and the current epoch ms, returns:
 *   "now"  — already elapsed (≤ 0 ms away): the source is due this cycle
 *   "~Ns"  — under a minute away
 *   "~Nm"  — under an hour away
 *   "~Nh"  — an hour or more away
 * The leading "~" marks it an estimate — the engine retries on its own cadence, so
 * this is a guide, not a promise.
 */
export function formatRetryEta(iso: string, nowMs: number = Date.now()): string {
  const diffMs = new Date(iso).getTime() - nowMs
  if (diffMs <= 0) return 'now'
  if (diffMs < 60_000) return `~${Math.round(diffMs / 1_000)}s`
  if (diffMs < 3_600_000) return `~${Math.round(diffMs / 60_000)}m`
  return `~${Math.round(diffMs / 3_600_000)}h`
}
