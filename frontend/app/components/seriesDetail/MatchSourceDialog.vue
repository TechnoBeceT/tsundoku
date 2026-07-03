<script setup lang="ts">
import { ref, watch, computed } from 'vue'
import Dialog from '../ui/Dialog.vue'
import AppButton from '../ui/AppButton.vue'
import SearchInput from '../ui/SearchInput.vue'
import Spinner from '../ui/Spinner.vue'
import ErrorBanner from '../ui/ErrorBanner.vue'
import SearchGroupCard from '../import/SearchGroupCard.vue'
import CandidateConfigRow from '../import/CandidateConfigRow.vue'
import type { SearchCandidate, SearchGroup } from '../screens/import.types'

/**
 * MatchSourceDialog — the Series-Detail "Match source" dialog: the inverse of
 * removing a source. Search by the series' OWN title (it's already imported —
 * this is NOT the Scan-Library path-based match step), pick one cross-source
 * group, choose exactly one of its candidates, set the priority to assign it,
 * and confirm.
 *
 * Presentation-only, mirroring `ExtensionPreferencesDialog`: built on
 * `ui/Dialog.vue`, all data arrives via props and every network-touching
 * action (`search`, `confirm`) is emitted for the parent's `useMatchSource`
 * composable to run. The two-stage flow (search → pick) is owned locally as
 * plain refs — the same shape `Import.vue` uses for its own multi-stage flow,
 * even though that component is likewise props-in/emits-out.
 *
 *   - `open` (v-model:open): whether the dialog is shown.
 *   - `seriesTitle`: the series' own title — prefills the search box and is
 *     restored every time the dialog re-opens.
 *   - `groups`: the current cross-source search results.
 *   - `searching`: a search is in flight (spinner + disabled Search button).
 *   - `saving`: the addProvider POST is in flight — spins + disables the
 *     confirm button and blocks the dialog from being dismissed (§16).
 *   - `error`: a search-or-add failure message, or null for none.
 *
 * Resets its local flow state (query/stage/pick/importance) every time it
 * opens, so a re-open never inherits a stale selection (mirrors
 * `DeleteSeriesDialog`'s reset-on-open).
 *
 * Emits `update:open` (v-model), `search` (the trimmed query string), and
 * `confirm` (the chosen `{source, mangaId, importance}`).
 */
const props = withDefaults(defineProps<{
  /** Whether the dialog is shown (v-model:open). */
  open: boolean
  /** The series' own title — prefills the search box. */
  seriesTitle?: string
  /** The current cross-source search results. */
  groups?: SearchGroup[]
  /** A search is in flight. */
  searching?: boolean
  /** The addProvider POST is in flight. */
  saving?: boolean
  /** A search-or-add failure message, or null for none. */
  error?: string | null
}>(), {
  seriesTitle: '',
  groups: () => [],
  searching: false,
  saving: false,
  error: null,
})

const emit = defineEmits<{
  /** The open state changed (v-model:open). */
  'update:open': [value: boolean]
  /** Run a search for the trimmed query. */
  'search': [q: string]
  /** Attach the chosen candidate at the given priority. */
  'confirm': [payload: { source: string, mangaId: number, importance: number }]
}>()

/** Stable identity for a candidate (a source can appear once per group). */
const candKey = (c: SearchCandidate): string => `${c.source}:${c.mangaId}`

const query = ref(props.seriesTitle)
const stage = ref<'search' | 'pick'>('search')
const searched = ref(false)
const pickedGroup = ref<SearchGroup | null>(null)
const selectedKey = ref<string | null>(null)
const importance = ref(2)

// Reset the whole flow every time the dialog opens — a re-open never inherits
// a stale query, pick, or importance value.
watch(() => props.open, (isOpen) => {
  if (isOpen) {
    query.value = props.seriesTitle
    stage.value = 'search'
    searched.value = false
    pickedGroup.value = null
    selectedKey.value = null
    importance.value = 2
  }
})

const noResults = computed(() => searched.value && !props.searching && props.groups.length === 0)

function runSearch(): void {
  searched.value = true
  emit('search', query.value.trim())
}

function pickGroup(group: SearchGroup): void {
  pickedGroup.value = group
  selectedKey.value = null
  stage.value = 'pick'
}

function back(): void {
  stage.value = 'search'
  pickedGroup.value = null
  selectedKey.value = null
}

function toggleCandidate(key: string): void {
  selectedKey.value = selectedKey.value === key ? null : key
}

interface CandRow {
  key: string
  candidate: SearchCandidate
  selected: boolean
}

