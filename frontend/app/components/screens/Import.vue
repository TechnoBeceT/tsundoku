<script setup lang="ts">
import { computed, ref, toRef, watch } from 'vue'
import AppButton from '../ui/AppButton.vue'
import Chip from '../ui/Chip.vue'
import SearchInput from '../ui/SearchInput.vue'
import SourceFilterChips from '../ui/SourceFilterChips.vue'
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
  inspect: [payload: { source: string, mangaId: number, url: string }]
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
// The intended synthetic-group title — the picked group's title (`pickGroup`)
// or the largest contributing group's title (`onConfigureTray`). Kept here (not
// read off the composable's `group.title`, which the composable derives from
// the first candidate's own title) so the adopt-payload + Stage-3 title
// FALLBACK (`title || groupTitle`) matches the pre-refactor behavior exactly.
const groupTitle = ref('')
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
  breakdownsResolving,
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
  groupTitle.value = g.title
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
  const seed = suggestedTrayTitle.value ?? tray.value[0]?.title ?? ''
  title.value = seed
  groupTitle.value = seed
  category.value = props.categories[0] ?? 'Other'
  inspectKey.value = null
  inspecting.value = false
  configureTray()
  stage.value = 2
}

const onInspect = (c: SearchCandidate): void => {
  inspectKey.value = candKey(c)
  inspecting.value = true
  emit('inspect', { source: c.source, mangaId: c.mangaId, url: c.url })
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
  if (selectedCount.value > 0 && !breakdownsResolving.value) stage.value = 3
}

const submit = (): void => {
  const g = group.value
  if (!g || props.adopting) return
  const request: AdoptRequest = {
    title: title.value.trim() || groupTitle.value,
    category: category.value,
    providers: reviewRows.value.map(s => ({
      source: s.row.candidate.source,
      mangaId: s.row.candidate.mangaId,
      url: s.row.candidate.url,
      importance: s.importance,
      scanlator: s.row.scanlatorParam,
    })),
  }
  emit('adopt', request)
}
</script>

<template>
  <div class="import">
    <!-- Stepper: Search → Configure → Adopt — short, fixed-content, flows above
         the bounded panel (QCAT-231: never trapped inside a fixed-height/
         overflow-hidden ancestor). Wrapped in its own horizontal scroller as a
         safety net for the shared <Stepper> atom (out of this sweep's scope to
         edit) — see `.import__steps` below. -->
    <div class="import__steps">
      <Stepper :steps="stepItems" :current="currentStep" orientation="horizontal" />
    </div>

    <!-- QCAT-231 "fit the screen, scroll inside": everything from here down fits
         the remaining viewport; `.import__panel` is itself a flex column whose
         per-stage `.imp-stage__scroll` region is the ONE inner scroller, so a
         long search-results/config-rows list never grows the whole page and the
         Back/Next actions stay reachable without scrolling past it. -->
    <div class="import__shell">
      <div class="import__panel">
        <!-- Error banner (search or adopt failure) -->
        <p v-if="error" class="import__error">{{ error }}</p>

        <!-- ================= Stage 1 — Search ================= -->
        <section v-if="stage === 1" class="imp-stage">
          <div class="imp-stage__top">
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
            <SourceFilterChips v-model:selected="srcFilter" :sources="sources" />
          </div>

          <div class="imp-stage__scroll">
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
          </div>

          <div class="imp-actions imp-actions--start">
            <AppButton variant="ghost" @click="emit('cancel')">Cancel</AppButton>
          </div>
        </section>

        <!-- ================= Stage 2 — Configure ================= -->
        <section v-else-if="stage === 2" class="imp-stage">
          <div class="imp-stage__top">
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
          </div>

          <div class="imp-stage__scroll">
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
          </div>

          <div class="imp-actions imp-actions--between">
            <AppButton variant="ghost" @click="back">Back</AppButton>
            <div class="imp-actions__end">
              <span v-if="breakdownsResolving" class="imp-note imp-note--faint">Loading coverage…</span>
              <AppButton
                variant="primary"
                :disabled="selectedCount === 0 || breakdownsResolving"
                @click="toStage3"
              >
                Review →
              </AppButton>
            </div>
          </div>
        </section>

        <!-- ================= Stage 3 — Adopt ================= -->
        <section v-else class="imp-stage">
          <div class="imp-stage__top">
            <div class="imp-review-head">
              <Chip variant="accent">{{ category }}</Chip>
              <span class="imp-review-title">{{ title || groupTitle }}</span>
            </div>

            <p class="imp-eyebrow">Sources · higher importance is preferred</p>
          </div>

          <div class="imp-stage__scroll">
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
          </div>

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
/* QCAT-231 "fit the screen, scroll inside": `.import` is bounded to ONE
 * viewport under the sticky 64px AppShell header (mirrors Downloads/Discover's
 * shape) and is itself a flex column. `.import__steps` is fixed-size and flows
 * naturally above; `.import__shell` takes the rest of the height and is ALSO a
 * flex column so its child `.import__panel` (and each stage's own
 * `.imp-stage__scroll` inside it) can bound + scroll internally — a long
 * search-results or config-rows list scrolls WITHIN the panel, never as an
 * infinite page, and the Back/Next actions stay reachable without hunting for
 * them below the list. Holds at every width (QCAT-230/231) — this wizard is a
 * single column at every breakpoint, so no `@media` re-bound is needed for the
 * scroll shape itself (only for spacing/wrap tweaks below).
 */
