<script setup lang="ts">
import { computed } from 'vue'

/**
 * Skeleton — a loading placeholder block with a shimmer sweep.
 *
 * A token-surfaced rectangle (`--surface2`) with an `::after` overlay swept by
 * the global `shimmer` keyframe (§16 loading state). The `variant` picks a
 * sensible default shape; `height` overrides the height for any variant.
 *
 *   - `variant` (default 'line'):
 *       · 'line'  → a single text-line bar (radius-sm, default height 12px)
 *       · 'row'   → a list/table row block (radius-md, default height 52px)
 *       · 'card'  → a card-sized block (radius-2xl, default height 120px)
 *       · 'cover' → a manga-cover block (radius-lg, 2:3 portrait aspect)
 *   - `height` (CSS length, optional): overrides the variant's default height.
 *     Ignored by 'cover' (its height follows the 2:3 aspect-ratio).
 */
const props = withDefaults(defineProps<{
  /** Placeholder shape — see the component doc above. */
  variant?: 'card' | 'row' | 'line' | 'cover'
  /** Explicit height (any CSS length); overrides the variant default. */
  height?: string
}>(), {
  variant: 'line',
  height: undefined,
})

// Default height per variant when `height` isn't supplied ('cover' uses an
// aspect-ratio instead, so it has no default height).
const DEFAULT_HEIGHT: Record<'card' | 'row' | 'line' | 'cover', string | undefined> = {
  line: '12px',
  row: '52px',
  card: '120px',
  cover: undefined,
}

const resolvedHeight = computed(() => props.height ?? DEFAULT_HEIGHT[props.variant])
</script>

<template>
  <div
    class="skeleton"
    :class="`skeleton--${variant}`"
    :style="resolvedHeight ? { height: resolvedHeight } : undefined"
    aria-hidden="true"
  />
</template>

<style scoped>
.skeleton {
  position: relative;
  width: 100%;
  overflow: hidden;
  background: var(--surface2);
}

.skeleton::after {
  content: '';
  position: absolute;
  inset: 0;
  transform: translateX(-100%);
  background: linear-gradient(90deg, transparent, var(--surface3), transparent);
  animation: shimmer 1.4s ease-in-out infinite;
}

.skeleton--line {
  border-radius: var(--radius-sm);
}

.skeleton--row {
  border-radius: var(--radius-md);
}

.skeleton--card {
  border-radius: var(--radius-2xl);
}

.skeleton--cover {
  aspect-ratio: 2 / 3;
  border-radius: var(--radius-lg);
}
</style>
