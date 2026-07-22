/**
 * Prop/data types for the library-wide Sourceless screen
 * (`GET /api/library/sourceless`).
 *
 * The Sourceless page is the cleanup surface for CBZs left behind when every
 * source that once supplied a downloaded chapter is removed — the chapter row
 * survives (never-auto-delete, Rule 2) but no remaining source can satisfy its
 * `chapter_key`. Unlike Fractionals there is no ignore-policy toggle here: a
 * sourceless chapter has no carrier to flag, so each row is just the count and
 * the per-series cleanup dialog does the rest.
 *
 * Kept in this `.ts` (never exported from a `.vue`) so stories and fixtures can
 * import it freely; the screen stays presentation-only and never touches the
 * generated API client.
 */

/**
 * SeriesSourceless — one row of the Sourceless page.
 */
export interface SeriesSourceless {
  /** Series UUID — the row links to its detail view and is the cleanup target. */
  seriesId: string
  /** Canonical series title. */
  title: string
  /** Resolved display title (falls back to the canonical title). */
  displayName: string
  /** Category name. */
  category: string
  /** Series cover proxy path, or "" when no cover is available. */
  coverUrl: string
  /** How many downloaded sourceless chapters this series has. */
  sourcelessCount: number
}

/** One removable sourceless chapter in the per-series cleanup dialog. */
export interface SourcelessCleanupChapter {
  /** Chapter UUID — the id the cleanup POST names. */
  chapterId: string
  /** The chapter number, or null when a sourceless chapter lacks a parsed number. */
  number: number | null
  /** How many pages the downloaded file has, or null when never recorded. */
  pageCount: number | null
  /** Display label of the former satisfying source; "" once that source is gone. */
  provider: string
  /** The CBZ filename that will be deleted. */
  filename: string
}

/** The per-series sourceless-cleanup preview. */
export interface SourcelessCleanupPreview {
  /** The removable sourceless chapters; empty when there is nothing to clean. */
  chapters: SourcelessCleanupChapter[]
}
