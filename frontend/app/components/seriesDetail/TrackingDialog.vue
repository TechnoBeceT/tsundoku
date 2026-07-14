<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import AppButton from '../ui/AppButton.vue'
import Dialog from '../ui/Dialog.vue'
import ErrorBanner from '../ui/ErrorBanner.vue'
import FormError from '../ui/FormError.vue'
import IconButton from '../ui/IconButton.vue'
import SearchInput from '../ui/SearchInput.vue'
import SelectField from '../ui/SelectField.vue'
import type { SelectOption } from '../ui/forms.types'
import type { TrackBinding, TrackSearchResult } from '../screens/seriesDetail.types'
import type { TrackerStatus } from '../screens/settings.types'

/**
 * TrackingDialog — the Series-Detail "Trackers" panel (Phase 3d: connect + bind
 * + show — the full status/score/dates EDIT sheet is Phase 4). Opened by
 * `RichSeriesCard`'s additive `openTrackers` button (bubbled via
 * `SeriesDetail`'s `requestTracking`).
 *
 * Two sections:
 *   - Bound trackers: each binding's remote title/status/progress/score
 *     (READ-ONLY, the tracker's own native vocabulary/scale — never
 *     normalized, spec §2), a per-row refresh (re-pull the remote entry) and
 *     Unbind action.
 *   - "Add tracker": pick a CONNECTED tracker this series isn't already bound
 *     to, search its catalog, and Bind a result. Hidden behind a hint when no
 *     tracker is connected-and-unbound (nothing to add).
 *
 * Presentation-only: all data arrives via props and every network-touching
 * action is emitted, mirroring MetadataIdentifyModal/MatchSourceDialog. Resets
 * the local query + tracker picker on every open (mirrors the other
 * series-detail dialogs' reset-on-open) — a re-open never inherits a stale
 * search.
 *
 *   - `open` (v-model:open): whether the dialog is shown.
 *   - `bindings`: this series' current tracker bindings.
 *   - `trackers`: every registered tracker's connect status — filtered here to
 *     "connected AND not already bound to this series" for the picker.
 *   - `pending`/`error`: the bindings list load state.
 *   - `searchResults`/`searching`/`searchError`: the "Add tracker" search.
 *   - `binding`/`bindError`: the bind POST in flight.
 *   - `unbindBusyId`/`unbindError`: which binding's unbind is in flight.
 *   - `refreshBusyId`: which binding's remote re-pull is in flight.
 *
 * Emits `update:open` (v-model), `search` (`{ trackerId, q }`), `bind`
 * (`{ trackerId, remoteId }`), `unbind` (the TrackBinding id — always a
 * LOCAL-ONLY unbind; a "also remove from the tracker's account" affordance is
 * a Phase-4 nicety), `refresh` (the TrackBinding id).
 */
const props = withDefaults(defineProps<{
  /** Whether the dialog is shown (v-model:open). */
  open: boolean
  /** This series' current tracker bindings. */
  bindings?: TrackBinding[]
  /** Every registered tracker's connect status. */
  trackers?: TrackerStatus[]
  /** True while the bindings list is loading. */
  pending?: boolean
  /** A bindings-load failure, or null for none. */
  error?: string | null
  /** The "Add tracker" search results. */
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
})

const emit = defineEmits<{
  /** The open state changed (v-model:open). */
  'update:open': [value: boolean]
  /** Run a search on the given tracker for the trimmed query. */
  'search': [payload: { trackerId: number, q: string }]
  /** Bind the series to the given tracker's remote entry. */
  'bind': [payload: { trackerId: number, remoteId: string }]
  /** Unbind this TrackBinding (local-only; see the doc comment above). */
  'unbind': [recordId: string]
  /** Re-pull this TrackBinding's remote entry. */
  'refresh': [recordId: string]
}>()

