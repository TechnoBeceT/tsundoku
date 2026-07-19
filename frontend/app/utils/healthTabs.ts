/**
 * healthTabs.ts — the tab model for the `/health` Source Health Console (the
 * 2-tab screen: Library health + Source health). Keeping the keys, the tab list,
 * the sessionStorage key, and the deep-link resolver in ONE place means the page
 * (composition root, which owns the active-tab state + persistence) and the
 * Health shell (presentational, which renders the tab bar) share a single
 * definition instead of each re-declaring the tab keys.
 */
import type { TabItem } from '~/components/ui/nav.types'

/** The two tabs of the Health console. `library` is the default. */
export type HealthTab = 'library' | 'sources'

/**
 * sessionStorage key the page persists the active tab under, so returning to
 * `/health` reopens the last-used tab within the session.
 */
export const HEALTH_TAB_SESSION_KEY = 'tsundoku.health.tab'

/** The ordered tabs rendered by `SegmentedTabs` on the Health console. */
export const HEALTH_TABS: TabItem[] = [
  { key: 'library', label: 'Library' },
  { key: 'sources', label: 'Sources' },
]

/**
 * Accepted `?tab=` deep-link values → the tab they select. The canonical value
 * for the Sources tab is `?tab=sources`; `?tab=metrics` is a documented ALIAS
 * for it (slice-5's proactive-alert badge deep-links straight to the source
 * metrics), so both resolve to the same tab.
 */
const QUERY_TO_TAB: Record<string, HealthTab> = {
  library: 'library',
  sources: 'sources',
  metrics: 'sources',
}

/**
 * resolveInitialHealthTab — pick the tab to open on mount. A valid `?tab=` query
 * wins (so a deep-link always lands on its tab); else the persisted session tab;
 * else the `library` default. Unknown values in either input are ignored.
 */
export function resolveInitialHealthTab(
  queryTab: string | null,
  storedTab: string | null,
): HealthTab {
  if (queryTab != null && QUERY_TO_TAB[queryTab]) return QUERY_TO_TAB[queryTab]
  if (storedTab === 'library' || storedTab === 'sources') return storedTab
  return 'library'
}

/** The reporting windows the Source Metrics tab offers, as SegmentedTabs items. */
export const REPORT_PERIOD_TABS: TabItem[] = [
  { key: '24h', label: '24h' },
  { key: '7d', label: '7d' },
  { key: '30d', label: '30d' },
]

/** sessionStorage key the Source Metrics tab persists its report window under. */
export const HEALTH_REPORT_PERIOD_KEY = 'tsundoku.health.reportPeriod'

/**
 * resolveInitialReportPeriod — the report window to open with: the persisted
 * session value if valid, else the `24h` default. Unknown values are ignored.
 */
export function resolveInitialReportPeriod(stored: string | null): '24h' | '7d' | '30d' {
  return stored === '24h' || stored === '7d' || stored === '30d' ? stored : '24h'
}
