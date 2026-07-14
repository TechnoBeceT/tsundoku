<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import AppButton from '../ui/AppButton.vue'
import Dialog from '../ui/Dialog.vue'
import ErrorBanner from '../ui/ErrorBanner.vue'
import FormError from '../ui/FormError.vue'
import IconButton from '../ui/IconButton.vue'
import ScoreSelector from '../ui/ScoreSelector.vue'
import SearchInput from '../ui/SearchInput.vue'
import SelectField from '../ui/SelectField.vue'
import TextField from '../ui/TextField.vue'
import Toggle from '../ui/Toggle.vue'
import type { SelectOption } from '../ui/forms.types'
import type { TrackBinding, TrackSearchResult, UpdateTrackPatch } from '../screens/seriesDetail.types'
import type { TrackerStatus } from '../screens/settings.types'
import { scoreSelectorFormat, scoreToDisplay, scoreToNative } from '../../utils/scoreFormat'

/**
 * TrackingDialog — the Series-Detail "Trackers" panel. Phase 3d shipped
 * connect + bind + show; Phase 4 adds the per-row manual EDIT sheet
 * (status/last-chapter-read/score/dates/private) and a "Sync now" pull +
 * converge. Opened by `RichSeriesCard`'s additive `openTrackers` button
 * (bubbled via `SeriesDetail`'s `requestTracking`).
 *
 * Two sections:
 *   - Bound trackers: each binding's remote title/status/progress/score
 *     (the tracker's own native vocabulary/scale — never normalized, spec
 *     §2), a per-row Edit (opens an inline edit form, mirrors TrackerRow's
 *     inline credential form), Refresh (re-pull the remote entry), and Unbind.
 *   - "Add tracker": pick a CONNECTED tracker this series isn't already bound
 *     to, search its catalog, and Bind a result. Hidden behind a hint when no
 *     tracker is connected-and-unbound (nothing to add).
 *
 * The edit form is LOCAL UI state (which row is open + its field drafts) —
 * only ONE row can be open at a time. Submitting builds a patch of ONLY the
 * fields that changed from the binding's current values (the backend leaves
 * an omitted field unchanged) and emits `update`; the form auto-closes on the
 * busy→idle FALSE EDGE for its own row when no `updateError` is present
 * (mirrors CategoriesPane's `confirmBusy` watcher) — a failure keeps it open
 * with the error shown inline (§16), a success just lets the row's own props
 * reflect the new values once the parent's composable applies the response.
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
 *   - `updateBusyId`/`updateError`: which binding's manual edit is in flight.
 *   - `syncing`/`syncError`: the "Sync now" pull+converge in flight.
 *
 * Emits `update:open` (v-model), `search` (`{ trackerId, q }`), `bind`
 * (`{ trackerId, remoteId }`), `unbind` (the TrackBinding id — always a
 * LOCAL-ONLY unbind; a "also remove from the tracker's account" affordance is
 * a Phase-4 nicety), `refresh` (the TrackBinding id), `update`
 * (`{ recordId, patch }` — the changed-fields-only edit), `sync` (no payload —
 * pull + converge every binding on this series).
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
  /** Apply a changed-fields-only manual edit to this TrackBinding. */
  'update': [payload: { recordId: string, patch: UpdateTrackPatch }]
  /** Pull + converge every one of this series' tracker bindings. */
  'sync': []
}>()

// Connected trackers this series isn't already bound to — the only ones
// worth offering in the "Add tracker" picker (an already-bound tracker's
// status/score/dates are edited from its own bound row instead).
const addableTrackers = computed<TrackerStatus[]>(() =>
  props.trackers.filter((t) => t.isLoggedIn && !props.bindings.some((b) => b.trackerId === t.id)))

const trackerOptions = computed<SelectOption[]>(() =>
  addableTrackers.value.map((t) => ({ value: String(t.id), label: t.name })))

const pickerTrackerId = ref('')
const query = ref('')
const searched = ref(false)

