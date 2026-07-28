/**
 * cleanupTabs.ts — the tab model for the `/cleanup` console (the 3-tab screen:
 * Fractionals + Sourceless + Duplicates). Keeping the keys, the tab list, the
 * sessionStorage key, and the deep-link resolver in ONE place means the page
 * (composition root, which owns the active-tab state + persistence) and the
 * Cleanup shell (presentational, which renders the tab bar) share a single
 * definition instead of each re-declaring the tab keys.
 *
 * Mirrors utils/healthTabs.ts — same shape, same precedence, so there is one
 * tab-console pattern in the app rather than two.
 */
import type { TabItem } from '~/components/ui/nav.types'

/** The three tabs of the Cleanup console. `fractionals` is the default. */
export type CleanupTab = 'fractionals' | 'sourceless' | 'duplicates'

/**
 * sessionStorage key the page persists the active tab under, so returning to
 * `/cleanup` reopens the last-used tab within the session.
 */
export const CLEANUP_TAB_SESSION_KEY = 'tsundoku.cleanup.tab'

/** The ordered tabs rendered by `SegmentedTabs` on the Cleanup console. */
export const CLEANUP_TABS: TabItem[] = [
  { key: 'fractionals', label: 'Fractionals' },
  { key: 'sourceless', label: 'Sourceless' },
  { key: 'duplicates', label: 'Duplicates' },
]

/** Accepted `?tab=` deep-link values → the tab they select. */
const QUERY_TO_TAB: Record<string, CleanupTab> = {
  fractionals: 'fractionals',
  sourceless: 'sourceless',
  duplicates: 'duplicates',
}

/**
 * resolveInitialCleanupTab — pick the tab to open on mount. A valid `?tab=` query
 * wins (so a deep-link always lands on its tab); else the persisted session tab;
 * else the `fractionals` default. Unknown values in either input are ignored.
 *
 * The query-wins rule is load-bearing for the two REDIRECTED legacy routes:
 * `/fractionals` and `/sourceless` now redirect here carrying their tab in the
 * query, so an old bookmark must beat whatever tab the session last stored.
 */
export function resolveInitialCleanupTab(
  queryTab: string | null,
  storedTab: string | null,
): CleanupTab {
  if (queryTab != null && QUERY_TO_TAB[queryTab]) return QUERY_TO_TAB[queryTab]
  if (storedTab != null && QUERY_TO_TAB[storedTab]) return QUERY_TO_TAB[storedTab]
  return 'fractionals'
}
