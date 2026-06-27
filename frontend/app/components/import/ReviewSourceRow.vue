<script setup lang="ts">
import Chip from '../ui/Chip.vue'
import Tag from '../ui/Tag.vue'
import type { SearchCandidate } from '../screens/import.types'

/**
 * ReviewSourceRow — one resolved source line in Stage 3 (Adopt review): the rank
 * badge, the source name, a language <Chip>, the "PREFERRED" <Tag> on the top
 * source, and the derived importance weight. Presentation-only — the candidate +
 * its resolved rank/importance arrive via props; the row emits nothing.
 */
defineProps<{
  /** The candidate this review line represents. */
  candidate: SearchCandidate
  /** 1-based rank among the selected sources (1 = preferred). */
  rank: number
  /** Derived importance weight (higher = preferred metadata/download source). */
  importance: number
  /** Whether this is the preferred (rank-1) source. */
  preferred: boolean
}>()
</script>

<template>
  <div class="row">
    <span class="row__rank" :class="{ 'row__rank--top': preferred }">{{ rank }}</span>
    <span class="row__source">{{ candidate.sourceName }}</span>
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

.row__source {
  font-weight: var(--weight-bold);
  font-size: var(--text-md);
  color: var(--text);
}

.row__imp {
  margin-left: auto;
  font-size: var(--text-xs);
  color: var(--faint);
}
</style>
