<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import AppButton from '../ui/AppButton.vue'
import ErrorBanner from '../ui/ErrorBanner.vue'
import Spinner from '../ui/Spinner.vue'
import CandidateConfigRow from '../import/CandidateConfigRow.vue'
import SearchGroupCard from '../import/SearchGroupCard.vue'
import type { SearchCandidate, SearchGroup } from '../screens/import.types'
import type { ScanMatch } from '../screens/scanLibrary.types'

/**
 * MatchPanel — the Scan Library "Match a source" sub-panel: attaches ONE
 * Suwayomi source to a staged (disk-only) entry so future chapters/upgrades
 * work, instead of leaving it disk-only forever. Reuses the SAME grouped
 * candidates the Import/Adopt wizard renders (`GET /api/library/imports/match`
 * returns the identical `SearchGroup`/`SearchCandidate` DTO as `GET
 * /api/search`) via the exact `SearchGroupCard` + `CandidateConfigRow`
 * organisms `Import.vue` composes — this panel never re-implements candidate
 * rendering (§2 DRY / §8 reuse).
 *
 * Two internal stages, mirroring `Import.vue`'s Stage 1 → Stage 2 shape but
 * simpler: this endpoint attaches exactly ONE provider (unlike Adopt's
 * ranked multi-provider set), so there is no reorder/rank step.
 *   1. **Groups** — pick which cross-source group is the right series
 *      (`SearchGroupCard`, one card per group).
 *   2. **Candidates** — pick exactly one source within that group
 *      (`CandidateConfigRow`, driven as a single-select: toggling a row
 *      selects it and deselects any other — `rank`/`canUp`/`canDown` are
 *      always inert since there is nothing to reorder).
 *
 * Presentation-only: the parent (`ScanLibrary.vue`) owns whether the panel is
 * shown at all and supplies the match search's own loading/error state
 * (`searching`/`searchError`, from `useScanLibrary().matching`/`.matchError`)
 * separately from the CONFIRM mutation's loading/error state (`busy`/`error`,
 * from `useScanLibrary().busy(path)`/`.error(path)`) — two distinct async
 * operations, two distinct §16 state pairs.
 */
const props = withDefaults(defineProps<{
  /** The staged entry's title, for the panel header. */
  title: string
  /** Cross-source candidate groups returned by the match search. */
  groups: SearchGroup[]
  /** True while the match search itself is in flight. */
  searching?: boolean
  /** A match-search failure message, or "" for none. */
  searchError?: string
  /** True while the confirmed match is being imported (the CONFIRM mutation). */
  busy?: boolean
  /** The confirmed match's import failure, or "" for none. */
  error?: string
}>(), {
  searching: false,
  searchError: '',
  busy: false,
  error: '',
})

const emit = defineEmits<{
  /** The owner confirmed a source to attach — importance defaults to outrank disk-only. */
  confirm: [selection: ScanMatch]
  /** Abandon the match flow — the parent returns to the staging table. */
  back: []
}>()

/** Stable identity for a candidate (a source can appear once per group). */
const candKey = (c: SearchCandidate): string => `${c.source}:${c.mangaId}`

/**
 * Disk-origin providers are always importance 1 (Phase-A invariant); any
 * matched Suwayomi source must outrank that to trigger the upgrade-swap, so
 * a fresh match is pinned at 2 — the minimum value that satisfies "≥2" from
 * the plan's global constraints. There is only ever one provider attached
 * via this flow, so there is no ranking scheme to derive it from.
 */
const DEFAULT_IMPORTANCE = 2

const stage = ref<'groups' | 'candidates'>('groups')
const pickedGroup = ref<SearchGroup | null>(null)
const selectedKey = ref<string | null>(null)

// A fresh match search (new `groups` prop, e.g. the owner matched a
// different row) always restarts at the group-picking stage.
watch(() => props.groups, () => {
  stage.value = 'groups'
  pickedGroup.value = null
  selectedKey.value = null
})

const pickGroup = (g: SearchGroup): void => {
  pickedGroup.value = g
  selectedKey.value = null
  stage.value = 'candidates'
}

/** Toggles a candidate's selection — selecting one clears any other (single-select). */
const selectCandidate = (key: string): void => {
  selectedKey.value = selectedKey.value === key ? null : key
}

interface CandRow {
  key: string
  candidate: SearchCandidate
  selected: boolean
}

