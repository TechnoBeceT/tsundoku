/**
 * Screen types for the live engine source-status strip (GET /api/engine/sources).
 * Kept in a `.ts` (never exported from a `.vue`) so stories, fixtures, and the
 * composable can import them freely.
 *
 * The backend SourceStatus DTO already carries these exact fields, so the mapper
 * in useEngineStatus is a documented 1:1 projection (decoupling the screen from
 * the generated wire type, per the other composables' pattern) — no renames.
 */

/** Whether a source is actively fetching or in an anti-ban cooldown. */
export type SourceStatusState = 'downloading' | 'cooling'

/** One row of the source-status strip. */
export interface SourceStatus {
  /** Canonical physical-source name (the breaker/metrics key). */
  sourceKey: string
  /** "downloading" or "cooling". */
  state: SourceStatusState
  /** How many chapters are being fetched from this source now (0 when cooling). */
  activeCount: number
  /** The current per-source download-concurrency cap (the "N/cap" denominator). */
  cap: number
  /** Seconds until a cooling source's breaker reopens (0 when downloading). */
  cooldownRemainingSeconds: number
  /** Classified failure category of a cooling source (errorclass); "" when downloading. */
  reason: string
  /** The source's current failure streak. */
  consecutiveFailures: number
  /** The source's most recent recorded failure message ("" when none). */
  lastError: string
}
