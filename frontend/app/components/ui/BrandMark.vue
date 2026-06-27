<script setup lang="ts">
import { computed, useId } from 'vue'

/**
 * BrandMark — the Tsundoku product mark: three stacked rounded-rect "volumes",
 * the top one tilted -9° and carrying a small download-arrow glyph ("a pile
 * that downloads itself"). Geometry is the prototype's primary lockup (viewBox
 * 0 0 128 128).
 *
 * The mark re-tints from the brand TOKENS, never frozen hex, so it follows the
 * theme/accent:
 *   - `gradient` (default): volumes filled with the accent gradient
 *     (--brandGradFrom → --brandGradTo); white download arrow. The primary mark.
 *   - `mono`: a single-colour silhouette in `currentColor`; the arrow is knocked
 *     out to `--bg`. GOTCHA: the knockout assumes a `--bg`-coloured backdrop —
 *     on a raised surface the arrow may read faintly; use `gradient` there.
 *   - `inverse`: white volumes with an accent-deep arrow, for placing on an
 *     accent/photographic background.
 */
const props = withDefaults(defineProps<{
  /** Rendered square size in px (width = height). */
  size?: number
  /** Colour treatment — see the component doc above. */
  tone?: 'gradient' | 'mono' | 'inverse'
}>(), {
  size: 28,
  tone: 'gradient',
})

// Unique gradient id per instance so multiple marks on one page never collide.
const gradId = `tsundoku-mark-grad-${useId()}`

// Fill for the three volumes, derived from the tone.
const volumeFill = computed(() => {
  if (props.tone === 'mono') return 'currentColor'
  if (props.tone === 'inverse') return '#fff'
  return `url(#${gradId})`
})

// Stroke for the download-arrow glyph, derived from the tone.
const glyphStroke = computed(() => {
  if (props.tone === 'mono') return 'var(--bg)'
  if (props.tone === 'inverse') return 'var(--accentDeep)'
  return '#fff'
})

// The small spine-highlight rects read as a soft sheen; opacity tracks tone so
// the mono silhouette stays clean.
const highlightOpacity = computed(() => (props.tone === 'mono' ? 0.5 : 0.9))
</script>

<template>
  <svg
    :width="size"
    :height="size"
    viewBox="0 0 128 128"
    role="img"
    aria-label="Tsundoku"
    xmlns="http://www.w3.org/2000/svg"
  >
    <defs v-if="tone === 'gradient'">
      <linearGradient :id="gradId" x1="0" y1="0" x2="1" y2="1">
        <stop offset="0" style="stop-color: var(--brandGradFrom)" />
        <stop offset="1" style="stop-color: var(--brandGradTo)" />
      </linearGradient>
    </defs>

    <!-- bottom volume -->
    <g>
      <rect x="24" y="82" width="80" height="22" rx="6.5" :fill="volumeFill" />
      <rect x="34" y="88" width="11" height="10" rx="2.5" fill="#fff" :opacity="highlightOpacity * 0.6" />
    </g>

    <!-- middle volume -->
    <g>
      <rect x="20" y="56" width="88" height="22" rx="6.5" :fill="volumeFill" />
      <rect x="30" y="62" width="11" height="10" rx="2.5" fill="#fff" :opacity="highlightOpacity * 0.6" />
    </g>

    <!-- top volume (tilted — the unread one) with the download glyph -->
    <g transform="rotate(-9 64 39)">
      <rect x="30" y="26" width="74" height="22" rx="6.5" :fill="volumeFill" />
      <g
        transform="translate(50 31.5)"
        :stroke="glyphStroke"
        stroke-width="2.4"
        stroke-linecap="round"
        stroke-linejoin="round"
        fill="none"
      >
        <path d="M3 0v9" />
        <path d="M0 6l3 3 3-3" />
      </g>
    </g>
  </svg>
</template>
