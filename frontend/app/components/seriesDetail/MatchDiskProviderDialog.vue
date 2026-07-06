<script setup lang="ts">
import { ref, watch, computed } from 'vue'
import Dialog from '../ui/Dialog.vue'
import AppButton from '../ui/AppButton.vue'
import SearchInput from '../ui/SearchInput.vue'
import Spinner from '../ui/Spinner.vue'
import ErrorBanner from '../ui/ErrorBanner.vue'
import SearchGroupCard from '../import/SearchGroupCard.vue'
import CandidateConfigRow from '../import/CandidateConfigRow.vue'
import { candKey } from '../screens/import.types'
import type { ScanlatorCoverage, SearchCandidate, SearchGroup } from '../screens/import.types'

/**
 * MatchDiskProviderDialog — the Series-Detail "Match to source" dialog: links
 * an UNLINKED disk-origin provider's already-downloaded chapters to a real
 * Suwayomi source/scanlator WITHOUT re-downloading them (the no-re-download
 * Match, distinct from `MatchSourceDialog`'s add-a-new-source flow — see that
 * component's own doc comment for why they're kept separate).
 *
 * A three-part flow, all owned locally as plain refs (mirrors
 * `MatchSourceDialog`'s two-stage shape, extended with the scanlator pick):
 *   1. `search` stage — search across sources by the series' own title.
 *   2. `pick` stage — choose exactly one candidate source. Selecting one
 *      emits `pickCandidate` so the parent's `useMatchDiskProvider.
 *      loadBreakdown` can fetch its per-scanlator chapter-coverage breakdown.
 *   3. Still within `pick` — once the breakdown resolves, choose exactly one
 *      scanlation group (or the "coverage unavailable" fallback = all
 *      chapters, unsplit) via reused `CandidateConfigRow`s, set the priority,
 *      and confirm.
 *
 * Presentation-only, like `MatchSourceDialog`: all data arrives via props and
 * every network-touching action (`search`, `pickCandidate`, `confirm`) is
 * emitted for the parent composables to run — `useMatchDiskProvider` for
 * search/breakdown, `useSeriesDetail.matchDiskProvider` for the actual link
 * mutation (it reseeds `series` directly from the response, §16).
 *
 * `breakdownRequestedFor` tracks which candidate's breakdown was last
 * requested so the coverage section only renders once the PARENT has started
 * resolving that request (guards the one-tick gap between emitting
 * `pickCandidate` and the parent's `breakdownLoading` prop turning true —
 * without it, a freshly-picked candidate could flash the "coverage
 * unavailable" fallback before the fetch has even started).
 *
 * Resets its whole local flow every time it opens (mirrors
 * `MatchSourceDialog`/`DeleteSeriesDialog`'s reset-on-open) so a re-open never
 * inherits a stale search, pick, or scanlator selection.
 *
 * Emits `update:open` (v-model), `search` (the trimmed query string),
 * `pickCandidate` (the chosen source's `{source, mangaId}`, to trigger the
 * breakdown fetch), and `confirm` (the final `{source, mangaId, scanlator,
 * importance}` to POST).
 */
const props = withDefaults(defineProps<{
  /** Whether the dialog is shown (v-model:open). */
  open: boolean
  /** The series' own title — prefills the search box. */
  seriesTitle?: string
  /** Display label for the unlinked disk-origin group being matched (explains WHAT is being linked). */
  providerLabel?: string
  /** How many existing chapters the disk-origin group carries (the "no re-download" copy). */
  chapterCount?: number
  /** Priority to prefill the confirm step with (defaults to the disk group's own importance). */
  defaultImportance?: number
  /** The current cross-source search results. */
  groups?: SearchGroup[]
  /** A search is in flight. */
  searching?: boolean
  /** The selected candidate's per-scanlator chapter-coverage breakdown, or null (not loaded / failed). */
  breakdown?: ScanlatorCoverage[] | null
  /** The breakdown fetch is in flight. */
  breakdownLoading?: boolean
  /** The match POST is in flight. */
  saving?: boolean
  /** A search-or-match failure message, or null for none. */
  error?: string | null
}>(), {
  seriesTitle: '',
  providerLabel: '',
  chapterCount: 0,
  defaultImportance: 2,
  groups: () => [],
  searching: false,
  breakdown: null,
  breakdownLoading: false,
  saving: false,
  error: null,
})

