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
  /** Best-source provider key (satisfied-by, or highest-importance). */
  provider: string
  /**
   * Reserved: SSE emits state transitions, not byte progress, so the Active bar
   * is indeterminate today. Populate once a page-level progress event exists.
   */
  progress?: number
  /** Retry attempts so far (failed rows) — drives the "Retry #N" badge. */
  retries?: number
  /** Pre-formatted relative next-attempt, e.g. "in 12m" (failed rows only). */
  nextAttempt?: string
  /** Truncated last error message (failed rows). */
  lastError?: string
  /** Machine error category (failed rows) — mapped to a human label. */
  errorCategory?: ErrorCategory
}
