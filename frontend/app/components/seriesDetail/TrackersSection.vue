<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import AppButton from '../ui/AppButton.vue'
import EmptyState from '../ui/EmptyState.vue'
import ErrorBanner from '../ui/ErrorBanner.vue'
import FormError from '../ui/FormError.vue'
import IconButton from '../ui/IconButton.vue'
import SearchInput from '../ui/SearchInput.vue'
import Skeleton from '../ui/Skeleton.vue'
import SurfaceCard from '../ui/SurfaceCard.vue'
import TrackerIcon from '../ui/TrackerIcon.vue'
import TrackerBindingRow from './TrackerBindingRow.vue'
import TrackerSearchResultCard from './TrackerSearchResultCard.vue'
import type { TrackBinding, TrackSearchResult, UpdateTrackPatch } from '../screens/seriesDetail.types'
import type { TrackerStatus } from '../screens/settings.types'

/**
 * TrackersSection — the Series-Detail INLINE "Trackers" panel (QCAT-234), a
 * Komikku-style always-visible section replacing the retired PLANNED
 * `MetadataSourcePicker` card and the modal `TrackingDialog` it superseded.
 * No open/close affordance — it renders directly in `SeriesDetail`'s
 * `.detail__top`, mirroring `RichSeriesCard`'s `SurfaceCard` shell.
 *
 * QCAT-237 reuse: composes `SurfaceCard` (shell) + `TrackerBindingRow` (one
 * per bound tracker, itself built from `AppButton`/`IconButton`/
 * `ScoreSelector`/`TextField`/`Toggle`/`TrackerIcon` — extracted from the
 * retired `TrackingDialog`, not rewritten) + `TrackerSearchResultCard` (one
 * per search hit, built from `CoverImage`/`AppButton`) + `SearchInput`/
 * `IconButton`/`EmptyState`/`ErrorBanner`/`FormError`/`Skeleton`/`TrackerIcon`
 * (each "Add tracking" row's brand logo, same atom as `TrackerBindingRow`
 * and the Settings `TrackerRow`) for the rest.
 *
 * Two sections:
 *   - Bound trackers: each binding's remote title/status/progress/score via
 *     `TrackerBindingRow`, with its own Edit/Refresh/Unbind actions. At most
 *     ONE row's edit form is open at a time (`editingId`, enforced here — a
 *     row has no visibility into its siblings); the busy→idle FALSE EDGE for
 *     the editing row auto-closes the form when no `updateError` landed
 *     (mirrors the retired dialog's watcher).
 *   - "Add tracking": one row PER connected-but-unbound tracker. Pressing a
 *     row expands an inline `SearchInput` + Search button (Komikku's
 *     per-service add affordance, distinct from the retired dialog's single
 *     shared tracker picker) → results render as `TrackerSearchResultCard`s
 *     → Bind. When the EXPANDED tracker's `supportsPrivate` is true (AniList/
 *     Kitsu), an eye/eye-off `IconButton` toggles whether a freshly-created
 *     remote entry is marked private; the toggle is absent for MAL/
 *     MangaUpdates (no such remote concept — see `TrackerStatus.
 *     supportsPrivate`'s doc comment). A row auto-collapses once its tracker
 *     leaves `addableTrackers` (a successful bind removes it from the list).
 *
 * Empty state: when NOTHING is bound and NO tracker is connected-and-unbound
 * either, an `EmptyState` explains there's nothing to track yet — otherwise
 * the "Add tracking" rows themselves ARE the empty view (Komikku's shape),
 * with no separate "nothing bound" message.
 *
 * Presentation-only: all data arrives via props and every action is emitted,
 * mirroring the retired `TrackingDialog`'s contract minus `open`/
 * `update:open` (this section has no open/close state) and `bind` gaining an
 * optional `private` field.
 *
 *   - `bindings`: this series' current tracker bindings.
 *   - `trackers`: every registered tracker's connect status.
 *   - `pending`/`error`: the bindings list load state.
 *   - `searchResults`/`searching`/`searchError`: the "Add tracking" search.
 *   - `binding`/`bindError`: the bind POST in flight.
 *   - `unbindBusyId`/`unbindError`... (see prop docs below for the rest).
 *
 * Emits `search` (`{ trackerId, q }`), `bind`
 * (`{ trackerId, remoteId, private? }`), `unbind` (the TrackBinding id),
 * `refresh` (the TrackBinding id), `update` (`{ recordId, patch }`), `sync`
 * (no payload — pull + converge every binding on this series).
 */
