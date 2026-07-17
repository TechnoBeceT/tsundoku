<script setup lang="ts">
import { computed, ref } from 'vue'
import ErrorBanner from '../ui/ErrorBanner.vue'
import type { MoveDirection } from '../ui/controls.types'
import ChaptersPanel from '../seriesDetail/ChaptersPanel.vue'
import DeleteSeriesDialog from '../seriesDetail/DeleteSeriesDialog.vue'
import ResumeFab from '../seriesDetail/ResumeFab.vue'
import RichSeriesCard from '../seriesDetail/RichSeriesCard.vue'
import SourcesPanel from '../seriesDetail/SourcesPanel.vue'
import TrackersSection from '../seriesDetail/TrackersSection.vue'
import type { Chapter, Provider, SeriesDetail, TrackBinding, TrackSearchResult, UpdateTrackPatch } from './seriesDetail.types'
import type { TrackerStatus } from './settings.types'
import { findDriftedProviderIds } from '~/utils/providerDedup'

/**
 * SeriesDetail — the full single-series management screen: a thin container that
 * composes the rich catalogue card (cover/title/synopsis/credits/genres/tags/
 * links/stats/toggles/category/delete — `RichSeriesCard`, superseding the
 * plainer `SeriesHeader`), the INLINE `TrackersSection` (QCAT-234 — replaces
 * the retired PLANNED metadata-source picker card), the chapter table, the
 * ranked source list (reorder / remove / add / match-to-source for unlinked
 * disk-origin groups), plus the required-choice delete dialog. The
 * `matchProvider` emit (bubbled from `SourcesPanel`'s unlinked-row action)
 * opens the page's `MatchDiskProviderDialog` for the no-re-download Match, and
 * `requestRemoveSource` (bubbled from the row's Remove action) opens the page's
 * `RemoveSourceDialog` — the confirm dialogs whose lifetime depends on a
 * mutation OUTCOME live on the page, which is the only layer that learns whether
 * the mutation succeeded (an emit is fire-and-forget). `requestFractionalCleanup`
 * (bubbled from the Sources panel's "Remove fractional files" button, which the
 * panel renders only when `fractionalCleanupCount > 0`) opens the page's
 * `FractionalCleanupDialog` for the same reason. `RichSeriesCard`'s
 * `openMetadata`/`openCoverPicker` emits bubble here as `requestIdentify`/
 * `requestCoverPicker` for the SAME reason — the native-metadata-engine
 * "Identify" and "Choose cover" modals (`MetadataIdentifyModal`/
 * `CoverPickerModal`) mutate and hand back a fresh `SeriesDetail`, an outcome
 * only the page can observe.
 *
 * `TrackersSection` is always visible (no button, no dialog — QCAT-234 killed
 * both the RichSeriesCard "Trackers" toolbar button and the modal
 * `TrackingDialog` it used to open) and its bind/unbind/refresh/update/sync
 * actions bubble here as `trackBind`/`trackUnbind`/`trackRefresh`/
 * `trackUpdate`/`trackSync` — same page-owns-the-mutation reasoning as above,
 * since only the page can see whether a tracking mutation succeeded.
 *
 * Reading-progress reset (QCAT-242, two entry points, both page-owned mutations
 * routed through `useSeriesDetail.setReadingProgress`):
 *   - `TrackersSection`'s own "Reset progress" dialog emits `set-progress`,
 *     bubbled here as `resetProgress` — the page calls `setReadingProgress`
 *     directly and feeds `settingProgress`/`progressError` back down so the
 *     section's dialog can auto-close on success / stay open on failure.
 *   - `ChaptersPanel`'s per-row "Set as current progress" emits `set-current`
 *     (the chapter NUMBER), bubbled here as `requestSetChapterProgress` — the
 *     page owns the confirm dialog (`SetChapterProgressDialog`, same
 *     page-owned-dialog reasoning as `RemoveSourceDialog`) since only it
 *     learns whether the reset succeeded.
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
  /** How many downloaded fractional chapters are removable; 0 hides the "Remove fractional files" button. */
  fractionalCleanupCount?: number
  /** Transient dedup/dedupe-files result message. */
  dedupMessage?: string | null
  /** "Start"/"Continue" — renders the floating resume button; null/"" hides it (nothing downloaded). */
  resumeLabel?: string | null
  /** This series' current tracker bindings (TrackersSection). */
  trackBindings?: TrackBinding[]
  /** Every registered tracker's connect status (TrackersSection). */
  trackers?: TrackerStatus[]
  /** True while the tracker bindings list is loading. */
  trackBindingsPending?: boolean
  /** A tracker-bindings-load failure, or null for none. */
  trackBindingsError?: string | null
  /** The "Add tracking" search results. */
  trackSearchResults?: TrackSearchResult[]
  /** True while a tracker search is in flight. */
  trackSearching?: boolean
  /** A failed tracker-search message, or null for none. */
  trackSearchError?: string | null
  /** True while a tracker bind POST is in flight. */
  trackBinding?: boolean
  /** A failed tracker-bind message, or null for none. */
  trackBindError?: string | null
  /** The TrackBinding id currently being unbound, or null. */
  trackUnbindBusyId?: string | null
  /** A failed tracker-unbind message, or null for none. */
  trackUnbindError?: string | null
  /** The TrackBinding id `trackUnbindError` belongs to, or null. */
  trackUnbindErrorId?: string | null
  /** The TrackBinding id currently being remote-refreshed, or null. */
  trackRefreshBusyId?: string | null
  /** A failed tracker-refresh message, or null for none. */
  trackRefreshError?: string | null
  /** The TrackBinding id `trackRefreshError` belongs to, or null. */
  trackRefreshErrorId?: string | null
  /** The TrackBinding id currently being manually edited, or null. */
  trackUpdateBusyId?: string | null
  /** A failed manual tracker-edit message, or null for none. */
  trackUpdateError?: string | null
  /** True while "Sync now" (pull + converge every binding) is in flight. */
  trackSyncing?: boolean
  /** A failed tracker-sync message, or null for none. */
  trackSyncError?: string | null
  /** True while a reading-progress reset (QCAT-242) POST is in flight. */
  settingProgress?: boolean
  /** A failed reading-progress-reset message, or null for none. */
  progressError?: string | null
}>(), {
  saving: false,
  deleteBusy: false,
  error: null,
  dedupBusy: false,
  dedupeFilesBusy: false,
  fractionalCleanupCount: 0,
  dedupMessage: null,
  resumeLabel: null,
  trackBindings: () => [],
  trackers: () => [],
  trackBindingsPending: false,
  trackBindingsError: null,
  trackSearchResults: () => [],
  trackSearching: false,
  trackSearchError: null,
  trackBinding: false,
  trackBindError: null,
  trackUnbindBusyId: null,
  trackUnbindError: null,
  trackUnbindErrorId: null,
  trackRefreshBusyId: null,
  trackRefreshError: null,
  trackRefreshErrorId: null,
  trackUpdateBusyId: null,
  trackUpdateError: null,
  trackSyncing: false,
  trackSyncError: null,
  settingProgress: false,
  progressError: null,
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
  /** A source's "Ignore fractional chapters" switch flipped — carries its SeriesProvider id and the NEW value. */
  toggleIgnoreFractional: [providerId: string, ignore: boolean]
  /** RichSeriesCard's "Metadata" button was pressed (→ the page opens MetadataIdentifyModal). */
  requestIdentify: []
  /** RichSeriesCard's "Change cover" affordance was pressed (→ the page opens CoverPickerModal). */
  requestCoverPicker: []
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
  /** "Remove fractional files" pressed (→ the page opens its FractionalCleanupDialog). */
  requestFractionalCleanup: []
  /** A chapter's "Read" was clicked — carries the chapter UUID (→ opens the reader). */
  read: [chapterId: string]
  /** The resume FAB was clicked (→ the page resolves the resume target and opens the reader). */
  resume: []
  /** TrackersSection ran a search on the given tracker for the trimmed query. */
  trackSearch: [payload: { trackerId: number, q: string }]
  /** TrackersSection asked to bind the series to a tracker's remote entry. */
  trackBind: [payload: { trackerId: number, remoteId: string, private?: boolean }]
  /** TrackersSection asked to unbind a TrackBinding — carries its id. */
  trackUnbind: [recordId: string]
  /** TrackersSection asked to re-pull a TrackBinding's remote entry — carries its id. */
  trackRefresh: [recordId: string]
  /** TrackersSection applied a changed-fields-only manual edit to a TrackBinding. */
  trackUpdate: [payload: { recordId: string, patch: UpdateTrackPatch }]
  /** TrackersSection asked to pull + converge every one of this series' tracker bindings. */
  trackSync: []
  /** TrackersSection's expanded "Add tracking" tracker changed — clear the shared search state (bug 1). */
  trackClearSearch: []
  /** TrackersSection's "Reset progress" dialog was confirmed — carries the resolved target chapter (0 = from start). */
  resetProgress: [chapter: number]
  /** A chapter row's "Set as current progress" was clicked — carries the chapter NUMBER (→ opens the page's confirm dialog). */
  requestSetChapterProgress: [chapterNumber: number]
}>()

