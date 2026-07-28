/**
 * Prop/data types for the library-wide Duplicates screen
 * (`GET /api/library/duplicate-files`), the third tab of the Cleanup console.
 *
 * The Duplicates tab is DISCOVERY ONLY: it answers "which series are wasting disk
 * on leftover CBZs", because the removal itself already exists as the per-series
 * "Remove duplicate files" action and there is deliberately no library-wide
 * execute path. Every row therefore links to its series rather than offering a
 * delete of its own.
 *
 * Kept in this `.ts` (never exported from a `.vue`) so stories and fixtures can
 * import it freely; the screen stays presentation-only and never touches the
 * generated API client.
 */

/**
 * SeriesDuplicateFiles — one row of the Duplicates tab.
 */
export interface SeriesDuplicateFiles {
  /** Series UUID — the row links to its detail view, where the removal lives. */
  seriesId: string
  /** Canonical series title. */
  title: string
  /** Resolved display title (falls back to the canonical title). */
  displayName: string
  /** Category name. */
  category: string
  /** Series cover proxy path, or "" when no cover is available. */
  coverUrl: string
  /** How many duplicate CBZ files are removable for this series. */
  fileCount: number
  /** Total on-disk size of those files, in bytes. */
  reclaimableBytes: number
}