.import {
  padding: 20px 30px 20px;
  background: var(--bg);
  height: calc(100dvh - 64px);
  min-height: 0;
  display: flex;
  flex-direction: column;
}

/* ---- Stepper ---------------------------------------------------------------
 * `overflow-x: auto` is a safety net for the shared <Stepper> atom (out of
 * this sweep's scope — `app/components/ui/*`): its horizontal pills are
 * `white-space: nowrap`, so three un-abbreviated step labels can exceed a
 * narrow phone's width. Rather than let that blow out the whole page's
 * horizontal extent (QCAT-230's hard "zero horizontal overflow" gate), the
 * strip contains its own overflow and scrolls locally — a common pattern for
 * small nav/step bars, and harmless at desktop width where it never engages. */
.import__steps {
  flex: none;
  margin-bottom: 16px;
  overflow-x: auto;
  -webkit-overflow-scrolling: touch;
}

.import__shell {
  flex: 1;
  min-height: 0;
  max-width: 880px;
  width: 100%;
  margin: 0 auto;
  display: flex;
  flex-direction: column;
}

/* ---- Panel ------------------------------------------------------------------
 * The bounded region itself: a flex column so the error banner (when present)
 * and the active stage each get their natural/allotted height, and the stage's
 * OWN `.imp-stage__scroll` (see below) is the one inner scroller. */
.import__panel {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-2xl);
  padding: 22px;
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.import__error {
  flex: none;
  margin: 0 0 16px;
  padding: 11px 14px;
  border-radius: var(--radius-lg);
  background: var(--danger-bg);
  border: 1px solid var(--danger-border);
  color: var(--danger-text);
  font-size: var(--text-base);
  font-weight: var(--weight-semibold);
}

/* Each stage fills whatever height `.import__panel` has left and is itself a
 * flex column: `.imp-stage__top` (search/fields/review-head — short,
 * fixed-content) flows at its natural height, `.imp-stage__scroll` takes the
 * rest and is the ONE scroll container for that stage's list, and
 * `.imp-actions` (Back/Next) sits OUTSIDE the scroller so it's never buried
 * below a long list (mirrors Downloads' pinned "Load more"). */
.imp-stage {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.imp-stage__top {
  flex: none;
}

/* 🔴 min-height: 0 is the same flex-item overflow-trap PanelCard/Downloads
 * document: without it this region refuses to shrink below its content (every
 * search group / config row) and the bounded ancestors above would grow
 * instead of scrolling internally. */
.imp-stage__scroll {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
}

/* ---- Actions row ---------------------------------------------------------- */
.imp-actions {
  flex: none;
  display: flex;
  margin-top: 16px;
}

.imp-actions--start {
  justify-content: flex-start;
}

.imp-actions--between {
  justify-content: space-between;
}

.imp-actions__end {
  display: flex;
  align-items: center;
  gap: 10px;
}

@media (max-width: 900px) {
  /* `.imp-actions__end` can carry a "Loading coverage…" note beside the
   * Review button — too wide next to Back on a phone. Wrap the whole row and
   * let the end group wrap onto its own line rather than overflow. */
  .imp-actions--between {
    flex-wrap: wrap;
    gap: 10px;
  }

  .imp-actions__end {
    flex-wrap: wrap;
  }
}

/* ---- Stage 1: search ------------------------------------------------------ */
.imp-search {
  display: flex;
  gap: 10px;
  margin-bottom: 15px;
  flex-wrap: wrap;
}

/* The shared <SearchInput> fills the row beside the Search button; min-width:0
 * lets it shrink below its content size instead of forcing the row to wrap
 * wider than the panel (the flex-item overflow trap again, one level up). */
.imp-search__field {
  flex: 1;
  min-width: 160px;
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
  margin-bottom: 12px;
  flex-wrap: wrap;
}

.imp-review-title {
  font-family: var(--font-display);
  font-weight: var(--weight-black);
  font-size: var(--text-3xl);
  color: var(--text);
  /* A long adopted title (or a CJK title with no break opportunities) must
   * wrap rather than push the panel wider than the viewport (QCAT-230). */
  overflow-wrap: anywhere;
  min-width: 0;
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

@media (max-width: 900px) {
  /* Reclaim horizontal room on a phone — mirrors Discover's mobile padding
   * tightening (390px minus the desktop 30px side padding + 22px panel
   * padding leaves very little room for the search bar/rows/Stepper). */
  .import {
    padding: 14px 12px 14px;
  }

  .import__panel {
    padding: 14px;
  }
}
</style>