// ---- Derived data ----------------------------------------------------------
// Chapters ordered LATEST-FIRST by number (null sorts as 0) then by stable key
// (reversed tiebreak to match) — Komikku/Suwayomi convention: newest chapter
// on top so reading progress is visible without scrolling to the bottom.
const sortedChapters = computed<Chapter[]>(() =>
  [...props.series.chapters]
    .filter((c) => c.state !== 'superseded' && c.state !== 'ignored')
    .sort(
      (a, b) => (b.number ?? 0) - (a.number ?? 0) || b.chapterKey.localeCompare(a.chapterKey),
    ),
)

// Sources ordered by importance descending — the top one is "Preferred".
const sortedProviders = computed<Provider[]>(() =>
  [...props.series.providers].sort((a, b) => b.importance - a.importance),
)

// SeriesProvider ids involved in a same-physical-source drift (disk/live twin) —
// surfaces the "Clean up duplicate sources" affordance in SourcesPanel.
const driftedIds = computed(() => findDriftedProviderIds(props.series.providers))

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
    <div class="detail__top">
      <!-- §16 error banner: a failed mutation surfaces here, dismissible -->
      <div v-if="error" class="detail__error">
        <ErrorBanner :message="error" @dismiss="emit('dismissError')" />
      </div>

      <RichSeriesCard
        :series="series"
        :category-options="categoryOptions"
        :saving="saving"
        @change-category="emit('changeCategory', $event)"
        @toggle-monitored="emit('toggleMonitored', $event)"
        @toggle-completed="emit('toggleCompleted', $event)"
        @request-delete="deleteOpen = true"
        @open-metadata="emit('requestIdentify')"
        @open-cover-picker="emit('requestCoverPicker')"
      />

      <TrackersSection
        :bindings="trackBindings"
        :series-title="series.title"
        :trackers="trackers"
        :pending="trackBindingsPending"
        :error="trackBindingsError"
        :search-results="trackSearchResults"
        :searching="trackSearching"
        :search-error="trackSearchError"
        :binding="trackBinding"
        :bind-error="trackBindError"
        :unbind-busy-id="trackUnbindBusyId"
        :unbind-error="trackUnbindError"
        :unbind-error-id="trackUnbindErrorId"
        :refresh-busy-id="trackRefreshBusyId"
        :refresh-error="trackRefreshError"
        :refresh-error-id="trackRefreshErrorId"
        :update-busy-id="trackUpdateBusyId"
        :update-error="trackUpdateError"
        :syncing="trackSyncing"
        :sync-error="trackSyncError"
        :setting-progress="settingProgress"
        :progress-error="progressError"
        @search="emit('trackSearch', $event)"
        @bind="emit('trackBind', $event)"
        @unbind="emit('trackUnbind', $event)"
        @refresh="emit('trackRefresh', $event)"
        @update="emit('trackUpdate', $event)"
        @sync="emit('trackSync')"
        @clear-search="emit('trackClearSearch')"
        @set-progress="emit('resetProgress', $event)"
      />
    </div>

    <div class="columns">
      <ChaptersPanel
        :chapters="sortedChapters"
        :total="series.chapterCounts.total"
        @read="emit('read', $event)"
        @set-current="emit('requestSetChapterProgress', $event)"
      />
      <SourcesPanel
        :providers="sortedProviders"
        :saving="saving"
        :drifted-ids="driftedIds"
        :dedup-busy="dedupBusy"
        :dedupe-files-busy="dedupeFilesBusy"
        :fractional-cleanup-count="fractionalCleanupCount"
        :dedup-message="dedupMessage"
        @move="onMove"
        @remove-source="emit('requestRemoveSource', $event)"
        @match-provider="emit('matchProvider', $event)"
        @toggle-ignore-fractional="(providerId, ignore) => emit('toggleIgnoreFractional', providerId, ignore)"
        @add-source="emit('addSource')"
        @dedup-providers="emit('dedupProviders')"
        @dedupe-files="emit('dedupeFiles')"
        @remove-fractional="emit('requestFractionalCleanup')"
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
/* `.detail` is a FLOWING document container that GROWS with its content
 * (QCAT-265 treatment #3 — the default). No viewport-fit: the 2026-07-13
 * `min-height: calc(100dvh − 64px)` letterbox floor was removed (GAP-093) —
 * it was part of the experience-drift batch the owner rejected ("on a really
 * big screen I'm trying to work inside a small area"). RichSeriesCard's height
 * is content-driven (description + tag cloud + links + stats) and simply flows;
 * shell/AppShell.vue owns the only legitimate viewport floor (`min-height:
 * 100vh` on the shell, untouched here) plus the sticky 64px `.head`.
 *
 * A flex column so the top-level sections — the RichSeriesCard/Trackers block
 * (`.detail__top`) and the Chapters/Sources `.columns` — share ONE consistent
 * gap (--space-xl = the design's 18px, now fluid so it tightens on a phone). */
