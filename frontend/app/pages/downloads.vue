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
 *   :retrying-ids     — chapter ids with a single retry in flight
 *   :retrying-all     — bulk retry scope in flight, or null
 *   :retry-error      — surfaced retry/load failure (dismissible banner)
 *   :loading          — true during the initial fetch
 *
 * Emit wiring:
 *   @set-tab        → setTab(tab)
 *   @retry          → retry(chapterId)
 *   @retry-all      → retryAll(state)
 *   @open-series    → navigateTo('/series/' + id)
 *   @dismiss-error  → dismissError()
 */
const {
  items,
  activeTab,
  loading,
  retryingIds,
  retryingAll,
  retryError,
  cycleActive,
  nextCycleMinutes,
  setTab,
  retry,
  retryAll,
  dismissError,
} = useDownloads()
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
      @set-tab="setTab"
      @retry="retry"
      @retry-all="retryAll"
      @open-series="(id: string) => navigateTo(`/series/${id}`)"
      @dismiss-error="dismissError"
    />
  </div>
</template>

<style scoped>
.page-downloads {
  min-height: 100%;
}
</style>
