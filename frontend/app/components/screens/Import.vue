<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import AppButton from '../ui/AppButton.vue'
import Chip from '../ui/Chip.vue'
import SearchInput from '../ui/SearchInput.vue'
import Spinner from '../ui/Spinner.vue'
import Stepper from '../ui/Stepper.vue'
import CandidateConfigRow from '../import/CandidateConfigRow.vue'
import ReviewSourceRow from '../import/ReviewSourceRow.vue'
import SearchGroupCard from '../import/SearchGroupCard.vue'
import type { StepItem } from '../ui/nav.types'
import type {
  AdoptRequest,
  ChapterInspect,
  ScanlatorCoverage,
  SearchCandidate,
  SearchGroup,
  Source,
} from './import.types'

/**
 * Import — the three-stage Adopt flow (Search → Configure → Adopt) for adding a
 * new series to the library. This is a THIN container: it owns the flow's step +
 * selection state via refs, composes the shared atoms (<Stepper>, <SearchInput>,
 * <Chip>, <AppButton>, <Spinner>) + the import organisms (<SearchGroupCard>,
 * <CandidateConfigRow>, <ReviewSourceRow>), and keeps only the flow/layout CSS.
 * Data (sources, search results, inspect chapters, breakdowns) arrives by props
 * and every outward action (search, inspect, loadBreakdowns, adopt, cancel) is
 * emitted — no fetching, routing, or stores. It references only design tokens,
 * so it reads correctly in both themes.
 *
 * Flow state lives here:
 *  - Stage 1 collects a query + optional source filter, emits `search`, and lists
 *    the returned cross-source `SearchGroup`s. Picking one advances to Stage 2
 *    and emits `loadBreakdowns` for the picked group's candidates.
 *  - Stage 2 auto-splits each candidate whose `breakdowns` entry resolves with
 *    2+ scanlators into one selectable/rankable row per scanlator (a candidate
 *    with 0/1 scanlators, or no/failed breakdown, stays a single row — see
 *    `configRows`); ranks the selected rows (higher rank = higher importance,
 *    spanning ALL selected rows across every source/scanlator), edits the title
 *    + category, and can `inspect` a candidate's chapter list (hidden on split
 *    rows — coverage is already shown inline). "Review" advances to Stage 3.
 *  - Stage 3 reviews the resolved providers (importance = rank weight) and emits
 *    the `adopt` request — one `AdoptProvider` per selected row, carrying that
 *    row's `scanlator` (see `configRows`'s `scanlatorParam`) — on success the
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
   * array drives `configRows`'s auto-split.
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

/** Stable identity for a candidate (a source can appear once per group). */
const candKey = (c: SearchCandidate): string => `${c.source}:${c.mangaId}`

// ---- Owned flow state ------------------------------------------------------
const stage = ref<1 | 2 | 3>(1)
const query = ref('')
const srcFilter = ref<string[]>([])
const hasSearched = ref(props.searched)

const group = ref<SearchGroup | null>(null)
const title = ref('')
const category = ref('Other')
// row key → selected?; `order` holds the selected keys in priority order. A row
// key is `source:mangaId` (unsplit) or `source:mangaId:scanlator` (once a
// candidate's breakdown resolves with 2+ scanlators — see the `breakdowns`
// watch below, which migrates the key(s) in place).
const selected = ref<Record<string, boolean>>({})
const order = ref<string[]>([])
// The candidate whose chapter list is being inspected (key), and its loading flag.
const inspectKey = ref<string | null>(null)
const inspecting = ref(false)
// Candidates (by base `source:mangaId` key) whose breakdown has already been
// split into per-scanlator row keys — guards the watch below from re-splitting
// (and duplicating) the same candidate on every unrelated `breakdowns` update.
const splitApplied = new Set<string>()

// Emit step changes so the parent can react without owning the state.
watch(stage, s => emit('step', s))

// Inspect resolves when the chapter prop arrives for the pending candidate.
watch(() => props.inspectChapters, value => {
  if (value != null) inspecting.value = false
})

