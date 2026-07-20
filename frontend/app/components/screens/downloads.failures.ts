/**
 * Pure failure-classification helpers for the Downloads screen's Failed tab.
 *
 * Extracted from Downloads.vue (mirroring the ReaderStrip.logic.ts pattern) so the
 * routing that decides which sub-tab a row lands in — and which rows count as
 * failures at all — is unit-testable in isolation.
 *
 * THE BUG THIS FIXES: failed *upgrades* silently revert the chapter to
 * `downloaded`, so a state-only view (`state IN failed,permanently_failed`) left
 * the Retryable sub-tab empty while the engine failed constantly. With the backend
 * `include_source_failures=true` widening, those downloaded-but-failing chapters
 * now arrive carrying `failingProviderName` / `retryable` / `terminal`; these
 * predicates route them by the FAILING SOURCE's budget, not by chapter state.
 */
import type { DownloadItem } from './downloads.types'

/** A source is failing this chapter when the backend named one (attempts > 0). */
export function hasFailingSource(i: DownloadItem): boolean {
  return (i.failingProviderName ?? '') !== ''
}

/**
 * A row belongs to the Failed tab when it is in a failed chapter state OR it
 * carries a chapter-specific failing source (a downloaded broken-upgrade row).
 */
export function isFailureRow(i: DownloadItem): boolean {
  return (
    i.state === 'failed'
    || i.state === 'permanently_failed'
    || i.retryable === true
    || i.terminal === true
    || hasFailingSource(i)
  )
}

/**
 * Terminal = the failing source has exhausted its budget (or the chapter itself is
 * permanently_failed). Checked first so a row is never counted in both buckets.
 */
export function isTerminalFailure(i: DownloadItem): boolean {
  return i.terminal === true || i.state === 'permanently_failed'
}

/**
 * Retryable = a failing source (or a failed-state chapter) that is NOT terminal —
 * a later cycle or an owner retry will try it again. The `hasFailingSource`
 * fallback keeps a source-failing row that lacks explicit flags on the retryable
 * side rather than dropping it.
 */
export function isRetryableFailure(i: DownloadItem): boolean {
  if (isTerminalFailure(i)) return false
  return i.retryable === true || i.state === 'failed' || hasFailingSource(i)
}

/** Sub-tab predicates for the Failed tab, keyed by the sub-tab id. */
export type FailSubTab = 'all' | 'retryable' | 'terminal'

/** The predicate a Failed sub-tab filters its rows by. */
export function failSubTabPredicate(tab: FailSubTab): (i: DownloadItem) => boolean {
  if (tab === 'retryable') return isRetryableFailure
  if (tab === 'terminal') return isTerminalFailure
  return isFailureRow
}
