<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import AppButton from '../ui/AppButton.vue'
import FormError from '../ui/FormError.vue'
import IconButton from '../ui/IconButton.vue'
import ScoreSelector from '../ui/ScoreSelector.vue'
import TextField from '../ui/TextField.vue'
import Toggle from '../ui/Toggle.vue'
import TrackerIcon from '../ui/TrackerIcon.vue'
import type { TrackBinding, UpdateTrackPatch } from '../screens/seriesDetail.types'
import { scoreSelectorFormat, scoreToDisplay, scoreToNative } from '../../utils/scoreFormat'

/**
 * TrackerBindingRow — one bound tracker's row + its inline manual-edit form,
 * extracted from the retired `TrackingDialog` (QCAT-234) so the Series-Detail
 * Trackers section can compose it directly (QCAT-237 — reuse, don't rewrite).
 * Shows the tracker's brand logo (`TrackerIcon`) + the binding's remote title
 * + `status · progress · Score · Private` summary, an Edit/Refresh/Unbind
 * action row, and — when `editing` — the status/last-chapter-read/score/
 * dates/private form (`ScoreSelector`/`Toggle`/`TextField`, exactly as the
 * dialog rendered them).
 *
 * Presentation-only + parent-owned open/close: `editing` is driven by the
 * PARENT (TrackersSection), which enforces "at most one row open at a time"
 * across the whole bound list and owns the busy→idle auto-close watcher — a
 * single row has no visibility into its siblings. Local state here is only
 * the edit DRAFT (status/lastChapterRead/score/dates/private), reseeded from
 * `binding` every time `editing` flips true so a re-open never shows a stale
 * draft from a previous edit attempt.
 *
 *   - `binding`: the bound tracker to render.
 *   - `editing`: whether this row's inline edit form is open.
 *   - `updateBusy`/`updateError`: this row's manual-edit mutation state.
 *   - `unbindBusy`/`unbindError`: this row's unbind mutation state.
 *   - `refreshBusy`/`refreshError`: this row's remote-refresh mutation state.
 *
 * §16: `unbindError`/`refreshError` are rendered inline via `FormError`
 * (reused, QCAT-237) below the head row — unlike the edit form's error, unbind
 * and refresh have no persistent "open" affordance to attach the message to,
 * so it's shown directly under the row until the next attempt (success clears
 * it; the PARENT, `TrackersSection`, is the one that scopes it to THIS row).
 *
 * Emits `toggleEdit` (Edit icon pressed), `cancelEdit` (form Cancel pressed),
 * `submit` (Save pressed — carries the changed-fields-only patch), `unbind`,
 * `refresh`.
 */
const props = withDefaults(defineProps<{
  /** The bound tracker to render. */
  binding: TrackBinding
  /** Whether this row's inline edit form is open. */
  editing?: boolean
  /** True while this row's manual edit is in flight. */
  updateBusy?: boolean
  /** A failed manual-edit message for this row, or null for none. */
  updateError?: string | null
  /** True while this row's unbind is in flight. */
  unbindBusy?: boolean
  /** A failed unbind message for this row, or null for none. */
  unbindError?: string | null
  /** True while this row's remote refresh is in flight. */
  refreshBusy?: boolean
  /** A failed remote-refresh message for this row, or null for none. */
  refreshError?: string | null
}>(), {
  editing: false,
  updateBusy: false,
  updateError: null,
  unbindBusy: false,
  unbindError: null,
  refreshBusy: false,
  refreshError: null,
})

const emit = defineEmits<{
  /** The row's Edit (pencil) icon was pressed. */
  toggleEdit: []
  /** The edit form's Cancel was pressed. */
  cancelEdit: []
  /** The edit form's Save was pressed — carries the changed-fields-only patch. */
  submit: [patch: UpdateTrackPatch]
  /** Unbind was pressed. */
  unbind: []
  /** Refresh (re-pull remote entry) was pressed. */
  refresh: []
}>()

/** "12 / 24 ch" once a total is known, else just the read count. */
const progressLabel = computed(() => {
  const b = props.binding
  return b.totalChapters > 0 ? `${b.lastChapterRead} / ${b.totalChapters} ch` : `${b.lastChapterRead} ch`
})

// ---- Edit draft (reseeded from `binding` on every open) --------------------
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

/** Seeds every draft field from the binding's current values. `editScore` is
 *  seeded on the DISPLAY scale (`scoreToDisplay`), not the raw stored native
 *  value — see `utils/scoreFormat.ts`'s module doc for the score-scale bug
 *  this guards against. */
function seedDraft(b: TrackBinding): void {
  editStatus.value = b.status
  editLastChapterRead.value = String(b.lastChapterRead)
  editScore.value = scoreToDisplay(b.score, b.scoreFormat)
  editStartDate.value = toDateInput(b.startDate)
  editFinishDate.value = toDateInput(b.finishDate)
  editPrivate.value = b.private
}

watch(() => props.editing, (isEditing) => {
  if (isEditing) seedDraft(props.binding)
}, { immediate: true })

/** Builds a patch of ONLY the fields that differ from the binding's current
 *  values — the backend leaves an omitted field unchanged, so an untouched
 *  field must never be sent. `editScore` is on the DISPLAY scale, so it is
 *  converted back to the binding's native scale (`scoreToNative`) before
 *  comparing/sending. */
