<script setup lang="ts">
/**
 * Health page — route "/health". The composition root for the 2-tab Health
 * console (Library health + Source health). It owns the active-tab state and
 * wires each tab's composable; the presentational Health screen renders the
 * tabs and forwards actions.
 *
 * Tab state:
 *   - Resolved on mount from the `?tab=` deep-link, else the persisted session
 *     tab, else the `library` default (resolveInitialHealthTab). The canonical
 *     Sources deep-link is `?tab=sources`; `?tab=metrics` is an accepted alias
 *     (slice-5's alert badge jumps straight to the source metrics).
 *   - Persisted to sessionStorage on every change so returning to /health
 *     reopens the last-used tab within the session.
 *
 * Lazy data:
 *   - Library tab (the default) → useHealth() loads on mount.
 *   - Sources tab → useSourceMetrics({ immediate: false }) is created deferred
 *     and only fetched the FIRST time the Sources tab is shown (a watcher on the
 *     active tab), so Tab-2 data never loads for a visitor who only ever looks at
 *     the Library tab.
 *
 * useHealth / useSourceMetrics are auto-imported from app/composables/.
 */
import {
  HEALTH_TAB_SESSION_KEY,
  HEALTH_REPORT_PERIOD_KEY,
  resolveInitialHealthTab,
  resolveInitialReportPeriod,
  type HealthTab,
} from '~/utils/healthTabs'

// ── Library tab (loads on mount — it is the default view) ──────────────────────
const { series, pending: healthPending, refreshing, error: healthError, refresh } = useHealth()

// ── Sources tab (LAZY — created deferred, fetched on first reveal) ─────────────
const {
  metrics,
  pending: sourcePending,
  error: sourceError,
  warming,
  warmMessage,
  warmError,
  resetting,
  resetError,
  refetch: refetchMetrics,
  warmNow,
  resetBreaker,
} = useSourceMetrics({ immediate: false })

// ── Kaizoku-grade Source Metrics report (LAZY — one reactive bundle) ───────────
// Restore the last-used reporting window before the first fetch so it loads at
// that period, not the default. `metrics` joins into the accordion for the
// superset badges + Reset (by canonical source key).
const storedPeriod = import.meta.client ? sessionStorage.getItem(HEALTH_REPORT_PERIOD_KEY) : null
const report = useSourceHealthReport({
  metrics,
  immediate: false,
  initialPeriod: resolveInitialReportPeriod(storedPeriod),
})

// Persist the reporting window so it survives navigating away + back.
watch(() => report.period, (p) => {
  if (import.meta.client) sessionStorage.setItem(HEALTH_REPORT_PERIOD_KEY, p)
})

// ── Active tab: ?tab= deep-link → sessionStorage → default 'library' ───────────
const route = useRoute()
const queryTab = typeof route.query.tab === 'string' ? route.query.tab : null
const storedTab = import.meta.client ? sessionStorage.getItem(HEALTH_TAB_SESSION_KEY) : null
const activeTab = ref<HealthTab>(resolveInitialHealthTab(queryTab, storedTab))

/** Update + persist the active tab (called by @set-tab from the Health shell). */
function setTab(tab: HealthTab): void {
  activeTab.value = tab
}

// Persist every change so the tab survives navigating away and back.
watch(activeTab, (tab) => {
  if (import.meta.client) sessionStorage.setItem(HEALTH_TAB_SESSION_KEY, tab)
})

// Lazy-load the Sources tab's data exactly once, the first time it is shown
// (fires immediately if the resolved initial tab is already 'sources'): the
// search-metrics snapshot AND the Kaizoku-grade report (overview + event log).
let sourcesLoaded = false
watch(activeTab, (tab) => {
  if (tab === 'sources' && !sourcesLoaded) {
    sourcesLoaded = true
    void refetchMetrics()
    report.load()
  }
}, { immediate: true })
</script>

<template>
  <div class="page-health">
    <Health
      :active-tab="activeTab"
      :series="series"
      :health-loading="healthPending"
      :refreshing="refreshing"
      :health-error="healthError"
      :report="report"
      :metrics="metrics"
      :source-pending="sourcePending"
      :source-error="sourceError"
      :warming="warming"
      :warm-message="warmMessage"
      :warm-error="warmError"
      :resetting="resetting"
      :reset-error="resetError"
      @set-tab="setTab"
      @open-series="(id: string) => navigateTo(`/series/${id}`)"
      @refresh="refresh"
      @warm-now="warmNow"
      @reset-breaker="resetBreaker"
    />
  </div>
</template>

<style scoped>
.page-health {
  min-height: 100%;
}
</style>
