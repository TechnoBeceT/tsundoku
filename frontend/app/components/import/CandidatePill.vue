<script setup lang="ts">
import CoverImage from '../ui/CoverImage.vue'
import type { SearchCandidate } from '../screens/import.types'

/**
 * CandidatePill — one source's hit shown as a compact pill inside a
 * <SearchGroupCard>: a tiny cover (image, or the initial-letter placeholder via
 * <CoverImage>) beside the source name + language code. Presentation-only — the
 * candidate arrives via the `candidate` prop and the pill emits nothing.
 *
 * The cover is a mini fixed-width box; <CoverImage>'s placeholder/lazy-image
 * logic is reused and tuned to the pill's small radius + initial size via the
 * scoped `:deep` overrides below (CoverImage is built for full grid covers).
 * Token-only colours → both themes work.
 */
defineProps<{
  /** The per-source candidate this pill represents. */
  candidate: SearchCandidate
}>()
</script>

<template>
  <div class="pill">
    <span class="pill__cover">
      <CoverImage
        :src="candidate.thumbnailUrl"
        :alt="`${candidate.title} cover`"
        placeholder="initial"
        :initial="candidate.title"
        aspect="26 / 34"
      />
    </span>
    <span class="pill__meta">
      <span class="pill__source">{{ candidate.sourceName }}</span>
      <span class="pill__lang">{{ candidate.lang.toUpperCase() }}</span>
    </span>
  </div>
</template>

<style scoped>
.pill {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 11px;
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--surface);
}

.pill__cover {
  width: 26px;
  flex: none;
}

/* Tune the shared CoverImage to the pill's mini cover (small radius + glyph). */
.pill__cover :deep(.cover) {
  border-radius: 5px;
}

.pill__cover :deep(.cover__initial) {
  font-size: var(--text-lg);
}

.pill__meta {
  display: flex;
  flex-direction: column;
}

.pill__source {
  font-size: 12.5px;
  font-weight: var(--weight-semibold);
  color: var(--text);
}

.pill__lang {
  font-size: 10.5px;
  color: var(--faint);
}
</style>
