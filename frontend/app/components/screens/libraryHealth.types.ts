/**
 * Prop/data types for the Library Health screen (`GET /api/health`).
 *
 * Health is the "what needs attention" view: the backend response only carries
 * series that have at least one `stale`, `erroring`, or `unavailable` source
 * (completed series are treated as healthy and never appear). Each entry lists
 * ONLY that series' unhealthy sources — reusing the per-source `Provider` shape
 * from the Series Detail screen so the badge/sync/error fields render identically.
 *
 * Kept in this `.ts` (never exported from a `.vue`) so stories and fixtures can
 * import it freely; the screen stays presentation-only and never touches the
 * generated API client.
 */
import type { Provider } from './seriesDetail.types'

/**
 * SeriesHealth — one sick series in the health report. `sources` holds only the
 * unhealthy `Provider` rows (the backend filters out the healthy ones), so the
 * screen renders every source it is given.
 */
export interface SeriesHealth {
  /** Series UUID (the row links to its detail view). */
  id: string
  /** Series display title. */
  title: string
  /** URL-safe slug (library folder name). */
  slug: string
  /** This series' unhealthy sources (same shape as in Series Detail). */
  sources: Provider[]
}