// Connected trackers this series isn't already bound to — the only ones
// worth offering in the "Add tracker" picker (an already-bound tracker binds
// again by re-searching from the bound row's own future edit sheet, Phase 4).
const addableTrackers = computed<TrackerStatus[]>(() =>
  props.trackers.filter((t) => t.isLoggedIn && !props.bindings.some((b) => b.trackerId === t.id)))

const trackerOptions = computed<SelectOption[]>(() =>
  addableTrackers.value.map((t) => ({ value: String(t.id), label: t.name })))

const pickerTrackerId = ref('')
const query = ref('')
const searched = ref(false)

watch(() => props.open, (isOpen) => {
  if (!isOpen) return
  pickerTrackerId.value = addableTrackers.value[0] ? String(addableTrackers.value[0].id) : ''
  query.value = ''
  searched.value = false
})

function runSearch(): void {
  const trackerId = Number(pickerTrackerId.value)
  const q = query.value.trim()
  if (!pickerTrackerId.value || !q) return
  searched.value = true
  emit('search', { trackerId, q })
}

function onBind(remoteId: string): void {
  const trackerId = Number(pickerTrackerId.value)
  if (!pickerTrackerId.value) return
  emit('bind', { trackerId, remoteId })
}

/** "12 / 24 ch" once a total is known, else just the read count. */
function progressLabel(b: TrackBinding): string {
  return b.totalChapters > 0 ? `${b.lastChapterRead} / ${b.totalChapters} ch` : `${b.lastChapterRead} ch`
}

const noResults = computed(() => searched.value && !props.searching && props.searchResults.length === 0)
</script>

<template>
  <Dialog :open="open" title="Trackers" max-width="640px" @update:open="emit('update:open', $event)">
    <ErrorBanner v-if="error" class="track__error" :message="error" :dismissible="false" />

    <!-- Loading skeleton -->
    <div v-if="pending" class="track-bound">
      <div v-for="n in 2" :key="n" class="skeleton-row" />
    </div>

    <template v-else>
      <!-- Bound trackers -->
      <div v-if="bindings.length" class="track-bound">
        <div v-for="b in bindings" :key="b.id" class="track-bound__row">
          <div class="track-bound__body">
            <p class="track-bound__tracker">{{ b.trackerName }}</p>
            <p class="track-bound__title">{{ b.title }}</p>
            <p class="track-bound__meta">
              {{ b.status }} · {{ progressLabel(b) }}
              <span v-if="b.score > 0"> · Score {{ b.score }}</span>
            </p>
          </div>
          <div class="track-bound__actions">
            <!-- eslint-disable-next-line vue/attribute-hyphenation -- camelCase :ariaLabel binds the REQUIRED prop; kebab :aria-label routes to the native attr, leaving it unset (vue-tsc error). -->
            <IconButton :ariaLabel="`Refresh ${b.trackerName}`" :disabled="refreshBusyId === b.id" @click="emit('refresh', b.id)">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <path d="M21 12a9 9 0 1 1-2.64-6.36M21 4v6h-6" />
              </svg>
            </IconButton>
            <AppButton
              variant="danger-ghost"
              size="sm"
              :loading="unbindBusyId === b.id"
              @click="emit('unbind', b.id)"
            >
              Unbind
            </AppButton>
          </div>
        </div>
      </div>
      <p v-else class="track-empty">No trackers bound yet.</p>

      <!-- Add tracker -->
      <div class="track-add">
        <p class="track-add__heading">Add tracker</p>
        <p v-if="addableTrackers.length === 0" class="track-add__hint">
          Connect a tracker in Settings → Trackers first.
        </p>
        <template v-else>
          <div class="track-add__row">
            <SelectField v-model="pickerTrackerId" :options="trackerOptions" aria-label="Tracker" />
            <SearchInput v-model="query" class="track-add__search" placeholder="Search title…" :clearable="false" @enter="runSearch" />
            <AppButton variant="solid" size="sm" :loading="searching" :disabled="!query.trim()" @click="runSearch">
              Search
            </AppButton>
          </div>

          <FormError v-if="searchError" class="track-add__error" :message="searchError" />
          <FormError v-if="bindError" class="track-add__error" :message="bindError" />

          <p v-if="noResults" class="track-empty">No matches found.</p>

          <div v-else-if="searchResults.length" class="track-results">
            <div v-for="r in searchResults" :key="r.remoteId" class="track-result">
              <img v-if="r.coverUrl" :src="r.coverUrl" class="track-result__cover" alt="">
              <div v-else class="track-result__cover track-result__cover--placeholder" />
              <div class="track-result__body">
                <p class="track-result__title">{{ r.title }}</p>
                <p class="track-result__meta">
                  {{ r.status }}<span v-if="r.totalChapters > 0"> · {{ r.totalChapters }} ch</span>
                </p>
              </div>
              <AppButton size="sm" variant="mini" :loading="binding" @click="onBind(r.remoteId)">Bind</AppButton>
            </div>
          </div>
        </template>
      </div>
    </template>

    <template #actions>
      <AppButton variant="ghost" @click="emit('update:open', false)">Close</AppButton>
    </template>
  </Dialog>