const emit = defineEmits<{
  /** The open state changed (v-model:open). */
  'update:open': [value: boolean]
  /** Run a search for the trimmed query. */
  'search': [q: string]
  /** A candidate source was picked — load its scanlator breakdown. */
  'pickCandidate': [payload: { source: string, mangaId: number }]
  /** Link the chosen source/scanlator at the given priority. */
  'confirm': [payload: { source: string, mangaId: number, scanlator: string, importance: number }]
}>()

const query = ref(props.seriesTitle)
const stage = ref<'search' | 'pick'>('search')
const searched = ref(false)
const pickedGroup = ref<SearchGroup | null>(null)
const selectedCandidateKey = ref<string | null>(null)
/** The chosen scanlation group; "" = the "coverage unavailable, link all" fallback; null = not yet chosen. */
const selectedScanlator = ref<string | null>(null)
/** Which candidate's breakdown was last requested (see the doc comment above). */
const breakdownRequestedFor = ref<string | null>(null)
const importance = ref(props.defaultImportance)

// Reset the whole flow every time the dialog opens.
watch(() => props.open, (isOpen) => {
  if (isOpen) {
    query.value = props.seriesTitle
    stage.value = 'search'
    searched.value = false
    pickedGroup.value = null
    selectedCandidateKey.value = null
    selectedScanlator.value = null
    breakdownRequestedFor.value = null
    importance.value = props.defaultImportance
  }
})

const noResults = computed(() => searched.value && !props.searching && props.groups.length === 0)

function runSearch(): void {
  searched.value = true
  emit('search', query.value.trim())
}

function pickGroup(group: SearchGroup): void {
  pickedGroup.value = group
  selectedCandidateKey.value = null
  selectedScanlator.value = null
  breakdownRequestedFor.value = null
  stage.value = 'pick'
}

function back(): void {
  stage.value = 'search'
  pickedGroup.value = null
  selectedCandidateKey.value = null
  selectedScanlator.value = null
  breakdownRequestedFor.value = null
}

function onBackOrCancel(): void {
  if (stage.value === 'pick') back()
  else emit('update:open', false)
}

interface CandRow {
  key: string
  candidate: SearchCandidate
  selected: boolean
}

// Single-selection view of the picked group's candidates — exactly one source is matched.
const candRows = computed<CandRow[]>(() => {
  const group = pickedGroup.value
  if (!group) return []
  return group.candidates.map(c => ({
    key: candKey(c),
    candidate: c,
    selected: selectedCandidateKey.value === candKey(c),
  }))
})

const selectedCandidate = computed<SearchCandidate | null>(() =>
  pickedGroup.value?.candidates.find(c => candKey(c) === selectedCandidateKey.value) ?? null,
)

function toggleCandidate(key: string): void {
  if (selectedCandidateKey.value === key) {
    selectedCandidateKey.value = null
    selectedScanlator.value = null
    breakdownRequestedFor.value = null
    return
  }
  selectedCandidateKey.value = key
  selectedScanlator.value = null
  const candidate = pickedGroup.value?.candidates.find(c => candKey(c) === key)
  if (candidate) {
    breakdownRequestedFor.value = key
    emit('pickCandidate', { source: candidate.source, mangaId: candidate.mangaId })
  }
}

// Whether the breakdown section (spinner / rows / fallback) should render at
// all — only once the parent has started resolving THIS candidate's request.
const showBreakdownSection = computed(() =>
  selectedCandidateKey.value !== null && breakdownRequestedFor.value === selectedCandidateKey.value,
)

interface ScanRow {
  key: string
  scanlator: string
  count: number
  ranges: string
  selected: boolean
}

const scanRows = computed<ScanRow[]>(() => {
  if (!props.breakdown) return []
  return props.breakdown.map(sc => ({
    key: sc.scanlator,
    scanlator: sc.scanlator,
    count: sc.count,
    ranges: sc.ranges,
    selected: selectedScanlator.value === sc.scanlator,
  }))
})

/** Toggles a scanlation-group pick; also used for the "" (link-all/fallback) choice. */
function toggleScanlator(scanlator: string): void {
  selectedScanlator.value = selectedScanlator.value === scanlator ? null : scanlator
}

// The backend's importance column is a Go `int` — require a clean integer
// client-side so a decimal never reaches the server as an ugly generic 400
// (mirrors MatchSourceDialog's identical guard).
const canConfirm = computed(() =>
  selectedCandidate.value !== null
  && selectedScanlator.value !== null
  && Number.isInteger(importance.value)
  && importance.value >= 1
  && !props.saving,
)

function confirm(): void {
  const candidate = selectedCandidate.value
  if (!candidate || selectedScanlator.value === null || !canConfirm.value) return
  emit('confirm', {
    source: candidate.source,
    mangaId: candidate.mangaId,
    scanlator: selectedScanlator.value,
    importance: importance.value,
  })
}
</script>

