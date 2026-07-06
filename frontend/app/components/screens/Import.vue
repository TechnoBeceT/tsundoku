<script setup lang="ts">
import { computed, ref, toRef, watch } from 'vue'
import AppButton from '../ui/AppButton.vue'
import Chip from '../ui/Chip.vue'
import SearchInput from '../ui/SearchInput.vue'
import Spinner from '../ui/Spinner.vue'
import Stepper from '../ui/Stepper.vue'
import AdoptTray from '../import/AdoptTray.vue'
import ReviewSourceRow from '../import/ReviewSourceRow.vue'
import SearchGroupCard from '../import/SearchGroupCard.vue'
import SourceConfigurePanel from '../import/SourceConfigurePanel.vue'
import { useSourceConfigure } from '~/composables/useSourceConfigure'
import type { StepItem } from '../ui/nav.types'
import {
  candKey,
  type AdoptRequest,
  type ChapterInspect,
  type ScanlatorCoverage,
  type SearchCandidate,
  type SearchGroup,
  type Source,
} from './import.types'

/**
 * Import — the three-stage Adopt flow (Search → Configure → Adopt) for adding a
 * new series to the library. This is a THIN container: it owns only the flow's
 * step + title/category/inspect state via refs, delegates the Configure-stage
 * orchestration (tray, row selection/order, per-scanlator split, rank) to the
 * shared `useSourceConfigure` composable (Slice P), composes the shared atoms
 * (<Stepper>, <SearchInput>, <Chip>, <AppButton>, <Spinner>) + the import
 * organisms (<SearchGroupCard>, <SourceConfigurePanel>, <ReviewSourceRow>), and
 * keeps only the flow/layout CSS. Data (sources, search results, inspect
 * chapters, breakdowns) arrives by props and every outward action (search,
 * inspect, loadBreakdowns, adopt, cancel) is emitted — no fetching, routing, or
 * stores. It references only design tokens, so it reads correctly in both
 * themes.
 *
 * Flow state lives here:
 *  - Stage 1 collects a query + optional source filter, emits `search`, and lists
 *    the returned cross-source `SearchGroup`s. Picking one advances to Stage 2
 *    and emits `loadBreakdowns` for the picked group's candidates (via the
 *    composable's `enterConfigure`). An owned cross-search "adopt tray" (`tray`,
 *    from the composable) accumulates whole groups ACROSS searches —
 *    independent of the `searchResults` prop, which the data layer replaces
 *    wholesale on every new search — so the owner can gather sources for the
 *    same series under several differently-worded queries before configuring.
 *    Both the classic single-group pick (`pickGroup`) and the tray's
 *    "Configure N sources →" (`onConfigureTray`) enter Stage 2 through the same
 *    composable `enterConfigure`, seeding this screen's own `title`/`category`
 *    first.
 *  - Stage 2 renders the composable's `displayRows` (auto-split into one row
 *    per scanlator once a candidate's breakdown resolves with 2+ groups) via
 *    <SourceConfigurePanel>; ranks the selected rows (higher rank = higher
 *    importance, spanning ALL selected rows across every source/scanlator),
 *    edits the title + category, and can `inspect` a candidate's chapter list
 *    (hidden on split rows — coverage is already shown inline). "Review"
 *    advances to Stage 3.
 *  - Stage 3 reviews the resolved providers (importance = rank weight) and emits
 *    the `adopt` request — one `AdoptProvider` per selected row, carrying that
 *    row's `scanlator` (see `DisplayRow`'s `scanlatorParam`) — on success the
 *    parent navigates to the new series.
 */
