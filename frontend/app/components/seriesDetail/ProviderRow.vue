<script setup lang="ts">
import { computed } from 'vue'
import Chip from '../ui/Chip.vue'
import HealthBadge from '../ui/HealthBadge.vue'
import ReorderControl from '../ui/ReorderControl.vue'
import type { MoveDirection } from '../ui/controls.types'
import type { Provider } from '../screens/seriesDetail.types'
import type { ScanlatorCoverage } from '../screens/import.types'

/**
 * ProviderRow — one ranked source in the Series-Detail "Sources" list: a
 * `ReorderControl` rank stepper, the source name (+ a PREFERRED chip for rank 1,
 * or an UNLINKED chip for a disk-origin group), the language/scanlator/
 * importance meta line, a chapter-count note, a `HealthBadge` with the
 * chapters-behind note, the synced/newest timestamps, an optional last-error,
 * a LAZY per-source coverage affordance, and the row actions (a quiet Remove
 * button, plus — for an unlinked disk-origin group only — a "Match to
 * source" button). Presentation-only — the source + its rank arrive via
 * props; the row emits `move` (re-rank), `remove`, `match` (opens
 * `MatchDiskProviderDialog` for this provider), and `loadCoverage` (the
 * "Show coverage" click).
 *
 * `provider.linked` is false for a disk-origin group created by library
 * import (no real Suwayomi source attached — `suwayomi_id=0` on the backend);
 * `chapterCount` is shown for every row so the owner can see how many
 * chapters an unlinked group carries before matching it.
 *
 * Coverage affordance (LAZY — never fetched by this component; it only
 * displays whatever `coverage` the parent hands it and emits `loadCoverage`
 * on click): a provider with `mangaId <= 0` (unlinked disk provider — nothing
 * to fetch) renders no affordance at all. Otherwise: `coverage === undefined`
 * (never fetched) shows a "Show coverage" button; once `coverage` is an
 * array or `null` (fetch resolved), the button is replaced by either the
 * matching scanlator's `{count} chapters · {ranges}` or, if there is no
 * match or the fetch failed (`null`), "Coverage unavailable". The match rule
 * mirrors the Adopt wizard's untagged-scanlator convention (`useSourceConfigure`):
 * this row's `scanlator === ''` matches the coverage entry whose `scanlator`
 * equals the row's `providerName` (the backend's untagged/source-name
 * bucket); a non-empty `scanlator` matches by exact name.
 */
const props = defineProps<{
  /** The source to render. */
  provider: Provider
  /** 1-based display rank (top = preferred). */
  rank: number
  /** Whether this is the rank-1 / preferred source (drives the chip + highlight). */
  preferred: boolean
  /** Whether the up arrow is enabled (false = already top). */
  canUp: boolean
  /** Whether the down arrow is enabled (false = already bottom). */
  canDown: boolean
  /** True while a mutation is in flight — disables reorder + remove. */
  saving?: boolean
  /**
   * This row's per-scanlator coverage breakdown: `undefined` = never fetched
   * (shows the "Show coverage" button), `null` = fetch attempted and failed
   * (shows "Coverage unavailable"), an array = the loaded breakdown (shows
   * the matching scanlator's count/ranges, or "Coverage unavailable" if none
   * matches). Never fetched by this component — see `loadCoverage`.
   */
  coverage?: ScanlatorCoverage[] | null
}>()

const emit = defineEmits<{
  /** A re-rank was requested: -1 = up (raise), 1 = down (lower). */
  move: [direction: MoveDirection]
  /** The Remove button was pressed. */
  remove: []
  /** The "Match to source" button was pressed (unlinked groups only). */
  match: []
  /** The "Show coverage" button was pressed — the parent should fetch this row's coverage. */
  loadCoverage: []
}>()

// Uppercased language code shown in the language Chip (e.g. "EN").
const language = computed(() => props.provider.language.toUpperCase())

// The coverage entry for THIS row's scanlator, or null when not (yet) resolvable
// (coverage not loaded, the fetch failed, or no scanlator in the breakdown matches).
const coverageMatch = computed<ScanlatorCoverage | null>(() => {
  if (!props.coverage) return null
  const match = props.coverage.find((sc) =>
    props.provider.scanlator === '' ? sc.scanlator === props.provider.providerName : sc.scanlator === props.provider.scanlator,
  )
  return match ?? null
})

// Relative-time label for the sync/newest timestamps (null → "never").
const rel = (iso: string | null): string => {
  if (iso == null) return 'never'
  const d = Date.now() - Date.parse(iso)
  const m = 60_000, h = 3_600_000, day = 86_400_000
  if (d < m) return 'just now'
  if (d < h) return `${Math.floor(d / m)}m ago`
  if (d < day) return `${Math.floor(d / h)}h ago`
  return `${Math.floor(d / day)}d ago`
}
</script>

