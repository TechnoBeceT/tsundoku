<script setup lang="ts">
import { computed } from 'vue'
import Chip from '../ui/Chip.vue'
import HealthBadge from '../ui/HealthBadge.vue'
import ReorderControl from '../ui/ReorderControl.vue'
import type { MoveDirection } from '../ui/controls.types'
import type { Provider } from '../screens/seriesDetail.types'

/**
 * ProviderRow — one ranked source in the Series-Detail "Sources" list: a
 * `ReorderControl` rank stepper, the source name (+ a PREFERRED chip for rank 1,
 * or an UNLINKED chip for a disk-origin group), the language/scanlator/
 * importance meta line, the source's chapter coverage, a `HealthBadge` with the
 * chapters-behind note, the synced/newest timestamps, an optional last-error,
 * and the row actions (a quiet Remove button, plus — for an unlinked disk-origin
 * group only — a "Match to source" button). Presentation-only — the source + its
 * rank arrive via props; the row emits `move` (re-rank), `remove`, and `match`
 * (opens `MatchDiskProviderDialog` for this provider).
 *
 * `provider.linked` is false for a disk-origin group created by library
 * import (no real Suwayomi source attached — `suwayomi_id=0` on the backend).
 *
 * COVERAGE — the two numbers say different things, and the row must never blur
 * them (a bare "56 chapters" once read as the source's offering and misled a
 * live diagnosis):
 *   - `feedCount` / `feedRanges` = what this source OFFERS ("270 chapters ·
 *     1-269"), straight from the stored ProviderChapter feed on the series-detail
 *     response. NO click, NO fetch — in particular no live ping to the source
 *     (we already hold the feed; pinging for it is needless ban risk).
 *   - `chapterCount` = how many of the owner's downloaded files this source
 *     currently SUPPLIES ("supplies 56").
 * A provider with an empty feed (`feedCount === 0` — e.g. an unlinked disk-origin
 * group) shows "No chapter feed" rather than a phantom "0 chapters".
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
  /** True when this row is an unlinked disk provider with a mergeable linked twin (drift). Renders a DUPLICATE chip. */
  duplicate?: boolean
}>()

const emit = defineEmits<{
  /** A re-rank was requested: -1 = up (raise), 1 = down (lower). */
  move: [direction: MoveDirection]
  /** The Remove button was pressed. */
  remove: []
  /** The "Match to source" button was pressed (unlinked groups only). */
  match: []
}>()

// Uppercased language code shown in the language Chip (e.g. "EN").
const language = computed(() => props.provider.language.toUpperCase())

// What the SOURCE offers: "270 chapters · 1-269" (ranges omitted when the feed
// carries no chapter numbers). Empty feed → null, so the row can say so plainly
// instead of rendering "0 chapters".
const offering = computed<string | null>(() => {
  const { feedCount, feedRanges } = props.provider
  if (feedCount <= 0) return null
  const label = `${feedCount} chapter${feedCount === 1 ? '' : 's'}`
  return feedRanges ? `${label} · ${feedRanges}` : label
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
        <Chip v-if="duplicate" variant="accent">DUPLICATE</Chip>
      </div>
      <div class="source__meta">
        <Chip variant="language">{{ language }}</Chip>
        <span v-if="provider.scanlator">{{ provider.scanlator }}</span>
        <span>importance {{ provider.importance }}</span>
      </div>
      <div class="source__coverage">
        <span v-if="offering" class="source__offering">{{ offering }}</span>
        <span v-else class="source__offering source__offering--none">No chapter feed</span>
        <span class="source__supplies">supplies {{ provider.chapterCount }}</span>
      </div>
      <div v-if="!provider.linked" class="source__unlinked-note">
        Imported from disk — no real source attached. Match it to link these chapters without re-downloading.
      </div>
      <div class="source__healthrow">
        <HealthBadge :health="provider.health" />
        <span v-if="provider.chaptersBehind > 0" class="source__behind">{{ provider.chaptersBehind }} behind</span>
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
  display: flex;
  align-items: baseline;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 8px;
  font-size: 11.5px;
}

/* What the SOURCE offers — the headline number, so it can't be misread as the
   satisfied count sitting next to it. */
.source__offering {
  color: var(--text);
  font-weight: var(--weight-bold);
}

.source__offering--none {
  color: var(--faint);
  font-weight: var(--weight-regular);
}

/* How many downloaded files come FROM this source — deliberately quieter. */
.source__supplies {
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