const props = withDefaults(defineProps<{
  /** Sources available to search (populates the filter chips). */
  sources: Source[]
  /** Cross-source groups returned by the current search. */
  searchResults?: SearchGroup[]
  /** When true, a search is in flight — show the searching spinner. */
  searching?: boolean
  /** When true, a search has run — distinguishes "no matches" from "prompt". */
  searched?: boolean
  /** Chapter preview for the candidate being inspected, or null while loading. */
  inspectChapters?: ChapterInspect[] | null
  /** When true, the adopt request is in flight — disable + spin the submit. */
  adopting?: boolean
  /** A search or adopt error message to surface, or "" for none. */
  error?: string
  /** The owner's dynamic category list; the picker defaults to the first category (owner: first = default). */
  categories?: string[]
  /**
   * Per-scanlator breakdown cache, keyed by `source:mangaId` (mirrors
   * `useImport`'s `breakdowns`). An absent key = not yet fetched (or still in
   * flight) — that candidate renders as a single unchanged row; `null` = the
   * fetch failed — a single row labelled "Coverage unavailable"; a populated
   * array drives the composable's auto-split (see `useSourceConfigure`).
   */
  breakdowns?: Record<string, ScanlatorCoverage[] | null>
}>(), {
  searchResults: () => [],
  searching: false,
  searched: false,
  inspectChapters: null,
  adopting: false,
  error: '',
  categories: () => ['Manga', 'Manhwa', 'Manhua', 'Comic', 'Other'],
  breakdowns: () => ({}),
})

const emit = defineEmits<{
  /** Run a search for `q`, optionally restricted to the given source IDs. */
  search: [payload: { q: string, sources: string[] }]
  /** Fetch the chapter list for one candidate (Stage 2 inspect). */
  inspect: [payload: { source: string, mangaId: number }]
  /** Fetch the per-scanlator breakdown for every candidate in the picked group (Stage 2 entry). */
  loadBreakdowns: [candidates: SearchCandidate[]]
  /** Submit the adopt request (Stage 3). */
  adopt: [request: AdoptRequest]
  /** Abandon the flow (Stage 1 cancel). */
  cancel: []
  /** The active stage changed (1, 2, or 3) — for parent awareness/analytics. */
  step: [stage: number]
}>()

// ---- Owned flow state ------------------------------------------------------
const stage = ref<1 | 2 | 3>(1)
const query = ref('')
const srcFilter = ref<string[]>([])
const hasSearched = ref(props.searched)

const title = ref('')
const category = ref('Other')
// The candidate whose chapter list is being inspected (key), and its loading flag.
const inspectKey = ref<string | null>(null)
const inspecting = ref(false)

// Emit step changes so the parent can react without owning the state.
watch(stage, s => emit('step', s))

// Inspect resolves when the chapter prop arrives for the pending candidate.
watch(() => props.inspectChapters, value => {
  if (value != null) inspecting.value = false
})

// The Configure-stage orchestration (tray, row selection/order, per-scanlator
// split, rank) is owned by the shared composable (Slice P) — this screen keeps
// only title/category + inspect state (Adopt-only concerns; see the
// composable's own ownership-split doc comment).
const {
  tray,
  trayActive,
  isGroupAdded,
  addGroup,
  removeGroup,
  removeCand,
  suggestedTrayTitle,
  configureTray,
  group,
  enterConfigure,
  displayRows,
  orderedKeys,
  selectedCount,
  toggleCand,
  moveCand,
} = useSourceConfigure({
  breakdowns: toRef(props, 'breakdowns'),
  onLoadBreakdowns: c => emit('loadBreakdowns', c),
})

// ---- Stage indicator (drives the shared <Stepper>) -------------------------
const stepItems: StepItem[] = [
  { key: '1', label: 'Search' },
  { key: '2', label: 'Configure' },
  { key: '3', label: 'Adopt' },
]
const currentStep = computed(() => String(stage.value))

// ---- Stage 1: search -------------------------------------------------------
const groups = computed(() => props.searchResults)

const promptSearch = computed(() => !hasSearched.value && !props.searching)
const noResults = computed(() => hasSearched.value && !props.searching && groups.value.length === 0)

const toggleFilter = (id: string): void => {
  srcFilter.value = srcFilter.value.includes(id)
    ? srcFilter.value.filter(x => x !== id)
    : [...srcFilter.value, id]
}

