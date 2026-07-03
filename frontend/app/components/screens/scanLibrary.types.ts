/**
 * Screen-only view types for the Scan Library wizard (the migrate-an-existing
 * on-disk library into Tsundoku flow — see the Task 6 design brief).
 *
 * Unlike the other screens' `*.types.ts` files, this one does NOT redefine its
 * read-model: the scan lifecycle + staged-entry shapes are owned by the data
 * layer (`~/composables/useScanLibrary`), whose doc comments are the single
 * source of truth for what each field means and where it comes from on the
 * wire (§2 DRY — re-declaring them here would drift the moment the composable
 * changes). This file only adds the types the SCREEN itself needs that the
 * composable has no reason to know about.
 */
import type { BatchImportFailure } from '~/composables/useScanLibrary'

export type {
  ScanEntry,
  ScanState,
  ScanStatusFilter,
  BatchImportFailure,
} from '~/composables/useScanLibrary'

/**
 * BatchImportResult — the summary of one `importAllDiskOnly()` run, surfaced
 * to the owner once the drain-then-chunk batch finishes (§16 — the bulk
 * action's outcome must be visible, not just its in-flight state). The
 * composable returns this shape inline (it isn't a named export there), so it
 * gets a name here for the screen's props to reference.
 */
export interface BatchImportResult {
  /** How many staged entries were imported disk-only. */
  imported: number
  /** Per-path failures within the batch (empty when everything succeeded). */
  failed: BatchImportFailure[]
}
