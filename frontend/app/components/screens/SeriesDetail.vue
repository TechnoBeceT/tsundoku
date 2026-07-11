<script setup lang="ts">
import { computed, ref } from 'vue'
import ErrorBanner from '../ui/ErrorBanner.vue'
import type { MoveDirection } from '../ui/controls.types'
import ChaptersPanel from '../seriesDetail/ChaptersPanel.vue'
import DeleteSeriesDialog from '../seriesDetail/DeleteSeriesDialog.vue'
import MetadataSourcePicker from '../seriesDetail/MetadataSourcePicker.vue'
import RemoveSourceDialog from '../seriesDetail/RemoveSourceDialog.vue'
import SeriesHeader from '../seriesDetail/SeriesHeader.vue'
import SourcesPanel from '../seriesDetail/SourcesPanel.vue'
import type { Chapter, Provider, SeriesDetail } from './seriesDetail.types'
import { findDriftedProviderIds } from '~/utils/providerDedup'

/**
 * SeriesDetail — the full single-series management screen: a thin container that
 * composes the header (cover/title/stats/toggles/category/delete), the (planned)
 * metadata-source picker, the chapter table, the ranked source list (reorder /
 * remove / add / match-to-source for unlinked disk-origin groups), plus the
 * required-choice delete dialog and the
 * remove-source confirm dialog. The `matchProvider` emit (bubbled from
 * `SourcesPanel`'s unlinked-row action) opens the page's
 * `MatchDiskProviderDialog` for the no-re-download Match.
 *
 * Presentation only: ALL data arrives via props and every action is emitted —
 * the screen never fetches, routes, or mutates the backend. It honours §16 by
 * surfacing loading (busy spinners / disabled controls) and error (a dismissible
 * banner) states; success is reflected when the parent feeds back an updated
 * `series` prop. Token-only colours, so it reads correctly in both themes.
 *
 * Each source's chapter coverage rides the `series` prop itself
 * (`Provider.feedCount` / `feedRanges`) — no coverage fetch, no source call.
 */
const props = withDefaults(defineProps<{
  /** The series to render (summary fields + chapters + providers). */
  series: SeriesDetail
  /** Category names for the recategorize select (dynamic, user-defined list). */
  categoryOptions: string[]
  /** True while an inline mutation (toggle/category/reorder/metadata) is in flight. */
  saving?: boolean
  /** True while the delete request is in flight (dialog confirm spinner). */
  deleteBusy?: boolean
  /** True while the remove-source request is in flight (dialog confirm spinner). */
  removeBusy?: boolean
  /** A failed-mutation message to surface, or null/"" when there is none. */
  error?: string | null
  /** True while the dedup-providers request is in flight. */
  dedupBusy?: boolean
  /** True while the dedupe-files request is in flight. */
  dedupeFilesBusy?: boolean
  /** Transient dedup/dedupe-files result message. */
  dedupMessage?: string | null
}>(), {
  saving: false,
  deleteBusy: false,
  removeBusy: false,
  error: null,
  dedupBusy: false,
  dedupeFilesBusy: false,
  dedupMessage: null,
})

const emit = defineEmits<{
  /** The category select changed — carries the new category name. */
  changeCategory: [category: string]
  /** The monitored toggle flipped — carries the NEW value. */
  toggleMonitored: [monitored: boolean]
  /** The completed toggle flipped — carries the NEW value. */
  toggleCompleted: [completed: boolean]
  /** Providers were re-ranked — carries the full updated {id, importance} list. */
  reorderProviders: [providers: { id: string, importance: number }[]]
  /** A source removal was confirmed — carries the SeriesProvider id. */
  removeSource: [providerId: string]
  /** "Match to source" was pressed on an unlinked disk-origin group — carries its SeriesProvider id. */
  matchProvider: [providerId: string]
  /** A metadata source was picked — carries the SeriesProvider id. */
  chooseMetadataSource: [providerId: string]
  /** The series delete was confirmed — carries the required deleteFiles choice. */
  deleteSeries: [deleteFiles: boolean]
  /** The owner asked to add a source (→ opens the Match Source dialog). */
  addSource: []
  /** The error banner was dismissed. */
  dismissError: []
  /** "Clean up duplicate sources" pressed. */
  dedupProviders: []
  /** "Remove duplicate files" pressed. */
  dedupeFiles: []
  /** A chapter's "Read" was clicked — carries the chapter UUID (→ opens the reader). */
  read: [chapterId: string]
}>()

// ---- Derived data ----------------------------------------------------------
// Chapters ordered by number (null sorts as 0) then by stable key — matches the
// backend's "ordered by number then chapterKey" contract.
const sortedChapters = computed<Chapter[]>(() =>
  [...props.series.chapters]
    .filter((c) => c.state !== 'superseded')
    .sort(
      (a, b) => (a.number ?? 0) - (b.number ?? 0) || a.chapterKey.localeCompare(b.chapterKey),
    ),
)