// A single-selection view of the picked group's candidates — only ever one
// row selected at a time (this dialog attaches exactly one new source).
const candRows = computed<CandRow[]>(() => {
  const group = pickedGroup.value
  if (!group) return []
  return group.candidates.map(c => ({
    key: candKey(c),
    candidate: c,
    selected: selectedKey.value === candKey(c),
  }))
})

const canConfirm = computed(() => selectedKey.value !== null && importance.value >= 1 && !props.saving)

function confirm(): void {
  const group = pickedGroup.value
  if (!group || !canConfirm.value) return
  const candidate = group.candidates.find(c => candKey(c) === selectedKey.value)
  if (!candidate) return
  emit('confirm', { source: candidate.source, mangaId: candidate.mangaId, importance: importance.value })
}

function onBackOrCancel(): void {
  if (stage.value === 'pick') back()
  else emit('update:open', false)
}
</script>

<template>
  <Dialog
    :open="open"
    :busy="saving"
    title="Match a source"
    @update:open="emit('update:open', $event)"
  >
    <ErrorBanner v-if="error" class="match__error" :message="error" :dismissible="false" />

    <!-- ============= Search stage ============= -->
    <section v-if="stage === 'search'" class="match-stage">
      <div class="match-search">
        <SearchInput
          v-model="query"
          class="match-search__field"
          :clearable="false"
          placeholder="Search a title across sources…"
          @enter="runSearch"
        />
        <AppButton variant="primary" :loading="searching" @click="runSearch">
          Search
        </AppButton>
      </div>

      <div v-if="searching" class="match-loading">
        <Spinner :size="16" tone="accent" />
        Searching sources…
      </div>
      <p v-else-if="noResults" class="match-note">No matches found. Try another title.</p>

      <div v-if="!searching && groups.length" class="match-groups">
        <SearchGroupCard
          v-for="g in groups"
          :key="g.title"
          :group="g"
          @pick="pickGroup"
        />
      </div>
    </section>

    <!-- ============= Pick stage ============= -->
    <section v-else class="match-stage">
      <p class="match-eyebrow">Choose the source to attach</p>

      <CandidateConfigRow
        v-for="row in candRows"
        :key="row.key"
        :candidate="row.candidate"
        :selected="row.selected"
        :rank="1"
        :can-up="false"
        :can-down="false"
        :inspecting="false"
        :inspected="false"
        :chapters="[]"
        @toggle="toggleCandidate(row.key)"
      />

      <label class="match-importance">
        <span class="match-importance__label">Priority</span>
        <input
          v-model.number="importance"
          type="number"
          min="1"
          class="match-importance__input"
        >
        <span class="match-importance__hint">
          Higher number = higher priority — set above your existing sources to prefer this one.
        </span>
      </label>
    </section>

    <template #actions>
      <AppButton variant="ghost" :disabled="saving" @click="onBackOrCancel">
        {{ stage === 'pick' ? 'Back' : 'Cancel' }}
      </AppButton>
      <AppButton
        v-if="stage === 'pick'"
        variant="primary"
        :loading="saving"
        :disabled="!canConfirm"
        @click="confirm"
      >
        Attach source
      </AppButton>
    </template>
  </Dialog>
</template>

<style scoped>
.match__error {
  margin-bottom: 14px;
}

.match-stage {
  display: block;
}

.match-search {
  display: flex;
  gap: 10px;
  margin-bottom: 14px;
}

.match-search__field {
  flex: 1;
}

.match-loading {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 10px;
  padding: 30px 0;
  color: var(--muted);
  font-size: var(--text-base);
}

.match-note {
  margin: 0;
  padding: 26px 0;
  text-align: center;
  font-size: 13.5px;
  color: var(--muted);
}

.match-groups {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.match-eyebrow {
  margin: 0 0 11px;
  font-size: var(--text-xs);
  font-weight: var(--weight-extrabold);
  text-transform: uppercase;
  letter-spacing: var(--tracking-label);
  color: var(--faint);
}

.match-importance {
  display: block;
  margin-top: 14px;
}

.match-importance__label {
  display: block;
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  letter-spacing: var(--tracking-label);
  text-transform: uppercase;
  color: var(--faint);
  margin-bottom: 7px;
}

.match-importance__input {
  width: 90px;
  padding: 9px 11px;
  border-radius: var(--radius-md);
  border: 1px solid var(--border2);
  background: var(--bg2);
  color: var(--text);
  font-family: var(--font-sans);
  font-size: var(--text-base);
  outline: none;
}

.match-importance__input:focus {
  border-color: var(--accent);
  box-shadow: var(--ring-focus);
}

.match-importance__hint {
  display: block;
  margin-top: 7px;
  font-size: 12px;
  color: var(--faint);
}
</style>