const props = withDefaults(defineProps<{
  /** This series' current tracker bindings. */
  bindings?: TrackBinding[]
  /** Every registered tracker's connect status. */
  trackers?: TrackerStatus[]
  /** True while the bindings list is loading. */
  pending?: boolean
  /** A bindings-load failure, or null for none. */
  error?: string | null
  /** The "Add tracking" search results. */
  searchResults?: TrackSearchResult[]
  /** True while a search is in flight. */
  searching?: boolean
  /** A failed search message, or null for none. */
  searchError?: string | null
  /** True while a bind POST is in flight. */
  binding?: boolean
  /** A failed bind message, or null for none. */
  bindError?: string | null
  /** The TrackBinding id currently being unbound, or null. */
  unbindBusyId?: string | null
  /** The TrackBinding id currently being remote-refreshed, or null. */
  refreshBusyId?: string | null
  /** The TrackBinding id currently being manually edited, or null. */
  updateBusyId?: string | null
  /** A failed manual edit message, or null for none. */
  updateError?: string | null
  /** True while "Sync now" (pull + converge every binding) is in flight. */
  syncing?: boolean
  /** A failed sync message, or null for none. */
  syncError?: string | null
}>(), {
  bindings: () => [],
  trackers: () => [],
  pending: false,
  error: null,
  searchResults: () => [],
  searching: false,
  searchError: null,
  binding: false,
  bindError: null,
  unbindBusyId: null,
  refreshBusyId: null,
  updateBusyId: null,
  updateError: null,
  syncing: false,
  syncError: null,
})

const emit = defineEmits<{
  /** Run a search on the given tracker for the trimmed query. */
  'search': [payload: { trackerId: number, q: string }]
  /** Bind the series to the given tracker's remote entry; `private` set only when the tracker supports it AND the owner opted in. */
  'bind': [payload: { trackerId: number, remoteId: string, private?: boolean }]
  /** Unbind this TrackBinding. */
  'unbind': [recordId: string]
  /** Re-pull this TrackBinding's remote entry. */
  'refresh': [recordId: string]
  /** Apply a changed-fields-only manual edit to this TrackBinding. */
  'update': [payload: { recordId: string, patch: UpdateTrackPatch }]
  /** Pull + converge every one of this series' tracker bindings. */
  'sync': []
}>()

// Connected trackers this series isn't already bound to — one "Add tracking"
// row per entry; an already-bound tracker is edited from its own bound row.
const addableTrackers = computed<TrackerStatus[]>(() =>
  props.trackers.filter((t) => t.isLoggedIn && !props.bindings.some((b) => b.trackerId === t.id)))

const nothingToShow = computed(() => props.bindings.length === 0 && addableTrackers.value.length === 0)

// ---- Bound-row edit (at most one open at a time) ---------------------------
const editingId = ref<string | null>(null)

function toggleEdit(b: TrackBinding): void {
  editingId.value = editingId.value === b.id ? null : b.id
}

function closeEdit(): void {
  editingId.value = null
}

function onSubmit(b: TrackBinding, patch: UpdateTrackPatch): void {
  emit('update', { recordId: b.id, patch })
}