const runSearch = (): void => {
  hasSearched.value = true
  emit('search', { q: query.value.trim(), sources: [...srcFilter.value] })
}

// Picking a group seeds Stage 2 from just that group's candidates (the
// pre-tray classic path — still the fast path when the owner only needs one
// group's sources). `title`/`category`/inspect state are this screen's own
// concern (the composable owns only the tray + row selection/order/split).
const pickGroup = (g: SearchGroup): void => {
  title.value = g.title
  category.value = props.categories[0] ?? 'Other'
  inspectKey.value = null
  inspecting.value = false
  enterConfigure(g.candidates)
  stage.value = 2
}

// "Configure N sources →" — the tray's entry into Stage 2, seeded from every
// tray candidate as one synthetic group. Title defaults to the largest
// contributing group's title, falling back to the first tray candidate's own
// title (mirrors the pre-extraction behavior).
const onConfigureTray = (): void => {
  title.value = suggestedTrayTitle.value ?? tray.value[0]?.title ?? ''
  category.value = props.categories[0] ?? 'Other'
  inspectKey.value = null
  inspecting.value = false
  configureTray()
  stage.value = 2
}

const onInspect = (c: SearchCandidate): void => {
  inspectKey.value = candKey(c)
  inspecting.value = true
  emit('inspect', { source: c.source, mangaId: c.mangaId })
}

// ---- Stage 3: review + adopt -----------------------------------------------
// Importance is derived from rank: the top row gets the highest weight. Spans
// ALL selected rows across every source/scanlator — one global ordered list.
const reviewRows = computed(() => {
  const keys = orderedKeys.value
  const n = keys.length
  return keys.map((k, i) => {
    const row = displayRows.value.find(r => r.key === k)!
    return {
      key: k,
      row,
      rank: i + 1,
      importance: (n - i) * 10,
      preferred: i === 0,
    }
  })
})

const back = (): void => {
  stage.value = (Math.max(1, stage.value - 1)) as 1 | 2 | 3
}

const toStage3 = (): void => {
  if (selectedCount.value > 0) stage.value = 3
}

const submit = (): void => {
  const g = group.value
  if (!g || props.adopting) return
  const request: AdoptRequest = {
    title: title.value.trim() || g.title,
    category: category.value,
    providers: reviewRows.value.map(s => ({
      source: s.row.candidate.source,
      mangaId: s.row.candidate.mangaId,
      importance: s.importance,
      scanlator: s.row.scanlatorParam,
    })),
  }
  emit('adopt', request)
}
</script>

