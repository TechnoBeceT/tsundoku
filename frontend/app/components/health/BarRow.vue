<script setup lang="ts">
import { computed } from 'vue'

/**
 * BarRow — one horizontal bar in a leaderboard: a left label, a CSS-width bar
 * (fraction of the row's max), and a right-aligned value. Pure CSS, no charting
 * dependency (the report deliberately ships zero chart deps). The bar has a
 * rounded data-end and sits on the track baseline (dataviz mark spec).
 *
 * Presentation-only — the parent (`HBarChart`) computes the `fraction` against
 * the series max so every row shares one scale.
 *
 *   - `label` (required): the row label (left).
 *   - `fraction` (required, 0..1): the bar fill fraction of the row max.
 *   - `valueLabel` (required): the formatted value shown at the right.
 *   - `tone` (default `var(--accent)`): the bar colour (token-backed).
 *   - `title`: an optional hover tooltip (the raw/precise value).
 */
const props = withDefaults(defineProps<{
  /** The row label. */
  label: string
  /** Bar fill fraction of the series max (0..1). */
  fraction: number
  /** The formatted value shown at the right. */
  valueLabel: string
  /** Bar colour — a token-backed CSS value. */
  tone?: string
  /** Optional hover tooltip. */
  title?: string
}>(), {
  tone: 'var(--accent)',
  title: undefined,
})

// Clamp to [0,1] and keep a visible sliver for any non-zero value so a tiny bar
// never renders as nothing.
const width = computed(() => {
  const f = Math.min(1, Math.max(0, props.fraction))
  if (f === 0) return '0%'
  return `${Math.max(2, f * 100)}%`
})
</script>

<template>
  <div class="bar-row" :title="title">
    <span class="bar-row__label">{{ label }}</span>
    <span class="bar-row__track">
      <span class="bar-row__fill" :style="{ width, background: tone }" />
    </span>
    <span class="bar-row__value">{{ valueLabel }}</span>
  </div>
</template>

<style scoped>
.bar-row {
  display: grid;
  grid-template-columns: minmax(0, 8rem) 1fr auto;
  align-items: center;
  gap: 10px;
}

.bar-row__label {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: var(--text-sm);
  font-weight: var(--weight-semibold);
  color: var(--text);
}

.bar-row__track {
  height: 8px;
  border-radius: var(--radius-pill);
  background: var(--surface3);
  overflow: hidden;
}

.bar-row__fill {
  display: block;
  height: 100%;
  /* Rounded data-end, flat baseline-anchored start (dataviz mark spec). */
  border-radius: 0 var(--radius-pill) var(--radius-pill) 0;
  transition: width 0.3s ease;
}

.bar-row__value {
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  color: var(--muted);
  white-space: nowrap;
}
</style>
