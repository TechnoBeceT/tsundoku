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
 * `downloaded` is normally hidden (completed chapters live in the per-series
 * library view), but the Failed tab's HONEST FAILURES set (fetched with
 * `include_source_failures=true`) surfaces `downloaded` chapters whose source
 * fetch — most often a failed UPGRADE — keeps failing. So `downloaded` is a
 * legal state HERE, carried by those source-failing rows.
 */
export type DownloadState =
  | 'wanted'
  | 'downloading'
  | 'downloaded'
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
   * The upgrade TARGET's own per-source attempt count (`ProviderChapter.attempts`)
   * against this chapter — so the row can badge the source it is converging TO
   * ("Asura Scans · 2/5"), the one actually being fetched, instead of the satisfier's
   * misleading 0. Pairs with `maxRetries` exactly as `attempts` does, only for the
   * target. 0/undefined when there is no upgrade target; describes the same source
   * `upgradeTarget` names.
   */
  upgradeTargetAttempts?: number
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
   * Why a QUEUED chapter is deferred, classifying the cooldown on the waited-on
   * source: `'backoff'` = that source has a persisted per-source next_attempt_at in
   * the future (a failed fetch's per-chapter backoff); `'cooling_down'` = that
   * source's circuit-breaker is tripped (the WHOLE source is in anti-ban cooldown).
   * Undefined when the chapter is not waiting (backend "" → undefined). Drives the
   * DeferralNote wording ("cooling down, retry …" vs "retrying …").
   */
  waitingReason?: 'backoff' | 'cooling_down'
  /**
   * Per-source download attempts against THIS chapter by the resolved source
   * (`ProviderChapter.attempts` — the engine's per-source retry budget, NOT the
   * legacy top-level `retries`). Paired with `maxRetries` for the "‹source› · N/max"
   * badge. 0 when the resolved source has no feed row (or none is resolved).
   */
  attempts?: number
  /** Current per-source retry budget (jobs.max_retries) — the N/max denominator. 0 when unwired. */
  maxRetries?: number
  /**
   * True when this row is a convergence UPGRADE (state upgrade_available/upgrading)
   * rather than a fresh download. NOT equivalent to "upgradeTarget is non-empty" —
   * a chapter can be an upgrade with no nameable target (the higher source has a
   * feed gap), so `isUpgrade` can be true while `upgradeTarget` is undefined.
   */
  isUpgrade?: boolean
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

  // ---- Honest per-source failure (the Failed tab, include_source_failures) ----
  // These name the source ACTUALLY failing this chapter — which, for a downloaded
  // chapter whose upgrade is broken, is a DIFFERENT source from `provider` (the
  // satisfier). Undefined for every row that has no failing source.
  /** Raw source-ID key of the failing source (distinct from `provider`). */
  failingProvider?: string
  /** Display name of the failing source — drives the "‹source› · N/max" badge + upgrade target. */
  failingProviderName?: string
  /** The failing source's per-source attempt count (ProviderChapter.attempts) — the badge numerator. */
  failingAttempts?: number
  /** The failing source's last per-source error message (shown truncated, full on expand). */
  failingLastError?: string
  /**
   * Coarse classification of `failingLastError` via the shared BACKEND error
   * taxonomy (e.g. "not_found", "no_pages", "rate_limit") — a WIDER set than the
   * frontend `ErrorCategory`, so this stays a raw string mapped to a label with a
   * fallback. Undefined when there is no failing source.
   */
  failingErrorCategory?: string
  /**
   * True when a failing source has budget left (failingAttempts < maxRetries) — a
   * later cycle or an owner retry will try it again. Routes the row to the
   * Retryable sub-tab.
   */
  retryable?: boolean
  /**
   * True when a failing source has spent its whole budget (failingAttempts >=
   * maxRetries) — given up until an owner retry resets it. Routes the row to the
   * Terminal sub-tab.
   */
  terminal?: boolean
}
