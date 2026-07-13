<script setup lang="ts">
import Chip from './Chip.vue'

/**
 * ChipRow — a thin, wrapping row of `Chip`s built from a list of strings, with an
 * optional leading label. It is the shared home for the rich card's genre and tag
 * rows (and anywhere else a labelled set of pills is wanted). An empty `items`
 * list renders NOTHING — so a caller can drop it in unconditionally and it simply
 * disappears when there is no data.
 *
 *   - `items`   (required): the chip labels, in order.
 *   - `label`:  an optional leading caption (e.g. `Genres`).
 *   - `variant` (default `neutral`): the `Chip` colour treatment for every chip.
 */
withDefaults(defineProps<{
  /** The chip labels (empty → the row renders nothing). */
  items: string[]
  /** Optional leading caption shown before the chips. */
  label?: string
  /** Chip colour treatment applied to every chip. */
  variant?: 'category' | 'language' | 'accent' | 'neutral' | 'frost'
}>(), {
  variant: 'neutral',
})
</script>

<template>
  <div v-if="items.length" class="chip-row">
    <span v-if="label" class="chip-row__label">{{ label }}</span>
    <Chip v-for="item in items" :key="item" :variant="variant">{{ item }}</Chip>
  </div>
</template>

<style scoped>
.chip-row {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
}

.chip-row__label {
  margin-right: 2px;
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  letter-spacing: var(--tracking-label);
  text-transform: uppercase;
  color: var(--faint);
}
</style>