watch(() => props.open, (isOpen) => {
  closeEdit()
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

// ---- Edit sheet (Phase 4) ---------------------------------------------------
// Local UI state: which bound row's edit form is open (at most one at a time)
// + its field drafts. `editScore` is kept on the DISPLAY scale for whichever
// ScoreSelector shape `b.scoreFormat` resolves to (see `scoreFormat` on
// TrackBinding + `utils/scoreFormat.ts`) — converted to/from the binding's
// STORED NATIVE scale by `scoreToDisplay`/`scoreToNative` in openEdit/
// buildPatch below. This is the fix for the score-scale bug: a fixed 0-10
// control used to send its raw value straight back as the native score,
// writing e.g. 8/100 for an AniList entry instead of 80/100.
const editingId = ref<string | null>(null)
const editStatus = ref('')
const editLastChapterRead = ref('0')
const editScore = ref(0)
const editStartDate = ref('')
const editFinishDate = ref('')
const editPrivate = ref(false)

/** ISO timestamp → a `<input type="date">` value ("" for null/unset). */
function toDateInput(iso: string | null): string {
  return iso ? iso.slice(0, 10) : ''
}

/** A `<input type="date">` value → an ISO timestamp (null for "", i.e. cleared). */
function fromDateInput(value: string): string | null {
  return value ? new Date(value).toISOString() : null
}

/** Opens b's edit form, seeding every draft field from its current values.
 *  editScore is seeded on the DISPLAY scale (scoreToDisplay), not the raw
 *  stored native value — see the field's own doc comment above. */
function openEdit(b: TrackBinding): void {
  editingId.value = b.id
  editStatus.value = b.status
  editLastChapterRead.value = String(b.lastChapterRead)
  editScore.value = scoreToDisplay(b.score, b.scoreFormat)
  editStartDate.value = toDateInput(b.startDate)
  editFinishDate.value = toDateInput(b.finishDate)
  editPrivate.value = b.private
}

function closeEdit(): void {
  editingId.value = null
}

function toggleEdit(b: TrackBinding): void {
  if (editingId.value === b.id) closeEdit()
  else openEdit(b)
}

/** Builds a patch of ONLY the fields that differ from b's current values —
 *  the backend leaves an omitted field unchanged, so an untouched field must
 *  never be sent. editScore is on the DISPLAY scale, so it is converted back
 *  to the binding's native scale (scoreToNative) before comparing/sending —
 *  comparing raw display-vs-native values here would both false-positive a
 *  no-op edit as changed AND re-introduce the score-scale bug on submit. */
function buildPatch(b: TrackBinding): UpdateTrackPatch {
  const patch: UpdateTrackPatch = {}
  if (editStatus.value !== b.status) patch.status = editStatus.value
  const lastChapterRead = Number(editLastChapterRead.value)
  if (Number.isFinite(lastChapterRead) && lastChapterRead !== b.lastChapterRead) patch.lastChapterRead = lastChapterRead
  const nativeScore = scoreToNative(editScore.value, b.scoreFormat)
  if (nativeScore !== b.score) patch.score = nativeScore
  const startDate = fromDateInput(editStartDate.value)
  if (startDate !== b.startDate) patch.startDate = startDate
  const finishDate = fromDateInput(editFinishDate.value)
  if (finishDate !== b.finishDate) patch.finishDate = finishDate
  if (editPrivate.value !== b.private) patch.private = editPrivate.value
  return patch
}

const editHasChanges = computed(() => {
  const b = props.bindings.find((x) => x.id === editingId.value)
  return b ? Object.keys(buildPatch(b)).length > 0 : false
})

function submitEdit(b: TrackBinding): void {
  const patch = buildPatch(b)
  if (Object.keys(patch).length === 0) return
  emit('update', { recordId: b.id, patch })
}

// Auto-close the edit form on the busy→idle FALSE EDGE for its own row, but
// ONLY when no updateError landed (mirrors CategoriesPane's confirmBusy
// watcher) — a failure keeps the form open with the error shown inline.
watch(() => props.updateBusyId, (busyId, prevBusyId) => {
  if (prevBusyId && prevBusyId === editingId.value && busyId === null && !props.updateError) {
    closeEdit()
  }
})
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
      <FormError v-if="syncError" class="track__error" :message="syncError" />
      <div v-if="bindings.length" class="track-bound">
        <div v-for="b in bindings" :key="b.id" class="track-bound__group">
          <div class="track-bound__row">
            <div class="track-bound__body">
              <p class="track-bound__tracker">{{ b.trackerName }}</p>
              <p class="track-bound__title">{{ b.title }}</p>
              <p class="track-bound__meta">
                {{ b.status }} · {{ progressLabel(b) }}
                <span v-if="b.score > 0"> · Score {{ b.score }}</span>
                <span v-if="b.private"> · Private</span>
              </p>
            </div>
            <div class="track-bound__actions">
              <!-- eslint-disable-next-line vue/attribute-hyphenation -- camelCase :ariaLabel binds the REQUIRED prop; kebab :aria-label routes to the native attr, leaving it unset (vue-tsc error). -->
              <IconButton :ariaLabel="`Edit ${b.trackerName} entry`" :disabled="updateBusyId === b.id" @click="toggleEdit(b)">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                  <path d="M12 20h9M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4Z" />
                </svg>
              </IconButton>
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

          <!-- Inline edit form (Phase 4) — mirrors TrackerRow's inline credential form. -->
          <form v-if="editingId === b.id" class="track-edit" @submit.prevent="submitEdit(b)">
            <FormError v-if="updateError" class="track-edit__error" :message="updateError" />

            <div class="track-edit__row">
              <TextField v-model="editStatus" label="Status" :disabled="updateBusyId === b.id" />
              <TextField v-model="editLastChapterRead" type="number" label="Last chapter read" :disabled="updateBusyId === b.id" />
            </div>

            <div class="track-edit__field">
              <span class="track-edit__label">Score</span>
              <ScoreSelector v-model="editScore" :format="scoreSelectorFormat(b.scoreFormat)" :disabled="updateBusyId === b.id" />
            </div>

            <div class="track-edit__row">
              <TextField v-model="editStartDate" type="date" label="Started" :disabled="updateBusyId === b.id" />
              <TextField v-model="editFinishDate" type="date" label="Finished" :disabled="updateBusyId === b.id" />
            </div>

            <div class="track-edit__field track-edit__field--inline">
              <span class="track-edit__label">Private</span>
              <!-- eslint-disable-next-line vue/attribute-hyphenation -- camelCase :ariaLabel binds the REQUIRED prop; kebab :aria-label routes to the native attr, leaving it unset (vue-tsc error). -->
              <Toggle v-model="editPrivate" :ariaLabel="'Private entry'" :disabled="updateBusyId === b.id" />
            </div>

            <div class="track-edit__actions">
              <AppButton variant="ghost" size="sm" type="button" :disabled="updateBusyId === b.id" @click="closeEdit">
                Cancel
              </AppButton>
              <AppButton
                variant="solid"
                size="sm"
                type="submit"
                :loading="updateBusyId === b.id"
                :disabled="!editHasChanges"
              >
                Save
              </AppButton>
            </div>
          </form>
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
      <AppButton
        v-if="bindings.length"
        variant="ghost"
        :loading="syncing"
        :disabled="pending"
        @click="emit('sync')"
      >
        Sync now
      </AppButton>
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

.track-bound__group {
  display: flex;
  flex-direction: column;
}

/* ---- Inline edit form ------------------------------------------------------ */
.track-edit {
  display: flex;
  flex-direction: column;
  gap: 12px;
  margin-top: 6px;
  padding: 14px;
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  background: var(--surface2);
}

.track-edit__error {
  margin-bottom: 0;
}

.track-edit__row {
  display: flex;
  gap: 10px;
}

.track-edit__row > * {
  flex: 1;
  min-width: 0;
}

.track-edit__field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.track-edit__field--inline {
  flex-direction: row;
  align-items: center;
  justify-content: space-between;
}

.track-edit__label {
  font-size: var(--text-xs);
  font-weight: var(--weight-extrabold);
  text-transform: uppercase;
  letter-spacing: var(--tracking-label);
  color: var(--muted);
}

.track-edit__actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
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

  /* The edit form's paired fields (status/last-read, start/finish dates)
   * squeeze the same way — stack them full-width, and let the Dialog's own
   * `overflow-y: auto` (Dialog.vue) provide the inner scroll (QCAT-230/231):
   * the form never grows its own scroll region, it just fits within the
   * dialog's existing one. */
  .track-edit__row {
    flex-direction: column;
  }
}
</style>
