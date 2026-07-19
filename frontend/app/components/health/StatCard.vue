<script setup lang="ts">
import StatTile from '../ui/StatTile.vue'

/**
 * StatCard — a KPI tile for the report's headline row: a bordered `--surface`
 * card with a coloured top accent rule, the big number + caption (composed from
 * the shared `ui/StatTile`, so the number treatment stays consistent app-wide),
 * and an optional quiet `hint` line beneath.
 *
 * The `tone` drives BOTH the value colour and the accent rule, so a KPI reads its
 * health at a glance (emerald success rate, rose failures). Pass a token-backed
 * `var(--…)` — never a raw hex.
 *
 *   - `label` (required): the caption beneath the value.
 *   - `value` (required): the headline number/string.
 *   - `tone` (default `var(--text)`): value + accent colour (token-backed).
 *   - `hint`: an optional quiet line under the tile (e.g. "of 1,284 events").
 */
withDefaults(defineProps<{
  /** Caption beneath the value. */
  label: string
  /** The headline number/string. */
  value: string | number
  /** Value + accent colour — a token-backed CSS value. */
  tone?: string
  /** Optional quiet detail line under the tile. */
  hint?: string
}>(), {
  tone: 'var(--text)',
  hint: undefined,
})
</script>

<template>
  <div class="stat-card" :style="{ '--stat-accent': tone }">
    <StatTile :label="label" :value="value" :tone="tone" />
    <p v-if="hint" class="stat-card__hint">{{ hint }}</p>
  </div>
</template>

<style scoped>
.stat-card {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 16px 16px 14px;
  border: 1px solid var(--border);
  border-radius: var(--radius-xl);
  background: var(--surface);
  overflow: hidden;
}

/* A slim coloured rule along the top edge — the tile's health at a glance. */
.stat-card::before {
  content: '';
  position: absolute;
  inset: 0 0 auto;
  height: 3px;
  background: var(--stat-accent);
  opacity: 0.85;
}

.stat-card__hint {
  margin: 0;
  font-size: var(--text-xs);
  color: var(--faint);
}
</style>