/**
 * Once a candidate's breakdown resolves with 2+ scanlators, migrate its single
 * `source:mangaId` row key to one `source:mangaId:scanlator` key per group —
 * spliced into `order` at the same position (preserving rank) and defaulted to
 * the replaced key's own selected state (mirrors `pickGroup`'s "select all"
 * default: an unsplit row selected when it split stays fully selected; one
 * deselected before its breakdown resolved stays fully deselected). A 0/1-
 * scanlator or failed/unloaded breakdown never splits — `configRows` below
 * renders those straight off the unsplit key with no reconciliation needed.
 *
 * Watches BOTH `breakdowns` (the normal case: the fetch resolves after
 * `pickGroup` already ran) AND `group` (the already-cached case: a candidate's
 * breakdown was fetched during an earlier visit to Stage 2 and `breakdowns`
 * already holds 2+ scanlators the moment `pickGroup` sets `group` — a
 * breakdowns-only watch would never re-fire since the prop itself doesn't
 * change on a re-pick).
 */
watch([() => props.breakdowns, group], () => {
  const g = group.value
  if (!g) return
  for (const c of g.candidates) {
    const baseKey = candKey(c)
    if (splitApplied.has(baseKey)) continue
    const bd = props.breakdowns[baseKey]
    if (!bd || bd.length < 2) continue
    splitApplied.add(baseKey)

    const newKeys = bd.map(sc => `${baseKey}:${sc.scanlator}`)
    const wasSelected = !!selected.value[baseKey]
    const idx = order.value.indexOf(baseKey)
    if (idx >= 0) {
      order.value = [
        ...order.value.slice(0, idx),
        ...(wasSelected ? newKeys : []),
        ...order.value.slice(idx + 1),
      ]
    }
    const { [baseKey]: _removed, ...rest } = selected.value
    const nextSelected = { ...rest }
    for (const k of newKeys) nextSelected[k] = wasSelected
    selected.value = nextSelected
  }
}, { deep: true })

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

// Picking a group seeds Stage 2: all candidates selected, in source order, and
// requests the per-scanlator breakdown for each (the auto-split source).
const pickGroup = (g: SearchGroup): void => {
  const keys = g.candidates.map(candKey)
  group.value = g
  title.value = g.title
  category.value = props.categories[0] ?? 'Other'
  selected.value = Object.fromEntries(keys.map(k => [k, true]))
  order.value = keys
  inspectKey.value = null
  inspecting.value = false
  splitApplied.clear()
  stage.value = 2
  emit('loadBreakdowns', g.candidates)
}

// ---- Stage 2: configure ----------------------------------------------------
// The selected rows, in current priority order (drives rank + importance).
const orderedKeys = computed(() => order.value.filter(k => selected.value[k]))
const selectedCount = computed(() => orderedKeys.value.length)

/** One Configure-stage row: either a whole source (unsplit) or one of its scanlators (split). */
interface ConfigRow {
  key: string
  candidate: SearchCandidate
  /** Scanlator subtitle to show under the source name; "" hides it (untagged/unsplit/unavailable). */
  scanlator: string
  /** The value to send as this row's `AdoptProvider.scanlator` ("" = all chapters from the source). */
  scanlatorParam: string
  /** Chapter count for this row's coverage, when the breakdown is available. */
  chapterCount?: number
  /** Human-readable chapter-range string, e.g. "1-90, 92-101". */
  chapterRanges: string
  /** True when this source's breakdown fetch failed (no split, no coverage — "Coverage unavailable"). */
  coverageUnavailable: boolean
  /** True for a per-scanlator split row (2+ scanlators) — hides the Inspect button (coverage is already inline). */
  isSplit: boolean
}

/**
 * One row per candidate, auto-split into one row per scanlator once that
 * candidate's breakdown resolves with 2+ groups (a 0/1-scanlator or
 * unavailable/unloaded breakdown stays a single row — bullet 2 of the
 * auto-split spec). A single-scanlator breakdown whose one group is named
 * after the source itself (the backend's "untagged" convention) resolves to
 * `scanlator: ''`/no subtitle — it's still an "all chapters" provider, not a
 * named filter; a split row's group keeps its own name verbatim even in the
 * rare case one of several groups happens to share the source's name.
 */