const patch = computed<UpdateTrackPatch>(() => {
  const b = props.binding
  const result: UpdateTrackPatch = {}
  if (editStatus.value !== b.status) result.status = editStatus.value
  const lastChapterRead = Number(editLastChapterRead.value)
  if (Number.isFinite(lastChapterRead) && lastChapterRead !== b.lastChapterRead) result.lastChapterRead = lastChapterRead
  const nativeScore = scoreToNative(editScore.value, b.scoreFormat)
  if (nativeScore !== b.score) result.score = nativeScore
  const startDate = fromDateInput(editStartDate.value)
  if (startDate !== b.startDate) result.startDate = startDate
  const finishDate = fromDateInput(editFinishDate.value)
  if (finishDate !== b.finishDate) result.finishDate = finishDate
  if (editPrivate.value !== b.private) result.private = editPrivate.value
  return result
})

const hasChanges = computed(() => Object.keys(patch.value).length > 0)

function submitEdit(): void {
  if (!hasChanges.value) return
  emit('submit', patch.value)
}
</script>

<template>
  <div class="track-bound__group">
    <div class="track-bound__row">
      <div class="track-bound__body">
        <p class="track-bound__tracker">
          <TrackerIcon :tracker-id="binding.trackerId" :size="14" />
          {{ binding.trackerName }}
        </p>
        <p v-if="binding.title" class="track-bound__title">{{ binding.title }}</p>
        <p class="track-bound__meta">
          {{ binding.status }} · {{ progressLabel }}
          <span v-if="binding.score > 0"> · Score {{ binding.score }}</span>
          <span v-if="binding.private"> · Private</span>
        </p>
      </div>
      <div class="track-bound__actions">
        <!-- eslint-disable-next-line vue/attribute-hyphenation -- camelCase :ariaLabel binds the REQUIRED prop; kebab :aria-label routes to the native attr, leaving it unset (vue-tsc error). -->
        <IconButton :ariaLabel="`Edit ${binding.trackerName} entry`" :disabled="updateBusy" @click="emit('toggleEdit')">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M12 20h9M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4Z" />
          </svg>
        </IconButton>
        <!-- eslint-disable-next-line vue/attribute-hyphenation -- camelCase :ariaLabel binds the REQUIRED prop; kebab :aria-label routes to the native attr, leaving it unset (vue-tsc error). -->
        <IconButton :ariaLabel="`Refresh ${binding.trackerName}`" :disabled="refreshBusy" @click="emit('refresh')">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M21 12a9 9 0 1 1-2.64-6.36M21 4v6h-6" />
          </svg>
        </IconButton>
        <AppButton variant="danger-ghost" size="sm" :loading="unbindBusy" @click="emit('unbind')">
          Unbind
        </AppButton>
      </div>
    </div>

    <!-- §16: unbind/refresh have no persistent "open" affordance to attach the
         error to, so it renders directly under the row (no form to nest inside). -->
    <FormError v-if="unbindError" class="track-bound__error" :message="unbindError" />
    <FormError v-if="refreshError" class="track-bound__error" :message="refreshError" />

    <!-- Inline edit form — mirrors the retired TrackingDialog's Phase 4 sheet. -->
    <form v-if="editing" class="track-edit" @submit.prevent="submitEdit">
      <FormError v-if="updateError" class="track-edit__error" :message="updateError" />

      <div class="track-edit__row">
        <TextField v-model="editStatus" label="Status" :disabled="updateBusy" />
        <TextField v-model="editLastChapterRead" type="number" label="Last chapter read" :disabled="updateBusy" />
      </div>

      <div class="track-edit__field">
        <span class="track-edit__label">Score</span>
        <ScoreSelector v-model="editScore" :format="scoreSelectorFormat(binding.scoreFormat)" :disabled="updateBusy" />
      </div>

      <div class="track-edit__row">
        <TextField v-model="editStartDate" type="date" label="Started" :disabled="updateBusy" />
        <TextField v-model="editFinishDate" type="date" label="Finished" :disabled="updateBusy" />
      </div>

      <div class="track-edit__field track-edit__field--inline">
        <span class="track-edit__label">Private</span>
        <!-- eslint-disable-next-line vue/attribute-hyphenation -- camelCase :ariaLabel binds the REQUIRED prop; kebab :aria-label routes to the native attr, leaving it unset (vue-tsc error). -->
        <Toggle v-model="editPrivate" :ariaLabel="'Private entry'" :disabled="updateBusy" />
      </div>

      <div class="track-edit__actions">
        <AppButton variant="ghost" size="sm" type="button" :disabled="updateBusy" @click="emit('cancelEdit')">
          Cancel
        </AppButton>
        <AppButton
          variant="solid"
          size="sm"
          type="submit"
          :loading="updateBusy"
          :disabled="!hasChanges"
        >
          Save
        </AppButton>
      </div>
    </form>
  </div>
</template>

<style scoped>
.track-bound__group {
  display: flex;
  flex-direction: column;
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
  display: flex;
  align-items: center;
  gap: 5px;
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

.track-bound__error {
  margin-top: 6px;
}

.track-bound__actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: none;
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

@media (max-width: 900px) {
  /* The edit form's paired fields (status/last-read, start/finish dates)
   * squeeze on a phone — stack them full-width (QCAT-230/231). */
  .track-edit__row {
    flex-direction: column;
  }
}
</style>
