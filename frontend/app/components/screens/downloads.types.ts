/**
 * Prop/data types for the Downloads screen.
 *
 * These mirror the PLANNED backend read-model (`GET /api/downloads` — see the
 * Downloads design brief §2) but stay presentation-only: the screen receives a
 * flat cross-library list of chapter-activity items and derives the per-tab
 * views itself. Kept in this `.ts` (never exported from a `.vue`) so stories and
 * fixtures can import them freely.
 */

/**
 * DownloadState — the subset of `Chapter.state` the Downloads screen surfaces.
 * `downloaded` is intentionally absent (completed chapters live in the per-series
 * library view, not here).
 */
export type DownloadState =
  | 'wanted'
  | 'downloading'
  | 'upgrading'
  | 'upgrade_available'
  | 'failed'
  | 'permanently_failed'

/** The error category a failed chapter carries (drives the human-readable label). */
export type ErrorCategory = 'network' | 'source' | 'cloudflare' | 'timeout' | 'parse'

/** The three top-level tabs — each a filtered view over chapters by state. */
export type DownloadTab = 'active' | 'failed' | 'queued'

/** The bulk-retry scope an emitted `retry-all` carries. */
export type RetryAllState = 'failed' | 'permanently_failed'

/**
 * DownloadItem — one chapter-activity row, enriched with its series context.
 * One flat shape covers all three tabs; the contextual fields (`retries`,
 * `lastError`, …) are only populated for `failed`/`permanently_failed` rows.
 */
export interface DownloadItem {
  /** Chapter UUID (the retry target). */
  chapterId: string
  /** Owning series UUID (the row links here). */
  seriesId: string
  /** Series display title (primary text). */
  seriesTitle: string
  /** Series category NAME (rendered as a subtle tag). */
  seriesCategory: string
  /** Series cover URL, or "" → branded placeholder. */
  coverUrl: string
  /** Chapter number for display/sort, or null when unknown. */
  number: number | null
  /** Resolved chapter name (falls back to "Chapter {number}" upstream). */
  name: string
  /** Which state bucket this chapter is in. */
  state: DownloadState
  /** Best-source raw source-ID key (satisfied-by, or highest-importance). */
  provider: string
  /** Human-readable display name of that source; falls back to the id upstream. Shown in place of the id. */
  providerName: string
  /**
   * Display name of the source this chapter is upgrading TO (`upgrade_available`
   * / `upgrading` rows only) — the row then reads "MangaDex → Asura Scans", so a
   * convergence wave shows where each chapter is HEADED, not just where its
   * current file came from. Undefined for every other state.
   */
  upgradeTarget?: string
  /**
   * Why a QUEUED chapter is not moving: the source the engine is waiting on (the
   * `upgradeTarget` for an upgrading chapter, else the primary source =
   * `providerName` for a wanted one) is under a persisted cooldown, and this is its
   * next-attempt time (raw ISO 8601). Set ONLY when genuinely in the future — a
   * ready source leaves it undefined. Raw (not pre-formatted) so the row's "retry
   * ~Nm" counts down live. Undefined for a row that is not deferred.
   */
  deferredUntil?: string
  /** The waited-on source's last error, shown as the deferral tooltip. Undefined when none. */
  deferReason?: string
  /**
   * Live download percentage (0–100), driven by the `download.progress` SSE
   * event (round(100 * pagesCurrent / pagesTotal)). Undefined before the first
   * event → the Active bar stays indeterminate until pages start arriving.
   */
  progress?: number
  /** Pages fetched so far — powers the "12 / 40" counter (set with `progress`). */
  pagesCurrent?: number
  /** Total pages in the chapter — the counter's denominator (set with `progress`). */
  pagesTotal?: number
  /** Retry attempts so far (failed rows) — drives the "Retry #N" badge. */
  retries?: number
  /** Pre-formatted relative next-attempt, e.g. "in 12m" (failed rows only). */
  nextAttempt?: string
  /** Truncated last error message (failed rows). */
  lastError?: string
  /** Machine error category (failed rows) — mapped to a human label. */
  errorCategory?: ErrorCategory
}
