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
 *
 *   - `markSize` (default 44): the placeholder BrandMark size (px) — shrink it for
 *     the small download/import thumbnails.
 *   - `radius` (default `var(--radius-lg)`): the CSS corner radius token.
 *   - `clickable` (default false): render the root as a `<button>` and emit
 *     `click` — for the small tappable download/import thumbnails.
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
  /** Placeholder BrandMark size in px (smaller for thumbnails). */
  markSize?: number
  /** CSS corner-radius token for the box. */
  radius?: string
  /** Render the root as a `<button>` and emit `click`. */
  clickable?: boolean
}>(), {
  src: '',
  placeholder: 'brand',
  initial: '',
  aspect: '0.72',
  markSize: 44,
  radius: 'var(--radius-lg)',
  clickable: false,
})

const emit = defineEmits<{
  /** The cover was clicked (only when `clickable`). */
  click: []
}>()

// The placeholder letter: explicit `initial`, else the first char of `alt`.
const initialChar = computed(
  () => (props.initial || props.alt).trim()[0]?.toUpperCase() ?? '?',
)
</script>

<template>
  <component
    :is="clickable ? 'button' : 'div'"
    class="cover"
    :class="{ 'cover--clickable': clickable }"
    :type="clickable ? 'button' : undefined"
    :style="{ aspectRatio: aspect, borderRadius: radius }"
    @click="clickable && emit('click')"
  >
    <img v-if="src" class="cover__img" :src="src" :alt="alt" loading="lazy">
    <div v-else class="cover__placeholder">
      <BrandMark v-if="placeholder === 'brand'" :size="markSize" tone="inverse" />
      <span v-else class="cover__initial">{{ initialChar }}</span>
    </div>
  </component>
</template>

<style scoped>
.cover {
  position: relative;
  width: 100%;
  overflow: hidden;
  border-radius: var(--radius-lg);
  background: var(--cover-placeholder);
}

/* Button reset for the clickable thumbnail variant — strip the native chrome so
   the box looks identical to the div, plus a pointer + focus ring. */
.cover--clickable {
  display: block;
  padding: 0;
  border: none;
  font: inherit;
  color: inherit;
  text-align: inherit;
  cursor: pointer;
}

.cover--clickable:focus-visible {
  outline: none;
  box-shadow: var(--ring-focus);
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