const configRows = computed<ConfigRow[]>(() => {
  const g = group.value
  if (!g) return []
  const rows: ConfigRow[] = []
  for (const c of g.candidates) {
    const baseKey = candKey(c)
    const bd = props.breakdowns[baseKey]
    if (bd && bd.length >= 2) {
      for (const sc of bd) {
        // The untagged bucket (SourceBreakdown labels it with the source name)
        // must adopt as scanlator "" — the backend's filterByScanlator keeps
        // only chapters whose Chapter.Scanlator EQUALS the param, and untagged
        // chapters carry "", so sending the source name here would match ZERO
        // chapters (a silently-empty, never-downloading provider). This mirrors
        // the single-group branch's collapse; it applies even inside a 2+-group
        // split where one group IS the source-name bucket.
        const isUntagged = sc.scanlator === c.sourceName
        rows.push({
          key: `${baseKey}:${sc.scanlator}`,
          candidate: c,
          scanlator: isUntagged ? '' : sc.scanlator,
          scanlatorParam: isUntagged ? '' : sc.scanlator,
          chapterCount: sc.count,
          chapterRanges: sc.ranges,
          coverageUnavailable: false,
          isSplit: true,
        })
      }
    }
    else if (bd?.length === 1) {
      const sc = bd[0]!
      const isUntagged = sc.scanlator === c.sourceName
      rows.push({
        key: baseKey,
        candidate: c,
        scanlator: isUntagged ? '' : sc.scanlator,
        scanlatorParam: isUntagged ? '' : sc.scanlator,
        chapterCount: sc.count,
        chapterRanges: sc.ranges,
        coverageUnavailable: false,
        isSplit: false,
      })
    }
    else {
      rows.push({
        key: baseKey,
        candidate: c,
        scanlator: '',
        scanlatorParam: '',
        chapterCount: undefined,
        chapterRanges: '',
        // Only a definite failure (`null`) is "unavailable" — an absent key
        // (not yet fetched / still in flight) shows no coverage line at all.
        coverageUnavailable: bd === null,
        isSplit: false,
      })
    }
  }
  return rows
})

/** `configRows` merged with this row's current selection + rank + inspect state. */
const displayRows = computed(() => {
  const sel = orderedKeys.value
  return configRows.value.map((row) => {
    const idx = sel.indexOf(row.key)
    return {
      ...row,
      selected: !!selected.value[row.key],
      rank: idx + 1,
      canUp: idx > 0,
      canDown: idx >= 0 && idx < sel.length - 1,
      inspected: inspectKey.value === row.key && props.inspectChapters != null && !inspecting.value,
      loadingInspect: inspectKey.value === row.key && inspecting.value,
    }
  })
})

const toggleCand = (key: string): void => {
  const next = { ...selected.value, [key]: !selected.value[key] }
  selected.value = next
  if (next[key]) {
    if (!order.value.includes(key)) order.value = [...order.value, key]
  } else {
    order.value = order.value.filter(k => k !== key)
  }
}

// Move a selected candidate up (-1) or down (+1) within the selected ordering.
const moveCand = (key: string, dir: -1 | 1): void => {
  const sel = orderedKeys.value
  const i = sel.indexOf(key)
  const j = i + dir
  if (i < 0 || j < 0 || j >= sel.length) return
  const full = [...order.value]
  const fi = full.indexOf(sel[i]!)
  const fj = full.indexOf(sel[j]!)
  ;[full[fi], full[fj]] = [full[fj]!, full[fi]!]
  order.value = full
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
    const row = configRows.value.find(r => r.key === k)!
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

          <!-- Grouped results -->
          <div v-if="!searching && groups.length" class="imp-groups">
            <SearchGroupCard
              v-for="g in groups"
              :key="g.title"
              :group="g"
              @pick="pickGroup"
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

          <p class="imp-eyebrow">Sources to adopt · use arrows to rank priority</p>

          <CandidateConfigRow
            v-for="row in displayRows"
            :key="row.key"
            :candidate="row.candidate"
            :selected="row.selected"
            :rank="row.rank"
            :can-up="row.canUp"
            :can-down="row.canDown"
            :inspecting="row.loadingInspect"
            :inspected="row.inspected"
            :chapters="inspectChapters ?? []"
            :hide-inspect="row.isSplit"
            :scanlator="row.scanlator"
            :chapter-count="row.chapterCount"
            :chapter-ranges="row.chapterRanges"
            :coverage-unavailable="row.coverageUnavailable"
            @toggle="toggleCand(row.key)"
            @inspect="onInspect(row.candidate)"
            @move="moveCand(row.key, $event)"
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
