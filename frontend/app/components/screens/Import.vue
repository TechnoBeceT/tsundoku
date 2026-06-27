<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type {
  AdoptRequest,
  ChapterInspect,
  SearchCandidate,
  SearchGroup,
  Source,
} from './import.types'

/**
 * Import — the three-stage Adopt flow (Search → Configure → Adopt) for adding a
 * new series to the library. The screen OWNS its step + selection state via refs;
 * data (sources, search results, inspect chapters) arrives by props and every
 * outward action (search, inspect, adopt, cancel) is emitted — no fetching,
 * routing, or stores. It references only design tokens, so it reads correctly in
 * both themes.
 *
 * Flow state lives here:
 *  - Stage 1 collects a query + optional source filter, emits `search`, and lists
 *    the returned cross-source `SearchGroup`s. Picking one advances to Stage 2.
 *  - Stage 2 selects which of the group's candidates to adopt, ranks them (higher
 *    rank = higher importance), edits the title + category, and can `inspect` a
 *    candidate's chapter list. "Review" advances to Stage 3.
 *  - Stage 3 reviews the resolved providers (importance = rank weight) and emits
 *    the `adopt` request; on success the parent navigates to the new series.
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
  /** The owner's dynamic category list; the picker defaults to "Other". */
  categories?: string[]
}>(), {
  searchResults: () => [],
  searching: false,
  searched: false,
  inspectChapters: null,
  adopting: false,
  error: '',
  categories: () => ['Manga', 'Manhwa', 'Manhua', 'Comic', 'Other'],
})