</template>

<style scoped>
.track__error {
  margin-bottom: 14px;
}

.track-bound {
  display: flex;
  flex-direction: column;
  gap: 10px;
  margin-bottom: 20px;
}

.track-bound__row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 14px;
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  background: var(--surface);
  flex-wrap: wrap;
}

.track-bound__body {
  min-width: 0;
  flex: 1;
}

.track-bound__tracker {
  margin: 0;
  font-size: var(--text-xs);
  font-weight: var(--weight-extrabold);
  text-transform: uppercase;
  letter-spacing: var(--tracking-label);
  color: var(--accentBright);
}

.track-bound__title {
  margin: 2px 0 0;
  font-weight: var(--weight-semibold);
  font-size: 13.5px;
  color: var(--text);
  overflow-wrap: anywhere;
}

.track-bound__meta {
  margin: 2px 0 0;
  font-size: var(--text-sm);
  color: var(--muted);
}

.track-bound__actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: none;
}

.track-empty {
  margin: 0 0 20px;
  padding: 14px 0;
  text-align: center;
  font-size: 13.5px;
  color: var(--muted);
}

.track-add__heading {
  margin: 0 0 10px;
  font-family: var(--font-display);
  font-weight: var(--weight-bold);
  font-size: var(--text-base);
  color: var(--text);
}

.track-add__hint {
  margin: 0;
  font-size: var(--text-sm);
  color: var(--muted);
}

.track-add__row {
  display: flex;
  gap: 8px;
  align-items: center;
  margin-bottom: 10px;
}

.track-add__search {
  flex: 1;
  min-width: 0;
}

.track-add__error {
  margin-bottom: 10px;
}

.track-results {
  display: flex;
  flex-direction: column;
  gap: 8px;
  max-height: 280px;
  overflow-y: auto;
}

.track-result {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px;
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--surface);
}

.track-result__cover {
  width: 34px;
  height: 48px;
  border-radius: var(--radius-xs);
  object-fit: cover;
  flex: none;
  background: var(--surface3);
}

.track-result__cover--placeholder {
  background: var(--surface3);
}

.track-result__body {
  min-width: 0;
  flex: 1;
}

.track-result__title {
  margin: 0;
  font-weight: var(--weight-semibold);
  font-size: 13px;
  color: var(--text);
  overflow-wrap: anywhere;
}

.track-result__meta {
  margin: 2px 0 0;
  font-size: var(--text-xs);
  color: var(--muted);
}

@media (max-width: 900px) {
  /* The picker + search field + button squeeze on a phone — stack them
   * (QCAT-230). */
  .track-add__row {
    flex-direction: column;
    align-items: stretch;
  }
}
</style>
