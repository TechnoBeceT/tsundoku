<script setup lang="ts">
/**
 * Series detail page — route /series/:id.
 *
 * Delegates all data fetching, mutation state, and §16 error handling to
 * useSeriesDetail(id). The "Match source" dialog (the inverse of remove-source)
 * is backed by its OWN composable, useMatchSource(id) — searching sources is
 * orthogonal to the series-detail state useSeriesDetail owns. SeriesDetail and
 * MatchSourceDialog are auto-imported from app/components/. navigateTo is a
 * Nuxt auto-import.
 *
 * Prop wiring:
 *   :series            — the mapped SeriesDetail (null while loading)
 *   :category-options  — string[] of category names for the recategorize select
 *   :saving            — true while an inline mutation is in flight
 *   :delete-busy       — true while the delete request is in flight
 *   :remove-busy       — true while a remove-source request is in flight
 *   :error             — latest mutation error message (null when none)
 *
 * Emit wiring (every emit the screen declares, per the SFC defineEmits contract):
 *   @change-category        → setCategory(name)
 *   @toggle-monitored       → setMonitored(bool)
 *   @toggle-completed       → setCompleted(bool)
 *   @reorder-providers      → reorderProviders(list)
 *   @remove-source          → removeSource(providerId)
 *   @choose-metadata-source → chooseMetadataSource(providerId)
 *   @delete-series          → deleteSeries(deleteFiles)   (navigates to / on success)
 *   @add-source             → opens MatchSourceDialog (matchOpen = true)
 *   @dismiss-error          → dismissError()
 *
 * Match-source wiring: MatchSourceDialog's `search`/`confirm` emits drive
 * useMatchSource's `search`/`addProvider`. On a successful `addProvider` the
 * dialog closes and `useSeriesDetail.refresh()` reloads the authoritative
 * series state — the same "mutate, then refetch" pattern every other
 * useSeriesDetail mutation already uses (§16 round-trip).
 *
 * §16: pending true during the initial fetch; ErrorBanner shown on hard fetch
 * failure. Mutation errors are surfaced via the :error prop (dismissible banner
 * inside SeriesDetail).
 */
const route = useRoute()
const id = route.params.id as string

const {
  series,
  categoryOptions,
  pending,
  error,
  saving,
  deleteBusy,
  removeBusy,
  setMonitored,
  setCompleted,
  setCategory,
  reorderProviders,
  removeSource,
  chooseMetadataSource,
  deleteSeries,
  dismissError,
  refresh,
} = useSeriesDetail(id)

const {
  groups: matchGroups,
  searching: matchSearching,
  saving: matchSaving,
  error: matchError,
  search: matchSearch,
  addProvider: matchAddProvider,
} = useMatchSource(id)

const matchOpen = ref(false)

async function onMatchConfirm(payload: { source: string, mangaId: number, importance: number }): Promise<void> {
  const detail = await matchAddProvider(payload)
  if (detail) {
    matchOpen.value = false
    await refresh()
  }
}
</script>

<template>
  <div class="page-series-detail">
    <div v-if="pending && !series" class="page-series-detail__loading">
      Loading…
    </div>
    <ErrorBanner v-else-if="error && !series" :message="error" />
    <SeriesDetail
      v-else-if="series"
      :series="series"
      :category-options="categoryOptions"
      :saving="saving"
      :delete-busy="deleteBusy"
      :remove-busy="removeBusy"
      :error="error"
      @change-category="setCategory"
      @toggle-monitored="setMonitored"
      @toggle-completed="setCompleted"
      @reorder-providers="reorderProviders"
      @remove-source="removeSource"
      @choose-metadata-source="chooseMetadataSource"
      @delete-series="deleteSeries"
      @add-source="matchOpen = true"
      @dismiss-error="dismissError"
    />

    <MatchSourceDialog
      v-if="series"
      v-model:open="matchOpen"
      :series-title="series.title"
      :groups="matchGroups"
      :searching="matchSearching"
      :saving="matchSaving"
      :error="matchError"
      @search="matchSearch"
      @confirm="onMatchConfirm"
    />
  </div>
</template>

<style scoped>
.page-series-detail {
  min-height: 100%;
}

.page-series-detail__loading {
  padding: 40px;
  color: var(--text-muted);
  text-align: center;
}
</style>
