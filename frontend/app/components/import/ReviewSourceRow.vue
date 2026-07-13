<script setup lang="ts">
import Chip from '../ui/Chip.vue'
import Tag from '../ui/Tag.vue'
import type { SearchCandidate } from '../screens/import.types'

/**
 * ReviewSourceRow — one resolved source line in Stage 3 (Adopt review): the rank
 * badge, the source name, a language <Chip>, the "PREFERRED" <Tag> on the top
 * source, and the derived importance weight. Presentation-only — the candidate +
 * its resolved rank/importance arrive via props; the row emits nothing.
 *
 * `scanlator` is an opt-in subtitle (default "" = hidden) distinguishing two
 * review rows that share the same source (a per-scanlator adopt row) — see
 * `Import.vue`'s Stage 2 auto-split.
 */
withDefaults(defineProps<{
  /** The candidate this review line represents. */
  candidate: SearchCandidate
  /** 1-based rank among the selected sources (1 = preferred). */
  rank: number
  /** Derived importance weight (higher = preferred metadata/download source). */
  importance: number
  /** Whether this is the preferred (rank-1) source. */
  preferred: boolean
  /** Scanlation group subtitle for this row, when it tracks one specific group; "" hides it. */
  scanlator?: string
}>(), {
  scanlator: '',
})
</script>

<template>
  <div class="row">
    <span class="row__rank" :class="{ 'row__rank--top': preferred }">{{ rank }}</span>
    <span class="row__meta">
      <span class="row__source">{{ candidate.sourceName }}</span>
      <span v-if="scanlator" class="row__scanlator">{{ scanlator }}</span>
    </span>
    <Chip variant="language">{{ candidate.lang.toUpperCase() }}</Chip>
    <Tag v-if="preferred" tone="accent">PREFERRED</Tag>
    <span class="row__imp">importance {{ importance }}</span>
  </div>
</template>

<style scoped>
.row {
  display: flex;
  align-items: center;
  gap: 11px;
  padding: 11px 14px;
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  margin-bottom: 8px;
  background: var(--surface2);
}

@media (max-width: 900px) {
  /* A long source name + language chip + PREFERRED tag + importance label
   * can't share one nowrap line on a phone — wrap the row and let the
   * importance label sit naturally after whatever it wraps to instead of
   * being shoved to a lonely far edge by `margin-left: auto` (mirrors
   * Downloads' `.downloads__cycle` mobile fix). */
  .row {
    flex-wrap: wrap;
  }

  .row__imp {
    margin-left: 0;
  }
}

.row__rank {
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
  flex: none;
}

.row__rank--top {
  background: var(--accent);
  color: var(--cover-text);
}

.row__meta {
  display: flex;
  flex-direction: column;
  gap: 1px;
  min-width: 0;
}

.row__source {
  font-weight: var(--weight-bold);
  font-size: var(--text-md);
  color: var(--text);
  overflow-wrap: anywhere;
}

.row__scanlator {
  font-size: var(--text-xs);
  font-weight: var(--weight-semibold);
  color: var(--muted);
}

.row__imp {
  margin-left: auto;
  font-size: var(--text-xs);
  color: var(--faint);
}
</style>