const candRows = computed<CandRow[]>(() => {
  const g = pickedGroup.value
  if (!g) return []
  return g.candidates.map(c => ({
    key: candKey(c),
    candidate: c,
    selected: selectedKey.value === candKey(c),
  }))
})

const selectedCandidate = computed<SearchCandidate | null>(() => {
  const g = pickedGroup.value
  if (!g || !selectedKey.value) return null
  return g.candidates.find(c => candKey(c) === selectedKey.value) ?? null
})

/** Stage-aware back: from Candidates it returns to Groups; from Groups it exits the panel. */
const handleBack = (): void => {
  if (stage.value === 'candidates') {
    stage.value = 'groups'
    pickedGroup.value = null
    selectedKey.value = null
  } else {
    emit('back')
  }
}

const confirm = (): void => {
  const c = selectedCandidate.value
  if (!c || props.busy) return
  emit('confirm', { source: c.source, mangaId: c.mangaId, importance: DEFAULT_IMPORTANCE })
}
</script>

<template>
  <div class="match-panel">
    <div class="mp-head">
      <span class="mp-eyebrow">Match a source</span>
      <span class="mp-title">{{ title }}</span>
    </div>

    <ErrorBanner v-if="error" class="mp-error" :message="error" :dismissible="false" />

    <div v-if="searching" class="mp-loading">
      <Spinner :size="16" tone="accent" />
      Searching sources…
    </div>

    <ErrorBanner v-else-if="searchError" class="mp-error" :message="searchError" :dismissible="false" />

    <p v-else-if="groups.length === 0" class="mp-note mp-note--center">
      No matches found across any source.
    </p>

    <template v-else-if="stage === 'groups'">
      <p class="mp-subhead">
        {{ groups.length }} possible match{{ groups.length === 1 ? '' : 'es' }} · choose one
      </p>
      <div class="mp-groups">
        <SearchGroupCard
          v-for="g in groups"
          :key="g.title"
          :group="g"
          @pick="pickGroup"
        />
      </div>
      <div class="mp-actions mp-actions--start">
        <AppButton variant="ghost" @click="emit('back')">Back</AppButton>
      </div>
    </template>

    <template v-else>
      <p class="mp-subhead">Pick the source to attach · importance {{ DEFAULT_IMPORTANCE }} (outranks disk-only)</p>
      <CandidateConfigRow
        v-for="row in candRows"
        :key="row.key"
        :candidate="row.candidate"
        :selected="row.selected"
        :rank="row.selected ? 1 : 0"
        :can-up="false"
        :can-down="false"
        :inspecting="false"
        :inspected="false"
        :chapters="[]"
        @toggle="selectCandidate(row.key)"
        @inspect="() => {}"
        @move="() => {}"
      />
      <div class="mp-actions mp-actions--between">
        <AppButton variant="ghost" :disabled="busy" @click="handleBack">Back</AppButton>
        <AppButton variant="primary" :loading="busy" :disabled="!selectedCandidate || busy" @click="confirm">
          Confirm match
        </AppButton>
      </div>
    </template>
  </div>
</template>

<style scoped>
.match-panel {
  display: block;
}

.mp-head {
  display: flex;
  flex-direction: column;
  gap: 3px;
  margin-bottom: 18px;
}

.mp-eyebrow {
  font-size: var(--text-xs);
  font-weight: var(--weight-extrabold);
  text-transform: uppercase;
  letter-spacing: var(--tracking-label);
  color: var(--faint);
}

.mp-title {
  font-family: var(--font-display);
  font-weight: var(--weight-black);
  font-size: var(--text-2xl);
  color: var(--text);
}

.mp-error {
  margin-bottom: 16px;
}

.mp-subhead {
  margin: 0 0 11px;
  font-size: var(--text-sm);
  color: var(--muted);
  font-weight: var(--weight-semibold);
}

.mp-groups {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.mp-note {
  margin: 0;
  font-size: 13.5px;
  color: var(--muted);
}

.mp-note--center {
  padding: 34px 0;
  text-align: center;
}

.mp-loading {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 10px;
  padding: 40px 0;
  color: var(--muted);
  font-size: var(--text-base);
}

.mp-actions {
  display: flex;
  margin-top: 20px;
}

.mp-actions--start {
  justify-content: flex-start;
}

.mp-actions--between {
  justify-content: space-between;
}
</style>