<template>
  <div class="source">
    <ReorderControl
      :rank="rank"
      :top-highlighted="preferred"
      :can-up="canUp"
      :can-down="canDown"
      :disabled="saving"
      @move="emit('move', $event)"
    />

    <div class="source__main">
      <div class="source__namerow">
        <span class="source__name">{{ provider.providerName }}</span>
        <Chip v-if="preferred" variant="accent">PREFERRED</Chip>
        <Chip v-if="!provider.linked" variant="neutral">UNLINKED</Chip>
      </div>
      <div class="source__meta">
        <Chip variant="language">{{ language }}</Chip>
        <span v-if="provider.scanlator">{{ provider.scanlator }}</span>
        <span>importance {{ provider.importance }}</span>
        <span>{{ provider.chapterCount }} chapter{{ provider.chapterCount === 1 ? '' : 's' }}</span>
      </div>
      <div v-if="!provider.linked" class="source__unlinked-note">
        Imported from disk — no real source attached. Match it to link these chapters without re-downloading.
      </div>
      <div class="source__healthrow">
        <HealthBadge :health="provider.health" />
        <span v-if="provider.chaptersBehind > 0" class="source__behind">{{ provider.chaptersBehind }} behind</span>
      </div>
      <div v-if="provider.mangaId > 0" class="source__coverage">
        <button
          v-if="coverage === undefined"
          type="button"
          class="btn-coverage"
          :disabled="saving"
          @click="emit('loadCoverage')"
        >
          Show coverage
        </button>
        <span v-else-if="coverageMatch" class="source__coverage-text">
          {{ coverageMatch.count }} chapter{{ coverageMatch.count === 1 ? '' : 's' }} · {{ coverageMatch.ranges }}
        </span>
        <span v-else class="source__coverage-unavailable">Coverage unavailable</span>
      </div>
      <div class="source__times">
        <span>Synced {{ rel(provider.lastSyncedAt) }}</span>
        <span>Newest {{ rel(provider.newestChapterAt) }}</span>
      </div>
      <div v-if="provider.lastError" class="source__error">{{ provider.lastError }}</div>
      <div class="source__actions">
        <button v-if="!provider.linked" type="button" class="btn-match" :disabled="saving" @click="emit('match')">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M9 18l6-6-6-6" />
          </svg>
          Match to source
        </button>
        <button type="button" class="btn-remove" :disabled="saving" @click="emit('remove')">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M3 6h18M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6" />
          </svg>
          Remove
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.source {
  display: flex;
  align-items: flex-start;
  gap: 11px;
  margin-bottom: 10px;
  padding: 12px;
  border-radius: 13px;
  border: 1px solid var(--border);
  background: var(--surface2);
}

.source__main {
  flex: 1;
  min-width: 0;
}

.source__namerow {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 5px;
  flex-wrap: wrap;
}

.source__name {
  font-size: var(--text-md);
  font-weight: var(--weight-bold);
  color: var(--text);
}

.source__meta {
  display: flex;
  align-items: center;
  gap: 7px;
  margin-bottom: 8px;
  flex-wrap: wrap;
  font-size: 11.5px;
  color: var(--muted);
}

.source__healthrow {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.source__behind {
  font-size: var(--text-xs);
  color: var(--faint);
}

.source__coverage {
  margin-top: 8px;
}

.btn-coverage {
  padding: 0;
  border: none;
  background: transparent;
  color: var(--accentBright);
  font-size: 11.5px;
  font-weight: var(--weight-bold);
  cursor: pointer;
}

.btn-coverage:hover {
  text-decoration: underline;
}

.btn-coverage:disabled {
  opacity: 0.5;
  cursor: default;
}

.source__coverage-text {
  font-size: 11.5px;
  color: var(--muted);
}

.source__coverage-unavailable {
  font-size: 11.5px;
  color: var(--faint);
}

.source__times {
  display: flex;
  gap: 14px;
  flex-wrap: wrap;
  margin-top: 8px;
  font-size: 10.5px;
  color: var(--faint);
}

.source__error {
  margin-top: 8px;
  padding: 6px 9px;
  border-radius: var(--radius-sm);
  border: 1px solid var(--danger-border);
  background: var(--danger-bg);
  color: var(--danger-text);
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  word-break: break-word;
}

.source__unlinked-note {
  margin-top: 6px;
  margin-bottom: 8px;
  font-size: 11.5px;
  line-height: 1.4;
  color: var(--muted);
}

.source__actions {
  display: flex;
  gap: 8px;
  margin-top: 9px;
}

.btn-remove,
.btn-match {
  display: flex;
  align-items: center;
  gap: 5px;
  padding: 5px 10px;
  border-radius: var(--radius-sm);
  border: 1px solid var(--border);
  background: transparent;
  font-size: 11.5px;
  font-weight: var(--weight-bold);
  cursor: pointer;
  transition: background 0.15s, border-color 0.15s;
}

.btn-remove {
  color: var(--danger-bright);
}

.btn-remove:hover {
  background: var(--danger-bg);
}

.btn-match {
  color: var(--accentBright);
  border-color: var(--accent);
}

.btn-match:hover {
  background: var(--accentSoft);
}

.btn-remove:disabled,
.btn-match:disabled {
  opacity: 0.5;
  cursor: default;
}
</style>