// Auto-close the edit form on the busy→idle FALSE EDGE for its own row, but
// ONLY when no updateError landed (mirrors the retired dialog's watcher) — a
// failure keeps the form open with the error shown inline.
watch(() => props.updateBusyId, (busyId, prevBusyId) => {
  if (prevBusyId && prevBusyId === editingId.value && busyId === null && !props.updateError) {
    closeEdit()
  }
})

// ---- Add-tracking (one expandable per-tracker search row) ------------------
const expandedTrackerId = ref<number | null>(null)
const query = ref('')
const searched = ref(false)
const privateFlag = ref(false)

const expandedTracker = computed<TrackerStatus | null>(() =>
  addableTrackers.value.find((t) => t.id === expandedTrackerId.value) ?? null)

const noResults = computed(() => searched.value && !props.searching && props.searchResults.length === 0)

function toggleAddTracker(trackerId: number): void {
  if (expandedTrackerId.value === trackerId) {
    expandedTrackerId.value = null
    return
  }
  expandedTrackerId.value = trackerId
  query.value = ''
  searched.value = false
  privateFlag.value = false
}

// A successful bind removes its tracker from `addableTrackers` — collapse the
// row automatically rather than leaving a dangling expanded search for a
// tracker that's no longer offered.
watch(addableTrackers, (list) => {
  if (expandedTrackerId.value !== null && !list.some((t) => t.id === expandedTrackerId.value)) {
    expandedTrackerId.value = null
    query.value = ''
    searched.value = false
  }
})

function runSearch(): void {
  if (expandedTrackerId.value === null) return
  const q = query.value.trim()
  if (!q) return
  searched.value = true
  emit('search', { trackerId: expandedTrackerId.value, q })
}

function onBind(remoteId: string): void {
  if (expandedTrackerId.value === null) return
  const payload: { trackerId: number, remoteId: string, private?: boolean } = {
    trackerId: expandedTrackerId.value,
    remoteId,
  }
  if (expandedTracker.value?.supportsPrivate && privateFlag.value) payload.private = true
  emit('bind', payload)
}
</script>

<template>
  <SurfaceCard title="Trackers">
    <template v-if="bindings.length" #actions>
      <AppButton variant="ghost" size="sm" :loading="syncing" :disabled="pending" @click="emit('sync')">
        Sync now
      </AppButton>
    </template>

    <ErrorBanner v-if="error" class="trackers__error" :message="error" :dismissible="false" />

    <div v-if="pending" class="trackers__bound">
      <Skeleton v-for="n in 2" :key="n" variant="row" />
    </div>

    <template v-else>
      <FormError v-if="syncError" class="trackers__error" :message="syncError" />

      <div v-if="bindings.length" class="trackers__bound">
        <TrackerBindingRow
          v-for="b in bindings"
          :key="b.id"
          :binding="b"
          :editing="editingId === b.id"
          :update-busy="updateBusyId === b.id"
          :update-error="editingId === b.id ? updateError : null"
          :unbind-busy="unbindBusyId === b.id"
          :refresh-busy="refreshBusyId === b.id"
          @toggle-edit="toggleEdit(b)"
          @cancel-edit="closeEdit"
          @submit="onSubmit(b, $event)"
          @unbind="emit('unbind', b.id)"
          @refresh="emit('refresh', b.id)"
        />
      </div>

      <EmptyState
        v-if="nothingToShow"
        title="No trackers to add"
        sub="Connect a tracker in Settings → Trackers to add tracking here."
      >
        <template #icon>
          <Icon name="lucide:link-2-off" width="22" height="22" />
        </template>
      </EmptyState>

      <div v-else-if="addableTrackers.length" class="trackers__add">
        <p v-if="bindings.length" class="trackers__add-heading">Add tracking</p>
        <div v-for="t in addableTrackers" :key="t.id" class="add-row">
          <button
            type="button"
            class="add-row__head"
            :aria-expanded="expandedTrackerId === t.id"
            @click="toggleAddTracker(t.id)"
          >
            <span class="add-row__name">
              <TrackerIcon :tracker-id="t.id" :size="16" />
              {{ t.name }}
            </span>
            <Icon :name="expandedTrackerId === t.id ? 'lucide:chevron-up' : 'lucide:chevron-down'" width="16" height="16" />
          </button>

          <div v-if="expandedTrackerId === t.id" class="add-row__panel">
            <div class="add-row__searchbar">
              <SearchInput v-model="query" class="add-row__search" :placeholder="`Search ${t.name}…`" :clearable="false" @enter="runSearch" />
              <!-- eslint-disable vue/attribute-hyphenation -- camelCase :ariaLabel binds the REQUIRED prop; kebab :aria-label routes to the native attr, leaving it unset (vue-tsc error). -->
              <IconButton
                v-if="t.supportsPrivate"
                :ariaLabel="privateFlag ? 'New entries will be private — click to make public' : 'New entries will be public — click to make private'"
                :class="{ 'add-row__private--on': privateFlag }"
                @click="privateFlag = !privateFlag"
              >
                <Icon :name="privateFlag ? 'lucide:eye-off' : 'lucide:eye'" width="15" height="15" />
              </IconButton>
              <!-- eslint-enable vue/attribute-hyphenation -->
              <AppButton variant="solid" size="sm" :loading="searching" :disabled="!query.trim()" @click="runSearch">
                Search
              </AppButton>
            </div>

            <FormError v-if="searchError" class="add-row__error" :message="searchError" />
            <FormError v-if="bindError" class="add-row__error" :message="bindError" />

            <p v-if="noResults" class="trackers__empty">No matches found.</p>

            <div v-else-if="searchResults.length" class="track-results">
              <TrackerSearchResultCard
                v-for="r in searchResults"
                :key="r.remoteId"
                :result="r"
                :busy="binding"
                @bind="onBind(r.remoteId)"
              />
            </div>
          </div>
        </div>
      </div>
    </template>
  </SurfaceCard>