<template>
  <div class="import">
    <div class="import__shell">
      <!-- Stepper: Search → Configure → Adopt -->
      <div class="import__steps">
        <Stepper :steps="stepItems" :current="currentStep" orientation="horizontal" />
      </div>

      <div class="import__panel">
        <!-- Error banner (search or adopt failure) -->
        <p v-if="error" class="import__error">{{ error }}</p>

        <!-- ================= Stage 1 — Search ================= -->
        <section v-if="stage === 1" class="imp-stage">
          <div class="imp-search">
            <SearchInput
              v-model="query"
              class="imp-search__field"
              :clearable="false"
              placeholder="Search a title across sources…"
              @enter="runSearch"
            />
            <AppButton variant="primary" @click="runSearch">
              <template #icon>
                <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                  <circle cx="11" cy="11" r="7" />
                  <path d="M21 21l-4.3-4.3" />
                </svg>
              </template>
              Search
            </AppButton>
          </div>

          <!-- Source filter chips -->
          <div class="imp-filter">
            <span class="imp-filter__label">Limit to:</span>
            <button
              v-for="s in sources"
              :key="s.id"
              type="button"
              class="imp-chip"
              :class="{ 'imp-chip--on': srcFilter.includes(s.id) }"
              @click="toggleFilter(s.id)"
            >
              {{ s.name }}
            </button>
          </div>

          <!-- Searching / empty / prompt states (§16) -->
          <div v-if="searching" class="imp-loading">
            <Spinner :size="16" tone="accent" />
            Searching sources…
          </div>
          <p v-else-if="noResults" class="imp-note imp-note--center">No matches found. Try another title.</p>
          <p v-else-if="promptSearch" class="imp-note imp-note--center imp-note--faint">
            Search a title to find sources to adopt from.
          </p>

          <!-- Cross-search adopt tray: persists across a new search, always above the results -->
          <AdoptTray
            v-if="trayActive"
            :candidates="tray"
            @configure="onConfigureTray"
            @remove="removeCand"
          />

          <!-- Grouped results -->
          <div v-if="!searching && groups.length" class="imp-groups">
            <SearchGroupCard
              v-for="g in groups"
              :key="g.title"
              :group="g"
              tray-enabled
              :added="isGroupAdded(g)"
              :tray-active="trayActive"
              @pick="pickGroup"
              @add="addGroup"
              @remove="removeGroup"
            />
          </div>

          <div class="imp-actions imp-actions--start">
            <AppButton variant="ghost" @click="emit('cancel')">Cancel</AppButton>
          </div>
        </section>

        <!-- ================= Stage 2 — Configure ================= -->
        <section v-else-if="stage === 2" class="imp-stage">
          <div class="imp-fields">
            <label class="imp-field">
              <span class="imp-field__label">Series title</span>
              <input v-model="title" class="imp-input" type="text">
            </label>
            <label class="imp-field imp-field--cat">
              <span class="imp-field__label">Category</span>
              <select v-model="category" class="imp-input imp-input--select">
                <option v-for="c in categories" :key="c" :value="c">{{ c }}</option>
              </select>
            </label>
          </div>

          <SourceConfigurePanel
            :rows="displayRows"
            label="Sources to adopt · use arrows to rank priority"
            :inspect-key="inspectKey"
            :inspecting="inspecting"
            :inspect-chapters="inspectChapters"
            @toggle="toggleCand"
            @move="moveCand($event.key, $event.dir)"
            @inspect="onInspect"
          />

          <div class="imp-actions imp-actions--between">
            <AppButton variant="ghost" @click="back">Back</AppButton>
            <AppButton variant="primary" :disabled="selectedCount === 0" @click="toStage3">
              Review →
            </AppButton>
          </div>
        </section>

        <!-- ================= Stage 3 — Adopt ================= -->
        <section v-else class="imp-stage">
          <div class="imp-review-head">
            <Chip variant="accent">{{ category }}</Chip>
            <span class="imp-review-title">{{ title || (group ? group.title : '') }}</span>
          </div>

          <p class="imp-eyebrow">Sources · higher importance is preferred</p>

          <ReviewSourceRow
            v-for="s in reviewRows"
            :key="s.key"
            :candidate="s.row.candidate"
            :rank="s.rank"
            :importance="s.importance"
            :preferred="s.preferred"
            :scanlator="s.row.scanlator"
          />

          <p class="imp-note imp-explainer">
            All chapters from the preferred source will be queued as <b>wanted</b> and downloaded automatically.
          </p>

          <div class="imp-actions imp-actions--between">
            <AppButton variant="ghost" @click="back">Back</AppButton>
            <AppButton variant="primary" size="lg" :loading="adopting" @click="submit">
              Adopt series
            </AppButton>
          </div>
        </section>
      </div>
    </div>
  </div>
</template>

<style scoped>
.import {
  padding: 28px 30px 70px;
  background: var(--bg);
  min-height: 100%;
}

.import__shell {
  max-width: 880px;
  margin: 0 auto;
}

/* ---- Stepper -------------------------------------------------------------- */
.import__steps {
  margin-bottom: 24px;
}

/* ---- Panel --------------------------------------------------------------- */
.import__panel {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-2xl);
  padding: 22px;
}

