<script setup lang="ts">
import CandidateConfigRow from './CandidateConfigRow.vue'
import type { MoveDirection } from '../ui/controls.types'
import type { ChapterInspect, SearchCandidate } from '../screens/import.types'
import type { DisplayRow } from '~/composables/useSourceConfigure'

/**
 * SourceConfigurePanel — the shared Configure-stage rows block (multi-select +
 * coverage + scanlator + rank), extracted from `Import.vue` (Slice P) so all
 * three Configure surfaces (Adopt wizard `Import.vue`, the Add-source dialog,
 * and the Import-match panel) render the SAME `<CandidateConfigRow>` list from
 * the SAME `DisplayRow[]` produced by `useSourceConfigure` — no reimplemented
 * row, no duplicated template.
 *
 * Purely presentational: `rows` arrives fully resolved (selection/rank/
 * coverage already computed by the composable); this component only maps each
 * row to a `<CandidateConfigRow>` and re-emits its events with the row's `key`
 * (or candidate, for `inspect`) attached, so the consumer's handlers stay
 * keyed exactly as `Import.vue`'s did before extraction.
 *
 * Inspect display (`inspecting`/`inspected`/`chapters`) is derived LOCALLY from
 * `inspectKey`/`inspecting`/`inspectChapters` — only the row matching
 * `inspectKey` shows the spinner or the resolved chapter list, and
 * `hideInspect` (a whole-panel opt-out, e.g. the single-select match surfaces)
 * always wins over both.
 */
withDefaults(defineProps<{
  /** The rows to render, in display order (already selection/rank-resolved). */
  rows: DisplayRow[]
  /** Hide every row's Inspect button — for surfaces with no live chapter-inspect endpoint. */
  hideInspect?: boolean
  /** The row key currently being inspected (drives which row shows the spinner/list). */
  inspectKey?: string | null
  /** True while `inspectKey`'s chapter inspect is in flight. */
  inspecting?: boolean
  /** The resolved chapter-preview rows for `inspectKey`, once loaded. */
  inspectChapters?: ChapterInspect[] | null
  /** Eyebrow label above the rows. */
  label?: string
}>(), {
  hideInspect: false,
  inspectKey: null,
  inspecting: false,
  inspectChapters: null,
  label: 'Sources · use arrows to rank priority',
})

const emit = defineEmits<{
  /** Toggle the row's selection. */
  toggle: [key: string]
  /** Re-rank a selected row: -1 = up (raise), 1 = down (lower). */
  move: [payload: { key: string, dir: MoveDirection }]
  /** Load a candidate's chapter list (Stage 2 inspect). */
  inspect: [candidate: SearchCandidate]
}>()
</script>

<template>
  <p class="scp-eyebrow">{{ label }}</p>

  <CandidateConfigRow
    v-for="row in rows"
    :key="row.key"
    :candidate="row.candidate"
    :selected="row.selected"
    :rank="row.rank"
    :can-up="row.canUp"
    :can-down="row.canDown"
    :inspecting="!hideInspect && inspectKey === row.key && inspecting"
    :inspected="!hideInspect && inspectKey === row.key && inspectChapters != null && !inspecting"
    :chapters="inspectChapters ?? []"
    :hide-inspect="hideInspect || row.isSplit"
    :scanlator="row.scanlator"
    :chapter-count="row.chapterCount"
    :chapter-ranges="row.chapterRanges"
    :coverage-unavailable="row.coverageUnavailable"
    @toggle="emit('toggle', row.key)"
    @inspect="emit('inspect', row.candidate)"
    @move="emit('move', { key: row.key, dir: $event })"
  />
</template>

<style scoped>
.scp-eyebrow {
  margin: 0 0 0.6875rem; /* 11px @16 — off-ladder, byte-identical rem literal */
  font-size: var(--text-xs);
  font-weight: var(--weight-extrabold);
  text-transform: uppercase;
  letter-spacing: var(--tracking-label);
  color: var(--faint);
}
</style>