</template>

<style scoped>
.trackers__error {
  margin-bottom: 14px;
}

.trackers__bound {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.trackers__bound + .trackers__add,
.trackers__bound + :deep(.empty) {
  margin-top: 16px;
}

.trackers__add-heading {
  margin: 0 0 10px;
  font-family: var(--font-display);
  font-weight: var(--weight-bold);
  font-size: var(--text-base);
  color: var(--text);
}

.trackers__empty {
  margin: 10px 0 0;
  padding: 10px 0;
  text-align: center;
  font-size: 13px;
  color: var(--muted);
}

.add-row {
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  background: var(--surface);
  overflow: hidden;
}

.add-row + .add-row {
  margin-top: 8px;
}

.add-row__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
  padding: 11px 14px;
  border: none;
  background: transparent;
  color: var(--text);
  font-family: var(--font-sans);
  cursor: pointer;
  text-align: left;
}

.add-row__head:hover {
  background: var(--surface2);
}

.add-row__name {
  display: flex;
  align-items: center;
  gap: 8px;
  font-weight: var(--weight-semibold);
  font-size: 13.5px;
}

.add-row__panel {
  padding: 0 14px 14px;
  border-top: 1px solid var(--border);
}

.add-row__searchbar {
  display: flex;
  gap: 8px;
  align-items: center;
  margin-top: 12px;
}

.add-row__search {
  flex: 1;
  min-width: 0;
}

.add-row__private--on {
  color: var(--accentBright);
  border-color: var(--accent);
}

.add-row__error {
  margin-top: 10px;
}

.track-results {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-top: 10px;
  max-height: 320px;
  overflow-y: auto;
}

@media (max-width: 900px) {
  /* The search field + eye toggle + button squeeze on a phone — stack them
   * (QCAT-230/231, mirrors the retired dialog's picker row). */
  .add-row__searchbar {
    flex-wrap: wrap;
  }

  .add-row__search {
    flex: 1 0 100%;
  }
}
</style>
