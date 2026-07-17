<script setup lang="ts">
import { computed, ref, toRef, watch } from 'vue'
import AppButton from '../ui/AppButton.vue'
import Chip from '../ui/Chip.vue'
import DisclosurePanel from '../ui/DisclosurePanel.vue'
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
    <!-- Stepper: Search → Configure → Adopt — short, fixed-content, flows in the
         document above the panel. Wrapped in its own horizontal scroller as a
         safety net for the shared <Stepper> atom's nowrap pills (see
         `.import__steps` below). -->
    <div class="import__steps">
      <Stepper :steps="stepItems" :current="currentStep" orientation="horizontal" />
    </div>

    <!-- QCAT-265 GROW: the wizard grows with its content and the PAGE scrolls —
         no letterbox, no per-stage inner-scroller. The stages flow naturally
         inside the centred `.import__panel` card and the Back/Next actions sit at
         the panel's own bottom. -->
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

            <!-- Source filter (QCAT-265 treatment #2 — DISCLOSURE): the source
                 list is 40+ entries in prod (a long list that is IN THE WAY of
                 the search flow), so it collapses to a tap-to-open panel instead
                 of an always-on ~20-row chip cloud — the owner's "exclude
                 sources … open/close that list is more smart". Flat (no second
                 card outline inside the panel); the chips grow when open and the
                 page scrolls (no nested scroll band). -->
            <DisclosurePanel
              class="imp-filter-disclosure"
              collapsible
              flat
              :default-open="false"
              title="Limit to sources"
              :summary="srcFilter.length ? `${srcFilter.length} selected` : ''"
            >
              <SourceFilterChips
                v-model:selected="srcFilter"
                :sources="sources"
                :bounded="false"
                label=""
              />
            </DisclosurePanel>
          </div>

          <div class="imp-stage__body">
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

          <div class="imp-stage__body">
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

          <div class="imp-stage__body">
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
/* The old QCAT-231 letterbox (`height: calc(100dvh - 64px)` + a flex-fill chain
 * bounding each stage's `.imp-stage__scroll` into an inner-scroll region) was
 * experience drift (§0.1): on a large screen the owner was working inside a small
 * letterboxed area, and the Configure stage's config rows felt cramped in the
 * squeezed scroll box. Stripped — no viewport-keyed height, no per-stage
 * inner-scroll: the wizard GROWS with its content and the PAGE scrolls (QCAT-265,
 * the GROW case for a single-column wizard). Spacing is on the fluid token ladder
 * (byte-identical at the 16px anchor: 20px 30px sides, 20px trailing).
 * `--app-nav-bottom` (0 on desktop) clears the phone bottom-nav so the last
 * action/row is never occluded. */
.import {
  padding: var(--space-2xl-tight) var(--space-3xl)
    calc(var(--space-2xl-tight) + var(--app-nav-bottom));
  background: var(--bg);
}

/* ---- Stepper ---------------------------------------------------------------
 * `overflow-x: auto` is a safety net for the shared <Stepper> atom: its
 * horizontal pills are `white-space: nowrap`, so three un-abbreviated step
 * labels can exceed a narrow phone's width. Rather than let that blow out the
 * whole page's horizontal extent (QCAT-230's hard "zero horizontal overflow"
 * gate), the strip contains its own overflow and scrolls locally — a common
 * pattern for small nav/step bars, and harmless at desktop width where it never
 * engages. (Horizontal safety only — NOT the banned vertical viewport-fit.) */
.import__steps {
  margin-bottom: var(--space-lg); /* 16px @16 */
  overflow-x: auto;
  -webkit-overflow-scrolling: touch;
}

/* The centred reading column. `max-width` on rem so the measure (chars/line)
 * stays constant as the fluid root scales the type inside it. */
.import__shell {
  max-width: 55rem; /* 880px @16 */
  width: 100%;
  margin: 0 auto;
}

/* ---- Panel ------------------------------------------------------------------
 * The card the stages flow inside; grows with its content (QCAT-265). */
.import__panel {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-2xl);
  padding: 1.375rem; /* 22px @16 — off-ladder, byte-identical rem literal */
}

.import__error {
  margin: 0 0 var(--space-lg); /* 16px @16 */
  padding: 0.6875rem var(--space-base); /* 11px 14px @16 */
  border-radius: var(--radius-lg);
  background: var(--danger-bg);
  border: 1px solid var(--danger-border);
  color: var(--danger-text);
  font-size: var(--text-base);
  font-weight: var(--weight-semibold);
}