// Sources ordered by importance descending — the top one is "Preferred".
const sortedProviders = computed<Provider[]>(() =>
  [...props.series.providers].sort((a, b) => b.importance - a.importance),
)

// SeriesProvider ids involved in a same-physical-source drift (disk/live twin) —
// surfaces the "Clean up duplicate sources" affordance in SourcesPanel.
const driftedIds = computed(() => findDriftedProviderIds(props.series.providers))

// The preferred (rank-1) source id, and the active metadata source (pinned, else
// the preferred one when auto/unset).
const preferredId = computed(() => sortedProviders.value[0]?.id ?? null)
const metaActiveId = computed(() => props.series.metadataProviderId ?? preferredId.value)

// ---- Reorder ---------------------------------------------------------------
// Move a source up (dir -1) or down (dir +1) one rank, then emit the FULL list
// with the existing importance values reassigned by new position (higher rank =
// higher importance) — the API applies the batch all-or-nothing.
const onMove = (id: string, dir: MoveDirection): void => {
  if (props.saving) return
  const list = [...sortedProviders.value]
  const i = list.findIndex((p) => p.id === id)
  const j = i + dir
  if (j < 0 || j >= list.length) return
  ;[list[i], list[j]] = [list[j]!, list[i]!]
  const importances = sortedProviders.value.map((p) => p.importance).sort((a, b) => b - a)
  emit('reorderProviders', list.map((p, idx) => ({ id: p.id, importance: importances[idx]! })))
}

// ---- Metadata source -------------------------------------------------------
const onPickMeta = (id: string): void => {
  if (!props.saving) emit('chooseMetadataSource', id)
}

// ---- Delete dialog ---------------------------------------------------------
const deleteOpen = ref(false)
const onConfirmDelete = (deleteFiles: boolean): void => {
  emit('deleteSeries', deleteFiles)
}

// ---- Remove-source dialog --------------------------------------------------
const removeOpen = ref(false)
const removeTargetId = ref<string | null>(null)
const openRemove = (id: string): void => {
  removeTargetId.value = id
  removeOpen.value = true
}
const onConfirmRemove = (): void => {
  if (removeTargetId.value) emit('removeSource', removeTargetId.value)
}
const removeName = computed(
  () => props.series.providers.find((p) => p.id === removeTargetId.value)?.provider ?? '',
)
</script>

<template>
  <div class="detail">
    <!-- §16 error banner: a failed mutation surfaces here, dismissible -->
    <div v-if="error" class="detail__error">
      <ErrorBanner :message="error" @dismiss="emit('dismissError')" />
    </div>

    <SeriesHeader
      :series="series"
      :category-options="categoryOptions"
      :saving="saving"
      @change-category="emit('changeCategory', $event)"
      @toggle-monitored="emit('toggleMonitored', $event)"
      @toggle-completed="emit('toggleCompleted', $event)"
      @request-delete="deleteOpen = true"
    />

    <MetadataSourcePicker
      :providers="sortedProviders"
      :title="series.title"
      :active-id="metaActiveId"
      :preferred-id="preferredId"
      :saving="saving"
      @pick="onPickMeta"
    />

    <div class="columns">
      <ChaptersPanel :chapters="sortedChapters" :total="series.chapterCounts.total" @read="emit('read', $event)" />
      <SourcesPanel
        :providers="sortedProviders"
        :saving="saving"
        :drifted-ids="driftedIds"
        :dedup-busy="dedupBusy"
        :dedupe-files-busy="dedupeFilesBusy"
        :dedup-message="dedupMessage"
        @move="onMove"
        @remove-source="openRemove"
        @match-provider="emit('matchProvider', $event)"
        @add-source="emit('addSource')"
        @dedup-providers="emit('dedupProviders')"
        @dedupe-files="emit('dedupeFiles')"
      />
    </div>

    <DeleteSeriesDialog
      v-model:open="deleteOpen"
      :busy="deleteBusy"
      :series-title="series.title"
      @confirm="onConfirmDelete"
    />

    <RemoveSourceDialog
      v-model:open="removeOpen"
      :busy="removeBusy"
      :source-name="removeName"
      @confirm="onConfirmRemove"
    />
  </div>
</template>

<style scoped>
.detail {
  padding: 24px 30px 70px;
  background: var(--bg);
  min-height: 100%;
}

/* §16 error banner spacing (the banner chrome itself lives in the ErrorBanner atom). */
.detail__error {
  margin-bottom: 16px;
}

/* ---- Two-column layout ---------------------------------------------------- */
.columns {
  display: grid;
  grid-template-columns: 1.55fr 1fr;
  gap: 18px;
  align-items: start;
}

@media (max-width: 900px) {
  .columns {
    grid-template-columns: 1fr;
  }
}
</style>
