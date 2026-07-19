<script setup lang="ts">
import { computed } from 'vue'
import BarRow from './BarRow.vue'

/** One leaderboard datum: a stable key, a label, a numeric value + its display. */
export interface HBarDatum {
  /** Stable row key. */
  key: string
  /** The row label. */
  label: string
  /** The numeric value (scales the bar against the series max). */
  value: number
  /** The formatted value shown at the bar end (e.g. "18.4s", "14"). */
  valueLabel: string
}

/**
 * HBarChart — a compact horizontal-bar leaderboard (slowest sources, top failers)
 * built from `BarRow`s sharing one scale. Magnitude ranking → horizontal bars,
 * single series → no legend, direct value labels (dataviz form + rules). All CSS,
 * no chart dependency.
 *
 *   - `items` (required): the ordered rows (already sorted by the caller).
 *   - `tone` (default `var(--accent)`): the bar colour for every row.
 *   - `emptyLabel` (default "No data"): shown when `items` is empty.
 */
const props = withDefaults(defineProps<{
  /** The ordered leaderboard rows. */
  items: HBarDatum[]
  /** Bar colour — a token-backed CSS value. */
  tone?: string
  /** Message when there are no rows. */
  emptyLabel?: string
}>(), {
  tone: 'var(--accent)',
  emptyLabel: 'No data',
})

// The series max sets every bar's scale; guard against an all-zero series.
const max = computed(() => Math.max(1, ...props.items.map(i => i.value)))
</script>

<template>
  <div class="hbar">
    <p v-if="items.length === 0" class="hbar__empty">{{ emptyLabel }}</p>
    <BarRow
      v-for="item in items"
      v-else
      :key="item.key"
      :label="item.label"
      :fraction="item.value / max"
      :value-label="item.valueLabel"
      :tone="tone"
      :title="`${item.label}: ${item.valueLabel}`"
    />
  </div>
</template>

<style scoped>
.hbar {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.hbar__empty {
  margin: 0;
  padding: 8px 0;
  font-size: var(--text-sm);
  color: var(--muted);
}
</style>
