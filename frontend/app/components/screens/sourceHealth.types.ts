/**
 * sourceHealth.types.ts — screen-facing types for the Source Health tab (the
 * "Sources" tab of the `/health` console). These mirror the backend
 * `GET /api/sources/metrics` DTOs with the usual mapper renames and were
 * relocated out of `settings.types.ts` when the source-metrics UI moved off the
 * Settings screen into the Source Health tab (Source Health Console, slice 3).
 *
 * The runtime-editable warm-up/circuit-breaker CONFIG knobs (`SourcesSettings`)
 * stay in `settings.types.ts` — those are configuration and live on the Settings
 * screen; these are health/observability and live on the Health console.
 */

/**
 * SourceWarmth — a source's warm/cold session state, derived from how recently
 * it was last warmed (SourceMetricRow computes it from `lastWarmedAt`):
 *   - 'warm'  → warmed within the recency window (the anti-bot session is fresh)
 *   - 'cold'  → warmed, but longer ago than the window (session likely expired)
 *   - 'never' → never warmed (no `lastWarmedAt`)
 */
export type SourceWarmth = 'warm' | 'cold' | 'never'

/**
 * SourceBreaker — a source's anti-ban circuit-breaker state, joined into its
 * metric row (screen-facing mirror of the backend `SourceBreaker` DTO). Present
 * only when the source has a breaker row (null for a healthy source). When
 * `isCoolingDown`, the engine is currently refusing background fetches to this
 * source; the owner can force-clear it with the Reset action.
 */
export interface SourceBreaker {
  /** How many gated fetches failed in a row. */
  consecutiveFailures: number
  /** When the tripped breaker reopens (ISO 8601); null when not tripped. */
  cooldownUntil: string | null
  /** Most recent gated-fetch failure reason ("" when none). */
  lastError: string
  /** Derived — true when a cooldown is set and still in the future. */
  isCoolingDown: boolean
}

/**
 * SourceMetric — one source's search-performance snapshot (the Source Metrics
 * pane). Screen-facing mirror of the backend `SourceMetric` DTO with the usual
 * mapper RENAMES: sourceId → id, sourceName → name, ewmaLatencyMs → avgLatencyMs;
 * the three optional timestamps normalise absent → null (matching the other
 * mappers). `isSlow` is the backend's own derived flag (never measured OR EWMA
 * over the current slow threshold).
 */
export interface SourceMetric {
  /** Suwayomi source id — the row identity/key. */
  id: string
  /** Source display name. */
  name: string
  /** Rolling (EWMA) search latency, in milliseconds. */
  avgLatencyMs: number
  /** Most recent measured search latency, in milliseconds. */
  lastLatencyMs: number
  /** Lifetime number of recorded searches. */
  searchCount: number
  /** Lifetime number of successful searches. */
  successCount: number
  /** Lifetime number of failed/timed-out searches. */
  failCount: number
  /** Most recent failure reason ("" when none). */
  lastError: string
  /** When the most recent failure occurred (null if never failed). */
  lastErrorAt: string | null
  /** When the most recent success occurred (null if never succeeded). */
  lastSuccessAt: string | null
  /** When the source was last warmed (null if never warmed). */
  lastWarmedAt: string | null
  /** When this snapshot was last written. */
  updatedAt: string
  /** Derived — true when never measured OR EWMA over the slow threshold. */
  isSlow: boolean
  /** Anti-ban circuit-breaker state — null when the source has no breaker row. */
  breaker: SourceBreaker | null
}