<template>
  <Dialog
    :open="open"
    :busy="saving"
    title="Match to source"
    @update:open="emit('update:open', $event)"
  >
    <ErrorBanner v-if="error" class="match__error" :message="error" :dismissible="false" />

    <p class="match-intro">
      Link these <strong>{{ chapterCount }}</strong> existing chapter{{ chapterCount === 1 ? '' : 's' }}
      <template v-if="providerLabel">from <strong>{{ providerLabel }}</strong></template>
      to a real source — no re-download.
    </p>

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

    <!-- ============= Pick stage (source, then scanlator) ============= -->
    <section v-else class="match-stage">
      <p class="match-eyebrow">Choose the source to link</p>

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
        hide-inspect
        hide-reorder
        @toggle="toggleCandidate(row.key)"
      />

      <div v-if="showBreakdownSection" class="match-breakdown">
        <div v-if="breakdownLoading" class="match-loading">
          <Spinner :size="16" tone="accent" />
          Loading chapter breakdown…
        </div>

        <template v-else-if="breakdown && breakdown.length > 0">
          <p class="match-eyebrow">Pick the scanlation group</p>
          <button
            v-for="row in scanRows"
            :key="row.key"
            type="button"
            class="scan-row"
            :class="{ 'scan-row--on': row.selected }"
            :aria-pressed="row.selected"
            :aria-label="`Toggle ${row.scanlator}`"
            @click="toggleScanlator(row.scanlator)"
          >
            <span class="scan-row__check" :class="{ 'scan-row__check--on': row.selected }">
              <svg v-if="row.selected" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <path d="M20 6L9 17l-5-5" />
              </svg>
            </span>
            <span class="scan-row__meta">
              <span class="scan-row__name">{{ row.scanlator }}</span>
              <span class="scan-row__coverage">{{ row.count }} chapter{{ row.count === 1 ? '' : 's' }} · {{ row.ranges }}</span>
            </span>
          </button>
        </template>

        <button
          v-else
          type="button"
          class="match-fallback"
          :class="{ 'match-fallback--on': selectedScanlator === '' }"
          @click="toggleScanlator('')"
        >
          Coverage unavailable — link all chapters from this source
        </button>
      </div>

      <label v-if="selectedScanlator !== null" class="match-importance">
        <span class="match-importance__label">Priority</span>
        <input
          v-model.number="importance"
          type="number"
          min="1"
          step="1"
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
        Link chapters
      </AppButton>
    </template>
  </Dialog>
</template>

<style scoped>
.match__error {
  margin-bottom: 14px;
}

.match-intro {
  margin: 0 0 16px;
  font-size: 13px;
  line-height: 1.5;
  color: var(--muted);
}

.match-intro strong {
  color: var(--text);
  font-weight: var(--weight-bold);
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

.match-breakdown {
  margin-top: 14px;
}

.scan-row {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
  margin-bottom: 8px;
  padding: 10px 12px;
  border-radius: var(--radius-lg);
  border: 1px solid var(--border);
  background: var(--surface2);
  font-family: var(--font-sans);
  text-align: left;
  cursor: pointer;
  transition: all 0.15s;
}

.scan-row:hover {
  border-color: var(--accent);
}

.scan-row--on {
  border-color: var(--accent);
  background: var(--accentSoft);
}

.scan-row__check {
  width: 20px;
  height: 20px;
  flex: none;
  border-radius: var(--radius-xs);
  border: 1.5px solid var(--border2);
  background: transparent;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--cover-text);
}

.scan-row__check--on {
  border-color: var(--accent);
  background: var(--accent);
}

.scan-row__meta {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.scan-row__name {
  font-size: 13px;
  font-weight: var(--weight-bold);
  color: var(--text);
}

.scan-row__coverage {
  font-size: var(--text-xs);
  color: var(--faint);
  margin-top: 1px;
}

.match-fallback {
  display: block;
  width: 100%;
  padding: 11px 13px;
  border-radius: var(--radius-lg);
  border: 1px solid var(--border2);
  background: var(--surface2);
  color: var(--muted);
  font-family: var(--font-sans);
  font-size: 12.5px;
  font-style: italic;
  text-align: left;
  cursor: pointer;
  transition: all 0.15s;
}

.match-fallback:hover {
  border-color: var(--accent);
}

.match-fallback--on {
  border-color: var(--accent);
  background: var(--accentSoft);
  color: var(--accentBright);
  font-style: normal;
  font-weight: var(--weight-bold);
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
