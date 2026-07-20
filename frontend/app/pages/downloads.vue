<script setup lang="ts">
/**
 * Downloads page — route "/downloads".
 *
 * Delegates all data fetching, mutation state, and §16 error handling to
 * useDownloads(). Downloads is auto-imported from app/components/.
 * navigateTo is a Nuxt auto-import.
 *
 * Prop wiring:
 *   :items            — flat DownloadItem[] (screen derives tab views from it)
 *   :active-tab       — which top-level tab is active
 *   :cycle-active     — true while a download cycle is running (SSE-driven)
 *   :next-cycle-minutes — null (no countdown available from SSE)
 *   :download-running / :refresh-running — SSE running flags for the CycleTimers header
 *   :download-remaining-ms / :refresh-remaining-ms — live countdowns (useCycleTimers)
 *   :source-statuses  — the live per-source status strip (useEngineStatus, polled)
 *   :retrying-ids     — chapter ids with a single retry in flight
 *   :retrying-all     — bulk retry scope in flight, or null
 *   :retry-error      — surfaced retry/load failure (dismissible banner)
 *   :loading          — true during the initial fetch
 *   :counts           — server badge counts { active, queued, allFailures }
 *   :total            — server total for the active tab (load-more affordance)
 *   :has-more         — whether more pages exist for the active tab
 *   :loading-more     — whether a load-more fetch is in flight
 *   :running          — "Download now" trigger in flight
 *   :run-message      — "Download now" success note
 *   :run-error        — "Download now" failure message
 *
 * Emit wiring:
 *   @set-tab        → setTab(tab)
 *   @retry          → retry(chapterId)
 *   @retry-all      → retryAll(state)
 *   @open-series    → navigateTo('/series/' + id)
 *   @dismiss-error  → dismissError()
 *   @load-more      → loadMore()
 *   @run-now        → runNow()
 */
const {
  items,
  activeTab,
  loading,
  total,
  hasMore,
  loadingMore,
  counts,
  retryingIds,
  retryingAll,
  retryError,
  cycleActive,
  nextCycleMinutes,
  running,
  runMessage,
  runError,
  setTab,
  loadMore,
  retry,
  retryAll,
  runNow,
  dismissError,
} = useDownloads()

// Live count of sources whose circuit-breaker is tripped (anti-ban cooldown) — feeds
// the Active-tab "M sources cooling down" awareness banner so an empty Active list
// reads as WAITING, not "up to date". connect() is idempotent (the layout connects too).
const { coolingDownSources, connect } = useProgressStream()
onMounted(connect)

// Engine visibility: the two live header countdowns (derived from SSE + settings)
// and the per-source status strip (polled while the page is visible). Both
// composables own their own data/lifecycle; the screen renders the results.
const { downloadRunning, refreshRunning, downloadRemainingMs, refreshRemainingMs } = useCycleTimers()
const { sources: sourceStatuses } = useEngineStatus()
</script>

<template>
  <div class="page-downloads">
    <Downloads
      :items="items"
      :active-tab="activeTab"
      :cycle-active="cycleActive"
      :next-cycle-minutes="nextCycleMinutes"
      :retrying-ids="retryingIds"
      :retrying-all="retryingAll"
      :retry-error="retryError"
      :loading="loading"
      :counts="counts"
      :total="total"
      :has-more="hasMore"
      :loading-more="loadingMore"
      :running="running"
      :run-message="runMessage"
      :run-error="runError"
      :cooling-down-sources="coolingDownSources"
      :download-running="downloadRunning"
      :refresh-running="refreshRunning"
      :download-remaining-ms="downloadRemainingMs"
      :refresh-remaining-ms="refreshRemainingMs"
      :source-statuses="sourceStatuses"
      @set-tab="setTab"
      @retry="retry"
      @retry-all="retryAll"
      @open-series="(id: string) => navigateTo(`/series/${id}`)"
      @dismiss-error="dismissError"
      @load-more="loadMore"
      @run-now="runNow"
      @open-health="navigateTo('/health?tab=sources')"
    />
  </div>
</template>

<style scoped>
.page-downloads {
  min-height: 100%;
}
</style>
