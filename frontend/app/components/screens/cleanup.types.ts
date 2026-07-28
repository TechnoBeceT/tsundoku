/**
 * Prop/data types for the `/cleanup` console — the 3-tab screen that folds the
 * library-wide cleanup surfaces (Fractionals · Sourceless · Duplicates) into one
 * page instead of three sidebar entries.
 *
 * The three tab BODIES keep their own types (`fractionals.types`,
 * `sourceless.types`, `duplicates.types`); this file carries only what the SHELL
 * itself needs — the per-tab data bundles it forwards. Grouping each tab's props
 * into one object keeps the shell's surface readable as the console grows, and
 * makes the lazy contract explicit: a tab that has never been revealed simply
 * carries its empty/initial bundle.
 *
 * Kept in this `.ts` (never exported from a `.vue`) so stories and fixtures can
 * import it freely.
 */
import type { SeriesFractionals } from './fractionals.types'
import type { SeriesSourceless } from './sourceless.types'
import type { SeriesDuplicateFiles } from './duplicates.types'

/** Everything the Fractionals tab body needs. */
export interface CleanupFractionalsPane {
  /** The series with downloaded fractional chapters; empty → all-clear state. */
  series: SeriesFractionals[]
  /** Initial load in flight → skeleton cards. */
  loading: boolean
  /** Manual rescan in flight → the rescan button spins. */
  refreshing: boolean
  /** A load failure, shown as a banner above the tab (§16). */
  error: string | null
  /** Series ids whose ignore-policy toggle is in flight. */
  busyIds: string[]
  /** A toggle failure, shown as a banner above the tab (§16). */
  toggleError: string | null
}

/** Everything the Sourceless tab body needs. */
export interface CleanupSourcelessPane {
  /** The series with downloaded sourceless chapters; empty → all-clear state. */
  series: SeriesSourceless[]
  /** Initial load in flight → skeleton cards. */
  loading: boolean
  /** Manual rescan in flight → the rescan button spins. */
  refreshing: boolean
  /** A load failure, shown as a banner above the tab (§16). */
  error: string | null
}

/** Everything the Duplicates tab body needs. */
export interface CleanupDuplicatesPane {
  /** The series with removable duplicate files; empty → all-clear state. */
  series: SeriesDuplicateFiles[]
  /** Total removable files across the library (header figure). */
  totalFiles: number
  /** Total reclaimable bytes across the library (header figure). */
  totalBytes: number
  /** Initial load in flight → skeleton cards. */
  loading: boolean
  /** Manual rescan in flight → the rescan button spins. */
  refreshing: boolean
  /** A load failure, shown as a banner above the tab (§16). */
  error: string | null
}
