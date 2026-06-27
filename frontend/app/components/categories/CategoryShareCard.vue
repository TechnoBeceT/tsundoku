<script setup lang="ts">
import Chip from '../ui/Chip.vue'
import ProgressBar from '../ui/ProgressBar.vue'

/**
 * CategoryShareCard — one category's card in the Categories overview: a clickable
 * surface showing the category name (as an accent Chip), its series count, and its
 * share of the whole library as a percent + a ProgressBar.
 *
 * Presentation only. The share is resolved by the PARENT (which alone knows the
 * whole-library total) and passed in, so the card needs no other categories.
 * Clicking emits `open` for the parent to act on (jump to the library filtered to
 * this category). Reads only design tokens, so it renders in both themes.
 */
defineProps<{
  /** Category NAME — shown in the chip, the aria-label, and the meta line. */
  name: string
  /** Number of series in this category. */
  count: number
  /** This category's share of the whole library, as a whole percent (0–100). */
  share: number
}>()

const emit = defineEmits<{
  /** The card was clicked — the parent maps it to its category-filter action. */
  open: []
}>()
</script>

<template>
  <button
    type="button"
    class="cat-card"
    :aria-label="`${name} — ${count} series`"
    @click="emit('open')"
  >
    <div class="cat-card__head">
      <Chip variant="accent">{{ name }}</Chip>
      <span class="cat-card__count">{{ count }}</span>
    </div>
    <div class="cat-card__bar">
      <ProgressBar :value="share" tone="var(--accentBright)" />
    </div>
    <div class="cat-card__meta">{{ share }}% of library · {{ count }} series</div>
  </button>
</template>

<style scoped>
.cat-card {
  display: block;
  width: 100%;
  text-align: left;
  padding: 19px;
  border-radius: var(--radius-2xl);
  border: 1px solid var(--border);
  background: var(--surface);
  cursor: pointer;
  transition: transform 0.15s, border-color 0.15s, box-shadow 0.15s;
}

.cat-card:hover {
  transform: translateY(-3px);
  border-color: var(--border2);
  box-shadow: var(--shadow);
}

.cat-card:focus-visible {
  outline: none;
  border-color: var(--accent);
  box-shadow: var(--ring-focus);
}

.cat-card__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  margin-bottom: 15px;
}

.cat-card__count {
  font-family: var(--font-display);
  font-weight: var(--weight-black);
  font-size: 32px;
  line-height: 1;
  color: var(--text);
}

/* Wrap the ProgressBar so the original 7px track height + animated fill + the
   spacing under the bar are preserved (the atom's own track is 5px). */
.cat-card__bar {
  margin-bottom: 9px;
}

.cat-card__bar :deep(.progress) {
  height: 7px;
}

.cat-card__bar :deep(.progress__bar) {
  transition: width 0.5s;
}

.cat-card__meta {
  font-size: var(--text-xs);
  color: var(--faint);
}
</style>
