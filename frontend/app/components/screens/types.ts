/**
 * Shared prop/data types for the full-page screen components.
 *
 * These mirror the backend's read-model DTOs (e.g. `GET /api/series`,
 * `GET /api/categories`) but stay hand-light and presentation-only — the screens
 * are role-agnostic and receive everything via props, so they never import the
 * generated API client. Keep these in this `.ts` (never exported from a `.vue`)
 * so stories and fixtures can import them without the editor TS-server choking on
 * a `.vue` type import.
 */

/** Per-series chapter tallies used to render the progress bar + meta line. */
export interface ChapterCounts {
  /** Total chapters known across all sources. */
  total: number
  /** Chapters present on disk (the numerator of the progress bar). */
  downloaded: number
  /** Chapters wanted but not yet downloaded. */
  wanted: number
  /** Chapters that failed (or permanently failed) to download. */
  failed: number
  /** Downloaded chapters the owner has not read yet — what can be read right
   * now, deliberately not every chapter a source knows about. */
  unread: number
}

/**
 * SeriesSummary — one card in the library grid. Cover-forward: `coverUrl` may be
 * an empty string, in which case the card renders a branded placeholder.
 */
export interface SeriesSummary {
  /** Series UUID. */
  id: string
  /** Canonical display title. */
  title: string
  /** URL-safe slug (library folder name). */
  slug: string
  /** Category NAME this series belongs to (free string — categories are user-defined). */
  category: string
  /** Cover image URL, or "" when no cover is available (→ placeholder). */
  coverUrl: string
  /** Whether the refresh sweep auto-checks this series for new chapters. */
  monitored: boolean
  /** Whether the owner marked this series finished. */
  completed: boolean
  /** Chapter tallies for the progress bar + meta line. */
  chapterCounts: ChapterCounts
  /** When the series entered the library (ISO date-time). Powers the "recently added" sort. */
  createdAt: string
  /** When this series' newest chapter became readable (ISO date-time), or null when
   * no chapter ever carried a first-downloaded time. Powers the "recently updated" sort. */
  lastChapterDownloadedAt: string | null
}

/**
 * CategorySummary — one entry in the category filter bar, with its series count.
 * The list is dynamic (arbitrary length), so the filter is rendered from this
 * array rather than a fixed set.
 */
export interface CategorySummary {
  /** Category NAME (matches `SeriesSummary.category`). */
  category: string
  /** Number of series in this category. */
  count: number
}