/* Each stage flows in the document (QCAT-265 GROW): `.imp-stage__top`
 * (search/fields/review-head), then `.imp-stage__body` (the stage's list), then
 * `.imp-actions` (Back/Next) at the stage's own bottom. No fixed heights, no
 * inner scroller — the page scrolls.
 *
 * JUDGMENT CALL 2 (owner-review): the wizard action row GROWS with the stage
 * rather than being pinned as a fixed viewport-bottom chrome bar (QCAT-263). The
 * stages are moderate single-column content (a handful of source rows, not a
 * 320-chapter list), so scroll-to-the-bottom-to-progress is the conventional,
 * conservative form-flow shape and keeps desktop closest to the reference. A
 * fixed chrome bar remains available if the owner wants the primary action
 * permanently reachable on a long Configure list. */
.imp-stage__body {
  /* grows with content */
  min-width: 0;
}

/* ---- Actions row ---------------------------------------------------------- */
.imp-actions {
  display: flex;
  margin-top: var(--space-lg); /* 16px @16 */
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
  gap: var(--space-sm); /* 10px @16 */
}

@media (max-width: 900px) {
  /* `.imp-actions__end` can carry a "Loading coverage…" note beside the
   * Review button — too wide next to Back on a phone. Wrap the whole row and
   * let the end group wrap onto its own line rather than overflow. */
  .imp-actions--between {
    flex-wrap: wrap;
    gap: var(--space-sm); /* 10px @16 */
  }

  .imp-actions__end {
    flex-wrap: wrap;
  }
}

/* ---- Stage 1: search ------------------------------------------------------ */
.imp-search {
  display: flex;
  gap: var(--space-sm); /* 10px @16 */
  margin-bottom: 0.9375rem; /* 15px @16 — off-ladder, byte-identical rem literal */
  flex-wrap: wrap;
}

/* The shared <SearchInput> fills the row beside the Search button; min-width:0
 * lets it shrink below its content size instead of forcing the row to wrap
 * wider than the panel (the flex-item overflow trap again, one level up). */
.imp-search__field {
  flex: 1;
  min-width: 10rem; /* 160px @16 */
}

/* ---- Groups --------------------------------------------------------------- */
.imp-groups {
  display: flex;
  flex-direction: column;
  gap: var(--space-md); /* 12px @16 */
}

/* ---- Stage 2: configure --------------------------------------------------- */
.imp-fields {
  display: flex;
  gap: var(--space-base); /* 14px @16 */
  margin-bottom: var(--space-2xl-tight); /* 20px @16 */
  flex-wrap: wrap;
}

.imp-field {
  flex: 1;
  min-width: 13.75rem; /* 220px @16 */
  display: block;
}

.imp-field--cat {
  flex: none;
  width: 11.25rem; /* 180px @16 */
  min-width: 0;
}

.imp-field__label {
  display: block;
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  letter-spacing: var(--tracking-label);
  text-transform: uppercase;
  color: var(--faint);
  margin-bottom: 0.4375rem; /* 7px @16 — off-ladder, byte-identical rem literal */
}

.imp-input {
  width: 100%;
  padding: 0.6875rem var(--space-base); /* 11px 14px @16 */
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
  margin: 0 0 0.6875rem; /* 11px @16 — off-ladder, byte-identical rem literal */
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
  gap: var(--space-md); /* 12px @16 */
  margin-bottom: var(--space-md); /* 12px @16 */
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
  margin-top: var(--space-lg); /* 16px @16 */
  background: var(--surface2);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  padding: var(--space-md) var(--space-base); /* 12px 14px @16 */
  line-height: 1.5;
  font-size: 0.78125rem; /* 12.5px @16 — off-ladder, byte-identical rem literal */
  color: var(--muted);
}

.imp-explainer b {
  color: var(--text);
}

/* ---- Notes / loading ------------------------------------------------------ */
.imp-note {
  margin: 0;
  font-size: 0.84375rem; /* 13.5px @16 — off-ladder, byte-identical rem literal */
  color: var(--muted);
}

.imp-note--center {
  padding: 2.125rem 0; /* 34px @16 */
  text-align: center;
}

.imp-note--faint {
  color: var(--faint);
}

.imp-loading {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-sm); /* 10px @16 */
  padding: 2.5rem 0; /* 40px @16 */
  color: var(--muted);
  font-size: var(--text-base);
}

@media (max-width: 900px) {
  /* QCAT-261 mobile-compact: reclaim horizontal room on a phone (mirrors
   * Discover/Downloads' mobile padding tightening) and clear the fixed phone
   * bottom-nav so the last action/row is never occluded. Desktop unchanged. */
  .import {
    padding: var(--space-base) var(--space-md)
      calc(var(--space-base) + var(--app-nav-bottom)); /* 14px 12px @16 */
  }

  .import__panel {
    padding: var(--space-base); /* 14px @16 */
  }
}
</style>
