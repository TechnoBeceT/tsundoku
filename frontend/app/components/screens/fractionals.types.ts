/**
 * Prop/data types for the library-wide Fractionals screen
 * (`GET /api/library/fractionals`).
 *
 * The Fractionals page is the retroactive bulk-fix surface: every series that has
 * downloaded fractional chapters, so the owner can set the ignore-fractional
 * policy and clean the leftover files from ONE place instead of hunting
 * series-by-series. A series appears REGARDLESS of its ignore state (the pain is
 * OLD un-flagged series), which is why each row carries TWO counts and the
 * whole-series toggle state.
 *
 * Kept in this `.ts` (never exported from a `.vue`) so stories and fixtures can
 * import it freely; the screen stays presentation-only and never touches the
 * generated API client.
 */

/**
 * SeriesFractionals — one row of the Fractionals page.
 *
 * 🔴 The two counts are DISTINCT and must never be conflated:
 *   - `fractionalCount` is EVERY downloaded fractional (ignore-agnostic) — the
 *     reason the series is listed, including old series whose sources were never
 *     flagged.
 *   - `removableCount` is how many of those are removable RIGHT NOW under the
 *     strict resurrection-safe rule. It is 0 until the owner sets policy, then
 *     rises once every carrier ignores fractionals.
 *
 * The provider fields drive the inline whole-series ignore toggle:
 * `allProvidersIgnoring` is its resolved ON state (the backend computes it — the
 * screen renders the answer, never re-derives it), and providersTotal /
 * providersIgnoring give the "N of M sources ignoring" caption.
 */
export interface SeriesFractionals {
  /** Series UUID — the row links to its detail view and is the toggle/clean target. */
  seriesId: string
  /** Canonical series title. */
  title: string
  /** Resolved display title (falls back to the canonical title). */
  displayName: string
  /** Category name. */
  category: string
  /** Series cover proxy path, or "" when no cover is available. */
  coverUrl: string
  /** All downloaded fractional chapters (ignore-agnostic). */
  fractionalCount: number
  /** How many downloaded fractionals are removable right now. */
  removableCount: number
  /** Number of sources the series has. */
  providersTotal: number
  /** How many of those sources currently ignore fractionals. */
  providersIgnoring: number
  /** True when every source ignores fractionals (the toggle's ON state). */
  allProvidersIgnoring: boolean
}