.import__error {
  margin: 0 0 16px;
  padding: 11px 14px;
  border-radius: var(--radius-lg);
  background: var(--danger-bg);
  border: 1px solid var(--danger-border);
  color: var(--danger-text);
  font-size: var(--text-base);
  font-weight: var(--weight-semibold);
}

.imp-stage {
  display: block;
}

/* ---- Actions row ---------------------------------------------------------- */
.imp-actions {
  display: flex;
  margin-top: 20px;
}

.imp-actions--start {
  justify-content: flex-start;
}

.imp-actions--between {
  justify-content: space-between;
}

/* ---- Stage 1: search ------------------------------------------------------ */
.imp-search {
  display: flex;
  gap: 10px;
  margin-bottom: 15px;
}

/* The shared <SearchInput> fills the row beside the Search button. */
.imp-search__field {
  flex: 1;
}

.imp-filter {
  display: flex;
  flex-wrap: wrap;
  gap: 7px;
  align-items: center;
  margin-bottom: 20px;
}

.imp-filter__label {
  font-size: var(--text-xs);
  color: var(--faint);
  margin-right: 3px;
  font-weight: var(--weight-semibold);
}

.imp-chip {
  padding: 6px 12px;
  border-radius: var(--radius-pill);
  border: 1px solid var(--border);
  background: var(--surface2);
  color: var(--muted);
  font-family: var(--font-sans);
  font-size: var(--text-sm);
  font-weight: var(--weight-semibold);
  cursor: pointer;
  transition: all 0.15s;
}

.imp-chip--on {
  border-color: var(--accent);
  background: var(--accentSoft);
  color: var(--accentBright);
}

/* ---- Groups --------------------------------------------------------------- */
.imp-groups {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

/* ---- Stage 2: configure --------------------------------------------------- */
.imp-fields {
  display: flex;
  gap: 14px;
  margin-bottom: 20px;
  flex-wrap: wrap;
}

.imp-field {
  flex: 1;
  min-width: 220px;
  display: block;
}

.imp-field--cat {
  flex: none;
  width: 180px;
  min-width: 0;
}

.imp-field__label {
  display: block;
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  letter-spacing: var(--tracking-label);
  text-transform: uppercase;
  color: var(--faint);
  margin-bottom: 7px;
}

.imp-input {
  width: 100%;
  padding: 11px 14px;
  border-radius: var(--radius-lg);
  border: 1px solid var(--border2);
  background: var(--bg2);
  color: var(--text);
  font-family: var(--font-sans);
  font-size: var(--text-md);
  outline: none;
}

.imp-input:focus {
  border-color: var(--accent);
  box-shadow: var(--ring-focus);
}

.imp-input--select {
  font-weight: var(--weight-semibold);
  cursor: pointer;
}

.imp-eyebrow {
  margin: 0 0 11px;
  font-size: var(--text-xs);
  font-weight: var(--weight-extrabold);
  text-transform: uppercase;
  letter-spacing: var(--tracking-label);
  color: var(--faint);
}

/* ---- Stage 3: review ------------------------------------------------------ */
.imp-review-head {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 20px;
  flex-wrap: wrap;
}

.imp-review-title {
  font-family: var(--font-display);
  font-weight: var(--weight-black);
  font-size: var(--text-3xl);
  color: var(--text);
}

.imp-explainer {
  margin-top: 16px;
  background: var(--surface2);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  padding: 12px 14px;
  line-height: 1.5;
  font-size: 12.5px;
  color: var(--muted);
}

.imp-explainer b {
  color: var(--text);
}

/* ---- Notes / loading ------------------------------------------------------ */
.imp-note {
  margin: 0;
  font-size: 13.5px;
  color: var(--muted);
}

.imp-note--center {
  padding: 34px 0;
  text-align: center;
}

.imp-note--faint {
  color: var(--faint);
}

.imp-loading {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 10px;
  padding: 40px 0;
  color: var(--muted);
  font-size: var(--text-base);
}
</style>