.detail {
  display: flex;
  flex-direction: column;
  gap: var(--space-xl);
  padding: var(--space-2xl) var(--space-3xl) 70px;
  background: var(--bg);
}

/* A flex column so RichSeriesCard and TrackersSection get a consistent gap
 * regardless of which optional siblings (the error banner) are present —
 * neither SurfaceCard-based child carries its own top/bottom margin, so
 * without this the two panels sat flush against each other (a regression
 * from the old SeriesHeader→RichSeriesCard swap). Matches `.columns`' gap. */
.detail__top {
  display: flex;
  flex-direction: column;
  gap: var(--space-xl);
}

/* ---- Two-column layout: QCAT-265 treatment #1 (BOUNDED inner-scroll) ------ */
/* Chapters gets the WIDER column — it's the panel actually scanned; Sources
 * (4-7 cards) gets the narrower one. This is the ONE place the QCAT-265
 * diagnostic fires (§2.6.1): ASYMMETRY (320 chapters beside 4 sources) AND
 * EMPTY SPACE (an unbounded Sources column sits blank for thousands of px while
 * Chapters grows). So each panel bounds ITSELF at a content-keyed
 * `max-height: 580px` (the prototype's own value) via PanelCard's `maxHeight`
 * prop — NOT this grid. `.columns` carries NO height and NO overflow of its
 * own: the letterbox (`height: calc(100dvh − 64px)` here + `grid-auto-rows:
 * calc(100dvh − 64px)` on the stacked breakpoint) is GONE. `align-items: start`
 * (the prototype's own value) lets each panel size to its own content up to its
 * 580px cap, so a short Sources panel never stretches to Chapters' height and
 * leaves dead space beneath its list. The PAGE grows; each panel scrolls its
 * own list independently. */
.columns {
  display: grid;
  grid-template-columns: 1.7fr 1fr;
  gap: var(--space-xl);
  align-items: start;
}

@media (max-width: 900px) {
  /* Below the two-column threshold the pair stacks into ONE column. Each panel
   * KEEPS its own 580px bounded inner-scroll (from PanelCard's `maxHeight`), so
   * Sources sits right after ONE bounded Chapters panel — not 320 chapter rows
   * down — while the PAGE scrolls between the stacked panels. No viewport-fit. */
  .columns {
    grid-template-columns: 1fr;
  }
}
</style>
