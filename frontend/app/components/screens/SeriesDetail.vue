<script setup lang="ts">
import { computed, ref } from 'vue'
import ErrorBanner from '../ui/ErrorBanner.vue'
import type { MoveDirection } from '../ui/controls.types'
import ChaptersPanel from '../seriesDetail/ChaptersPanel.vue'
import DeleteSeriesDialog from '../seriesDetail/DeleteSeriesDialog.vue'
import MetadataSourcePicker from '../seriesDetail/MetadataSourcePicker.vue'
import ResumeFab from '../seriesDetail/ResumeFab.vue'
import SeriesHeader from '../seriesDetail/SeriesHeader.vue'
import SourcesPanel from '../seriesDetail/SourcesPanel.vue'
import type { Chapter, Provider, SeriesDetail } from './seriesDetail.types'
import { findDriftedProviderIds } from '~/utils/providerDedup'

/**
 * SeriesDetail — the full single-series management screen: a thin container that
 * composes the header (cover/title/stats/toggles/category/delete), the (planned)
 * metadata-source picker, the chapter table, the ranked source list (reorder /
 * remove / add / match-to-source for unlinked disk-origin groups), plus the
 * required-choice delete dialog. The `matchProvider` emit (bubbled from
 * `SourcesPanel`'s unlinked-row action) opens the page's
 * `MatchDiskProviderDialog` for the no-re-download Match, and
 * `requestRemoveSource` (bubbled from the row's Remove action) opens the page's
 * `RemoveSourceDialog` — the confirm dialogs whose lifetime depends on a
 * mutation OUTCOME live on the page, which is the only layer that learns whether
 * the mutation succeeded (an emit is fire-and-forget).
 *
 * Presentation only: ALL data arrives via props and every action is emitted —
 * the screen never fetches, routes, or mutates the backend. It honours §16 by
 * surfacing loading (busy spinners / disabled controls) and error (a dismissible
 * banner) states; success is reflected when the parent feeds back an updated
 * `series` prop. Token-only colours, so it reads correctly in both themes.
 *
 * Each source's chapter coverage rides the `series` prop itself
 * (`Provider.feedCount` / `feedRanges`) — no coverage fetch, no source call.
 *
 * The floating `ResumeFab` ("Start"/"Continue" reading) is driven entirely by
 * `resumeLabel`: null/"" hides it (nothing downloaded to resume), any other
 * string renders it with that label. The screen does not compute the resume
 * target or navigate — it just emits `resume` and lets the page (which already
 * owns the resume-target math via `useReadingProgress.resumeTarget`) route to
 * the reader.
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
  /** A failed-mutation message to surface, or null/"" when there is none. */
  error?: string | null
  /** True while the dedup-providers request is in flight. */
  dedupBusy?: boolean
  /** True while the dedupe-files request is in flight. */
  dedupeFilesBusy?: boolean
  /** Transient dedup/dedupe-files result message. */
  dedupMessage?: string | null
  /** "Start"/"Continue" — renders the floating resume button; null/"" hides it (nothing downloaded). */
  resumeLabel?: string | null
}>(), {
  saving: false,
  deleteBusy: false,
  error: null,
  dedupBusy: false,
  dedupeFilesBusy: false,
  dedupMessage: null,
  resumeLabel: null,
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
  /** "Remove" was pressed on a source row — carries its SeriesProvider id (→ opens the page's confirm dialog). */
  requestRemoveSource: [providerId: string]
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
  /** The resume FAB was clicked (→ the page resolves the resume target and opens the reader). */
  resume: []
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
// The delete dialog's lifetime does NOT depend on a mutation outcome the screen
// can't see: a successful delete navigates away (the page unmounts), a failed
// one leaves the dialog open with the error shown inside it (§16).
const deleteOpen = ref(false)
const onConfirmDelete = (deleteFiles: boolean): void => {
  emit('deleteSeries', deleteFiles)
}
</script>

<template>
  <div class="detail">
    <!-- Everything above the two-panel region keeps its natural height (see
         .detail__top below) — only .columns is allowed to shrink to fit the
         viewport. -->
    <div class="detail__top">
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
    </div>

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
        @remove-source="emit('requestRemoveSource', $event)"
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
      :error="error"
      @confirm="onConfirmDelete"
    />

    <ResumeFab v-if="resumeLabel" :label="resumeLabel" @click="emit('resume')" />
  </div>
</template>

<style scoped>
/* The screen is bounded to the viewport BELOW the AppShell header (shell/
 * AppShell.vue's `.head` is a fixed 64px) so the two-panel region never grows
 * a page-level scrollbar: it scrolls internally instead (each panel owns its
 * own scroll body — see .columns / PanelCard). `.detail__top` (header +
 * metadata picker + error banner) keeps its natural height; only `.columns`
 * is allowed to shrink to whatever is left. AppShell itself is out of scope
 * for this fix (pure layout, this screen only), hence the coupled magic
 * number instead of a shared CSS var — if AppShell's header height ever
 * changes, update it here too. */
.detail {
  padding: 24px 30px 70px;
  background: var(--bg);
  height: calc(100vh - 64px);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.detail__top {
  flex: none;
}

/* §16 error banner spacing (the banner chrome itself lives in the ErrorBanner atom). */
.detail__error {
  margin-bottom: 16px;
}

/* ---- Two-column layout ---------------------------------------------------- */
/* Chapters gets the WIDER column — it's the panel actually scanned. Sources
 * is a bounded list (4-7 cards) and gets the narrower one. */
.columns {
  display: grid;
  grid-template-columns: 1.7fr 1fr;
  gap: 18px;
  flex: 1;
  /* 🔴 THE OVERFLOW TRAP: a flex/grid child's automatic minimum size is its
   * CONTENT size, not 0. Without this, `.columns` refuses to shrink below the
   * combined natural height of both panels (easily 1000px+ with 7 sources),
   * so the panels' own internal `overflow-y: auto` never engages and the
   * WHOLE PAGE grows an unbounded scrollbar instead — every other rule here
   * looks correct while this one is silently defeated. Do not remove this to
   * "clean up" the CSS; see PanelCard.vue for the matching min-height:0 one
   * level down (the grid items themselves), and one level down again inside
   * PanelCard for the scrolling body — the trap applies at every nesting level. */
  min-height: 0;
}

@media (max-width: 900px) {
  .columns {
    grid-template-columns: 1fr;
    /* Narrow layout: stop fighting for two independent internal scrollers —
     * stack the panels and let this region be the one scroll area instead
     * (still bounded by .detail's overflow:hidden, so still no page scroll). */
    overflow-y: auto;
  }
}
</style>
