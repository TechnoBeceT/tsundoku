/**
 * dedupSweepSummary — the pure kernel that turns the backend's terminal
 * `library.dedup.done` SSE payload into the lines the Settings dialog shows.
 *
 * `POST /api/library/dedup-providers` answers a bare `202 {started:true}` and
 * runs the sweep DETACHED, so its response can never carry the result. The
 * outcome arrives later on this event — it is the only channel there is, which
 * is why the parsing lives in a tested unit rather than inline in a component.
 *
 * The sweep reports three independent counts and they must not be conflated:
 *   - `seriesProcessed` — series whose dedup ran to completion.
 *   - `skipped` — duplicate PAIRS left alone because the surviving source has no
 *     chapter feed yet; the owner fixes that by refreshing that source.
 *   - `busy` — SERIES skipped because another merge (a match, a consolidation,
 *     or the automatic self-heal) held them at that moment. Nothing was touched,
 *     and simply running the clean-up again catches them. This one is surfaced
 *     separately and actionably rather than folded into `skipped`.
 *
 * Everything is coerced at this boundary: the SSE payload is `unknown`, so a
 * missing or wrongly-typed field degrades to 0 / null instead of rendering
 * "undefined" at the owner.
 */

/** The parsed, render-ready outcome of one library-wide dedup sweep. */
export interface DedupSweepSummary {
  /** The success line, or null when the sweep reported a failure. */
  message: string | null
  /** The failure line the backend supplied, or null on success. */
  error: string | null
  /** How many series were skipped because a merge was already running on them. */
  busy: number
}

/** Reads a number off an untrusted payload, defaulting to 0. */
function count(source: Record<string, unknown>, key: string): number {
  const value = source[key]
  return typeof value === 'number' && Number.isFinite(value) ? value : 0
}

/**
 * "" for one, "s" for any other count — the same inline pluralisation the
 * per-series dedup message in useSeriesDetail uses. Note "series" is invariant
 * and is therefore never run through this.
 */
function s(n: number): string {
  return n === 1 ? '' : 's'
}

/**
 * Parses a `library.dedup.done` payload into the dialog's §16 outcome lines.
 *
 * A payload carrying `error` is a failure: the backend sends a fixed,
 * caller-safe sentence there (never raw server text), so it is shown as-is. On
 * success the message names what actually changed — how many duplicate sources
 * were folded, across how many series, plus the empty-feed skips when there were
 * any. The busy count is returned as a NUMBER, not folded into the message, so
 * the dialog can render it as its own actionable line.
 */
export function readDedupSweepEvent(data: unknown): DedupSweepSummary {
  const payload = (typeof data === 'object' && data !== null ? data : {}) as Record<string, unknown>
  const busy = count(payload, 'busy')

  const failure = payload.error
  if (typeof failure === 'string' && failure !== '') {
    return { message: null, error: failure, busy }
  }

  const merged = count(payload, 'merged')
  const seriesProcessed = count(payload, 'seriesProcessed')
  const skipped = count(payload, 'skipped')

  let message = merged === 0
    ? `Clean-up finished — no duplicate sources found across ${seriesProcessed} series`
    : `Clean-up finished — merged ${merged} duplicate source${s(merged)} across ${seriesProcessed} series`
  if (skipped > 0) {
    message += `; ${skipped} left alone until the surviving source has fetched its chapters`
  }

  return { message, error: null, busy }
}

/**
 * The actionable line for a sweep that had to skip series because a merge was
 * already running on them. Returns null when nothing was busy, so the dialog
 * simply renders nothing.
 */
export function busyNotice(busy: number): string | null {
  if (busy <= 0) return null
  const was = busy === 1 ? 'was' : 'were'
  return `${busy} series ${was} busy and ${was} skipped — run the clean-up again to catch ${busy === 1 ? 'it' : 'them'}.`
}
