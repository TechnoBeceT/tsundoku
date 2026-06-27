<script setup lang="ts">
import { computed } from 'vue'
import BrandMark from './BrandMark.vue'

/**
 * CoverImage — a manga/series cover image with a built-in placeholder for when
 * no cover URL is available. Renders a lazy-loaded `<img>` when `src` is set,
 * otherwise a placeholder on the dark `--cover-placeholder` tile:
 *   - `placeholder="brand"` (default): the white inverse BrandMark.
 *   - `placeholder="initial"`: a big faint first-letter glyph (`initial`, else
 *     the first letter of `alt`).
 *
 * The box holds the prototype's portrait cover ratio (`aspect` default `0.72`,
 * i.e. the ~138% padding-bottom shape) and clips its content; pass `aspect` to
 * override (any CSS `aspect-ratio` value). Token-only colours → both themes work.
 */
const props = withDefaults(defineProps<{
  /** Cover image URL. Empty / undefined renders the placeholder. */
  src?: string
  /** Required alt text (also the source of the initial when none is given). */
  alt: string
  /** Which placeholder to show when there is no `src`. */
  placeholder?: 'brand' | 'initial'
  /** The letter to show for `placeholder="initial"` (falls back to `alt`'s first). */
  initial?: string
  /** CSS `aspect-ratio` for the cover box (default the prototype's ~0.72 portrait). */
  aspect?: string
}>(), {
  src: '',
  placeholder: 'brand',
  initial: '',
  aspect: '0.72',
})

// The placeholder letter: explicit `initial`, else the first char of `alt`.
const initialChar = computed(
  () => (props.initial || props.alt).trim()[0]?.toUpperCase() ?? '?',
)
</script>

<template>
  <div class="cover" :style="{ aspectRatio: aspect }">
    <img v-if="src" class="cover__img" :src="src" :alt="alt" loading="lazy">
    <div v-else class="cover__placeholder">
      <BrandMark v-if="placeholder === 'brand'" :size="44" tone="inverse" />
      <span v-else class="cover__initial">{{ initialChar }}</span>
    </div>
  </div>
</template>

<style scoped>
.cover {
  position: relative;
  width: 100%;
  overflow: hidden;
  border-radius: var(--radius-lg);
  background: var(--cover-placeholder);
}

.cover__img {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.cover__placeholder {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--cover-placeholder);
}

.cover__initial {
  font-family: var(--font-display);
  font-weight: var(--weight-black);
  font-size: 56px;
  line-height: 1;
  color: var(--disc-initial);
}
</style>