const emit = defineEmits<{
  /** Run a search for `q`, optionally restricted to the given source IDs. */
  search: [payload: { q: string, sources: string[] }]
  /** Fetch the chapter list for one candidate (Stage 2 inspect). */
  inspect: [payload: { source: string, mangaId: number }]
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
// candidate key → selected?; `order` holds the selected keys in priority order.
const selected = ref<Record<string, boolean>>({})
const order = ref<string[]>([])
// The candidate whose chapter list is being inspected (key), and its loading flag.
const inspectKey = ref<string | null>(null)
const inspecting = ref(false)

// Emit step changes so the parent can react without owning the state.
watch(stage, s => emit('step', s))

// Inspect resolves when the chapter prop arrives for the pending candidate.
watch(() => props.inspectChapters, value => {
  if (value != null) inspecting.value = false
})

// ---- Stage indicator -------------------------------------------------------
const steps = [
  { n: 1, label: 'Search' },
  { n: 2, label: 'Configure' },
  { n: 3, label: 'Adopt' },
] as const

// A step's pill is "current" when active; its number is filled once reached.
const stepState = (n: number): 'current' | 'done' | 'todo' => {
  if (stage.value === n) return 'current'
  return stage.value > n ? 'done' : 'todo'
}

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

const onQueryKey = (e: KeyboardEvent): void => {
  if (e.key === 'Enter') runSearch()
}

// Picking a group seeds Stage 2: all candidates selected, in source order.
const pickGroup = (g: SearchGroup): void => {
  const keys = g.candidates.map(candKey)
  group.value = g
  title.value = g.title
  category.value = props.categories.includes('Other') ? 'Other' : (props.categories[0] ?? 'Other')
  selected.value = Object.fromEntries(keys.map(k => [k, true]))
  order.value = keys
  inspectKey.value = null
  inspecting.value = false
  stage.value = 2
}

// ---- Stage 2: configure ----------------------------------------------------
// The selected candidates, in current priority order (drives rank + importance).
const orderedKeys = computed(() => order.value.filter(k => selected.value[k]))
const selectedCount = computed(() => orderedKeys.value.length)

// Per-candidate view rows for the configure list (selection + rank affordances).
interface CandRow {
  key: string
  candidate: SearchCandidate
  selected: boolean
  rank: number
  canUp: boolean
  canDown: boolean
  inspected: boolean
}

const candRows = computed<CandRow[]>(() => {
  const g = group.value
  if (!g) return []
  const sel = orderedKeys.value
  return g.candidates.map((c) => {
    const key = candKey(c)
    const idx = sel.indexOf(key)
    return {
      key,
      candidate: c,
      selected: !!selected.value[key],
      rank: idx + 1,
      canUp: idx > 0,
      canDown: idx >= 0 && idx < sel.length - 1,
      inspected: inspectKey.value === key && props.inspectChapters != null && !inspecting.value,
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
// Importance is derived from rank: the top source gets the highest weight.
const reviewSources = computed(() => {
  const g = group.value
  if (!g) return []
  const keys = orderedKeys.value
  const n = keys.length
  return keys.map((k, i) => {
    const c = g.candidates.find(x => candKey(x) === k)!
    return {
      key: k,
      candidate: c,
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
    providers: reviewSources.value.map(s => ({
      source: s.candidate.source,
      mangaId: s.candidate.mangaId,
      importance: s.importance,
    })),
  }
  emit('adopt', request)
}

// ---- Presentation helpers --------------------------------------------------
/** The big faint placeholder letter behind a candidate cover. */
const initial = (text: string): string => (text.trim()[0] ?? '?').toUpperCase()

/** Chapter-row label: "Ch. <number> · <name>" with graceful gaps. */
const chapterLabel = (ch: ChapterInspect): string => {
  const num = ch.number == null ? '—' : String(ch.number)
  return ch.name ? `Ch. ${num} · ${ch.name}` : `Ch. ${num}`
}
</script>

<template>
  <div class="import">
    <div class="import__shell">
      <!-- Stepper: Search → Configure → Adopt -->
      <div class="import__steps">
        <template v-for="(s, i) in steps" :key="s.n">
          <div class="imp-step" :class="`imp-step--${stepState(s.n)}`">
            <span class="imp-step__num">{{ s.n }}</span>{{ s.label }}
          </div>
          <div v-if="i < steps.length - 1" class="import__steps-line" />
        </template>
      </div>

      <div class="import__panel">
        <!-- Error banner (search or adopt failure) -->
        <p v-if="error" class="import__error">{{ error }}</p>

        <!-- ================= Stage 1 — Search ================= -->
        <section v-if="stage === 1" class="imp-stage">
          <div class="imp-search">
            <input
              v-model="query"
              class="imp-search__input"
              type="text"
              placeholder="Search a title across sources…"
              @keydown="onQueryKey"
            >
            <button type="button" class="imp-btn imp-btn--primary" @click="runSearch">
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <circle cx="11" cy="11" r="7" />
                <path d="M21 21l-4.3-4.3" />
              </svg>
              Search
            </button>
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
            <span class="imp-spinner" aria-hidden="true" />
            Searching sources…
          </div>
          <p v-else-if="noResults" class="imp-note imp-note--center">No matches found. Try another title.</p>
          <p v-else-if="promptSearch" class="imp-note imp-note--center imp-note--faint">
            Search a title to find sources to adopt from.
          </p>

          <!-- Grouped results -->
          <div v-if="!searching && groups.length" class="imp-groups">
            <button
              v-for="g in groups"
              :key="g.title"
              type="button"
              class="imp-group"
              @click="pickGroup(g)"
            >
              <div class="imp-group__head">
                <span class="imp-group__title">{{ g.title }}</span>
                <span class="imp-group__count">{{ g.candidates.length }} sources · choose →</span>
              </div>
              <div class="imp-group__cands">
                <div v-for="c in g.candidates" :key="candKey(c)" class="imp-pill">
                  <span class="imp-pill__cover">
                    <img v-if="c.thumbnailUrl" class="imp-pill__img" :src="c.thumbnailUrl" :alt="`${c.title} cover`" loading="lazy">
                    <span v-else class="imp-pill__initial">{{ initial(c.title) }}</span>
                  </span>
                  <span class="imp-pill__meta">
                    <span class="imp-pill__source">{{ c.sourceName }}</span>
                    <span class="imp-pill__lang">{{ c.lang.toUpperCase() }}</span>
                  </span>
                </div>
              </div>
            </button>
          </div>

          <div class="imp-actions imp-actions--start">
            <button type="button" class="imp-btn imp-btn--ghost" @click="emit('cancel')">Cancel</button>
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

          <div
            v-for="row in candRows"
            :key="row.key"
            class="imp-cand"
            :class="{ 'imp-cand--on': row.selected }"
          >
            <div class="imp-cand__row">
              <button
                type="button"
                class="imp-check"
                :class="{ 'imp-check--on': row.selected }"
                :aria-pressed="row.selected"
                :aria-label="`Toggle ${row.candidate.sourceName}`"
                @click="toggleCand(row.key)"
              >
                <svg v-if="row.selected" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                  <path d="M20 6L9 17l-5-5" />
                </svg>
              </button>

              <span class="imp-cand__cover">
                <img v-if="row.candidate.thumbnailUrl" class="imp-cand__img" :src="row.candidate.thumbnailUrl" :alt="`${row.candidate.title} cover`" loading="lazy">
                <span v-else class="imp-cand__initial">{{ initial(row.candidate.title) }}</span>
              </span>

              <span class="imp-cand__meta">
                <span class="imp-cand__source">{{ row.candidate.sourceName }}</span>
                <span class="imp-cand__lang">{{ row.candidate.lang.toUpperCase() }}</span>
              </span>

              <button type="button" class="imp-inspect" @click="onInspect(row.candidate)">
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                  <path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7-10-7-10-7z" />
                  <circle cx="12" cy="12" r="3" />
                </svg>
                Inspect
              </button>

              <div v-if="row.selected" class="imp-rank">
                <button type="button" class="imp-rank__arrow" :disabled="!row.canUp" :aria-label="`Raise ${row.candidate.sourceName}`" @click="moveCand(row.key, -1)">
                  <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                    <path d="M18 15l-6-6-6 6" />
                  </svg>
                </button>
                <span class="imp-rank__num" :class="{ 'imp-rank__num--top': row.rank === 1 }">{{ row.rank }}</span>
                <button type="button" class="imp-rank__arrow" :disabled="!row.canDown" :aria-label="`Lower ${row.candidate.sourceName}`" @click="moveCand(row.key, 1)">
                  <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                    <path d="M6 9l6 6 6-6" />
                  </svg>
                </button>
              </div>
            </div>

            <!-- Inspect preview: loading spinner → chapter list (§16) -->
            <div v-if="inspectKey === row.key && inspecting" class="imp-inspect-panel imp-inspect-panel--loading">
              <span class="imp-spinner" aria-hidden="true" />
              Loading chapters…
            </div>
            <div v-else-if="row.inspected" class="imp-inspect-panel">
              <p class="imp-inspect-panel__count">{{ (inspectChapters ?? []).length }} chapters available</p>
              <ul class="imp-inspect-panel__list">
                <li v-for="(ch, ci) in inspectChapters ?? []" :key="ci" class="imp-inspect-panel__item">
                  {{ chapterLabel(ch) }}
                </li>
              </ul>
            </div>
          </div>

          <div class="imp-actions imp-actions--between">
            <button type="button" class="imp-btn imp-btn--ghost" @click="back">Back</button>
            <button
              type="button"
              class="imp-btn imp-btn--primary"
              :disabled="selectedCount === 0"
              @click="toStage3"
            >
              Review →
            </button>
          </div>
        </section>

        <!-- ================= Stage 3 — Adopt ================= -->
        <section v-else class="imp-stage">
          <div class="imp-review-head">
            <span class="imp-review-cat">{{ category }}</span>
            <span class="imp-review-title">{{ title || (group ? group.title : '') }}</span>
          </div>

          <p class="imp-eyebrow">Sources · higher importance is preferred</p>

          <div v-for="s in reviewSources" :key="s.key" class="imp-review-row">
            <span class="imp-rank__num" :class="{ 'imp-rank__num--top': s.preferred }">{{ s.rank }}</span>
            <span class="imp-review-row__source">{{ s.candidate.sourceName }}</span>
            <span class="imp-review-row__lang">{{ s.candidate.lang.toUpperCase() }}</span>
            <span v-if="s.preferred" class="imp-review-row__preferred">PREFERRED</span>
            <span class="imp-review-row__imp">importance {{ s.importance }}</span>
          </div>

          <p class="imp-note imp-explainer">
            All chapters from the preferred source will be queued as <b>wanted</b> and downloaded automatically.
          </p>

          <div class="imp-actions imp-actions--between">
            <button type="button" class="imp-btn imp-btn--ghost" @click="back">Back</button>
            <button type="button" class="imp-btn imp-btn--primary imp-btn--lg" :disabled="adopting" @click="submit">
              <span v-if="adopting" class="imp-spinner imp-spinner--on-accent" aria-hidden="true" />
              Adopt series
            </button>
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
  display: flex;
  align-items: center;
  gap: 4px;
  margin-bottom: 24px;
}

.import__steps-line {
  flex: 1;
  height: 1px;
  background: var(--border);
}

.imp-step {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 13px;
  border-radius: var(--radius-md);
  background: transparent;
  color: var(--muted);
  font-size: var(--text-base);
  font-weight: var(--weight-bold);
  white-space: nowrap;
}

.imp-step--current {
  background: var(--accentSoft);
  color: var(--accentBright);
}

.imp-step__num {
  width: 21px;
  height: 21px;
  border-radius: 50%;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-size: var(--text-xs);
  font-weight: var(--weight-extrabold);
  background: var(--surface3);
  color: var(--faint);
}

.imp-step--current .imp-step__num,
.imp-step--done .imp-step__num {
  background: var(--accent);
  color: var(--cover-text);
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

/* ---- Buttons (shared) ----------------------------------------------------- */
.imp-btn {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  font-family: var(--font-sans);
  font-weight: var(--weight-bold);
  cursor: pointer;
  transition: filter 0.15s, border-color 0.15s, color 0.15s;
}

.imp-btn--primary {
  padding: 12px 18px;
  border-radius: var(--radius-lg);
  border: none;
  background: linear-gradient(135deg, var(--accent), var(--accentDeep));
  color: var(--cover-text);
  font-size: 13.5px;
}

.imp-btn--primary:hover:not(:disabled) {
  filter: brightness(1.08);
}

.imp-btn--lg {
  padding: 12px 24px;
  font-size: var(--text-md);
  box-shadow: var(--shadow-accent);
}

.imp-btn--primary:disabled {
  background: var(--surface3);
  color: var(--faint);
  cursor: default;
  box-shadow: none;
}

.imp-btn--ghost {
  padding: 10px 18px;
  border-radius: var(--radius-lg);
  border: 1px solid var(--border2);
  background: transparent;
  color: var(--text);
  font-size: var(--text-base);
}

.imp-btn--ghost:hover {
  border-color: var(--accent);
  color: var(--accentBright);
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

.imp-search__input {
  flex: 1;
  padding: 12px 15px;
  border-radius: var(--radius-lg);
  border: 1px solid var(--border2);
  background: var(--bg2);
  color: var(--text);
  font-family: var(--font-sans);
  font-size: var(--text-md);
  outline: none;
}

.imp-search__input:focus {
  border-color: var(--accent);
  box-shadow: var(--ring-focus);
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

.imp-group {
  display: block;
  width: 100%;
  text-align: left;
  border: 1px solid var(--border);
  border-radius: var(--radius-xl);
  padding: 15px;
  cursor: pointer;
  background: var(--surface2);
  transition: border-color 0.15s;
}

.imp-group:hover {
  border-color: var(--accent);
}

.imp-group__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  margin-bottom: 11px;
}

.imp-group__title {
  font-family: var(--font-display);
  font-weight: var(--weight-bold);
  font-size: var(--text-lg);
  color: var(--text);
}

.imp-group__count {
  font-size: var(--text-xs);
  color: var(--accentBright);
  font-weight: var(--weight-bold);
  white-space: nowrap;
}

.imp-group__cands {
  display: flex;
  gap: 9px;
  flex-wrap: wrap;
}

.imp-pill {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 11px;
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--surface);
}

.imp-pill__cover {
  width: 26px;
  height: 34px;
  border-radius: 5px;
  overflow: hidden;
  position: relative;
  flex: none;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--cover-placeholder);
}

.imp-pill__img {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.imp-pill__initial {
  font-family: var(--font-display);
  font-weight: var(--weight-black);
  font-size: var(--text-lg);
  color: var(--disc-initial);
}

.imp-pill__meta {
  display: flex;
  flex-direction: column;
}

.imp-pill__source {
  font-size: 12.5px;
  font-weight: var(--weight-semibold);
  color: var(--text);
}

.imp-pill__lang {
  font-size: 10.5px;
  color: var(--faint);
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

/* ---- Candidate rows ------------------------------------------------------- */
.imp-cand {
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  padding: 11px 13px;
  margin-bottom: 10px;
  background: var(--surface2);
  transition: all 0.15s;
}

.imp-cand--on {
  border-color: var(--accent);
  background: var(--accentSoft);
}

.imp-cand__row {
  display: flex;
  align-items: center;
  gap: 12px;
}

.imp-check {
  width: 22px;
  height: 22px;
  border-radius: var(--radius-xs);
  border: 1.5px solid var(--border2);
  background: transparent;
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  flex: none;
  color: var(--cover-text);
  padding: 0;
}

.imp-check--on {
  border-color: var(--accent);
  background: var(--accent);
}

.imp-cand__cover {
  width: 30px;
  height: 40px;
  border-radius: var(--radius-xs);
  overflow: hidden;
  position: relative;
  flex: none;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--cover-placeholder);
}

.imp-cand__img {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.imp-cand__initial {
  font-family: var(--font-display);
  font-weight: var(--weight-black);
  font-size: var(--text-xl);
  color: var(--disc-initial);
}

.imp-cand__meta {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
}

.imp-cand__source {
  font-size: 13.5px;
  font-weight: var(--weight-bold);
  color: var(--text);
}

.imp-cand__lang {
  font-size: var(--text-xs);
  color: var(--faint);
}

.imp-inspect {
  display: flex;
  align-items: center;
  gap: 5px;
  padding: 6px 10px;
  border-radius: var(--radius-sm);
  border: 1px solid var(--border2);
  background: var(--surface);
  color: var(--muted);
  font-family: var(--font-sans);
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  cursor: pointer;
  flex: none;
  transition: color 0.15s, border-color 0.15s;
}

.imp-inspect:hover {
  color: var(--accentBright);
  border-color: var(--accent);
}

/* ---- Rank stepper --------------------------------------------------------- */
.imp-rank {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 3px;
  flex: none;
}

.imp-rank__arrow {
  width: 24px;
  height: 18px;
  border-radius: var(--radius-xs);
  border: 1px solid var(--border);
  background: var(--surface);
  color: var(--muted);
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  padding: 0;
}

.imp-rank__arrow:disabled {
  color: var(--faint);
  opacity: 0.4;
  cursor: default;
}

.imp-rank__num {
  width: 22px;
  height: 22px;
  border-radius: 7px;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: var(--text-xs);
  font-weight: var(--weight-extrabold);
  background: var(--surface3);
  color: var(--muted);
}

.imp-rank__num--top {
  background: var(--accent);
  color: var(--cover-text);
}

/* ---- Inspect preview panel ------------------------------------------------ */
.imp-inspect-panel {
  margin-top: 11px;
  padding: 11px 13px;
  border-radius: var(--radius-md);
  border: 1px solid var(--border);
  background: var(--surface);
}

.imp-inspect-panel--loading {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 10px;
  color: var(--muted);
  font-size: var(--text-base);
}

.imp-inspect-panel__count {
  margin: 0 0 8px;
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  text-transform: uppercase;
  letter-spacing: var(--tracking-label);
  color: var(--accentBright);
}

.imp-inspect-panel__list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
  gap: 4px 14px;
  max-height: 168px;
  overflow-y: auto;
}

.imp-inspect-panel__item {
  font-size: var(--text-sm);
  color: var(--muted);
  line-height: 1.5;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

/* ---- Stage 3: review ------------------------------------------------------ */
.imp-review-head {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 20px;
  flex-wrap: wrap;
}

.imp-review-cat {
  display: inline-flex;
  align-items: center;
  padding: 3px 11px;
  border-radius: var(--radius-pill);
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  color: var(--accentBright);
  background: var(--accentSoft);
}

.imp-review-title {
  font-family: var(--font-display);
  font-weight: var(--weight-black);
  font-size: var(--text-3xl);
  color: var(--text);
}

.imp-review-row {
  display: flex;
  align-items: center;
  gap: 11px;
  padding: 11px 14px;
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  margin-bottom: 8px;
  background: var(--surface2);
}

.imp-review-row__source {
  font-weight: var(--weight-bold);
  font-size: var(--text-md);
  color: var(--text);
}

.imp-review-row__lang {
  font-size: 10px;
  font-weight: var(--weight-bold);
  padding: 1px 6px;
  border-radius: 5px;
  background: var(--surface3);
  color: var(--muted);
}

.imp-review-row__preferred {
  font-size: 9.5px;
  font-weight: var(--weight-extrabold);
  padding: 2px 7px;
  border-radius: var(--radius-pill);
  background: var(--accentSoft);
  color: var(--accentBright);
}

.imp-review-row__imp {
  margin-left: auto;
  font-size: var(--text-xs);
  color: var(--faint);
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

.imp-spinner {
  width: 16px;
  height: 16px;
  border: 2px solid var(--accent);
  border-right-color: transparent;
  border-radius: 50%;
  display: inline-block;
  animation: imp-spin 0.8s linear infinite;
}

.imp-spinner--on-accent {
  width: 15px;
  height: 15px;
  border-color: var(--cover-text);
  border-right-color: transparent;
}

@keyframes imp-spin {
  to {
    transform: rotate(360deg);
  }
}
</style>
