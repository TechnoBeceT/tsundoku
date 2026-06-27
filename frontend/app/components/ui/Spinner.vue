<script setup lang="ts">
import { computed } from 'vue'

/**
 * Spinner — a small bordered ring that rotates via the global `spin` keyframe,
 * used as an in-button / in-flight busy indicator (§16 loading state).
 *
 * The ring is a circle with one side knocked transparent so the rotation is
 * visible. Colour comes from the `tone`; size scales the border-width with it.
 *
 *   - `size` (px, default 16): rendered width = height of the ring.
 *   - `tone` (default 'current'): the ring colour —
 *       · 'current'   → inherits `currentColor` (sits inside coloured text/buttons)
 *       · 'accent'    → the brand accent (`--accent`)
 *       · 'on-accent' → white-ish, for placing ON an accent/solid button (`--cover-text`)
 *       · 'dark'      → the page background colour (`--bg`), for light chips
 */
const props = withDefaults(defineProps<{
  /** Rendered diameter in px (width = height). */
  size?: number
  /** Ring colour treatment — see the component doc above. */
  tone?: 'accent' | 'current' | 'on-accent' | 'dark'
}>(), {
  size: 16,
  tone: 'current',
})

// Map the tone to a token-backed colour (never a raw hex).
const ringColor = computed(() => ({
  'accent': 'var(--accent)',
  'current': 'currentColor',
  'on-accent': 'var(--cover-text)',
  'dark': 'var(--bg)',
}[props.tone]))

// Border scales with size so a large spinner doesn't look hairline-thin.
const borderWidth = computed(() => Math.max(2, Math.round(props.size / 8)))
</script>

<template>
  <span
    class="spinner"
    role="status"
    aria-label="Loading"
    :style="{
      width: `${size}px`,
      height: `${size}px`,
      borderWidth: `${borderWidth}px`,
      borderColor: ringColor,
      borderRightColor: 'transparent',
    }"
  />
</template>

<style scoped>
.spinner {
  display: inline-block;
  box-sizing: border-box;
  border-style: solid;
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}
</style>
