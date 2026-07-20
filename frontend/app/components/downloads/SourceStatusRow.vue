<script setup lang="ts">
import { computed } from 'vue'
import { formatCompactDuration } from '../../utils/countdown'
import { humanizeSourceReason } from '../../utils/sourceReason'
import type { SourceStatus } from './sourceStatus.types'

/**
 * SourceStatusRow — one source on the live status strip. A downloading source
 * reads "Asura ● downloading 5/5"; a cooling source "Comix ⏸ cooling 12m
 * (rate-limited)". The status dot is accent for downloading, amber for cooling.
 * The full last-error is exposed on the row's title (hover) so the compact reason
 * never loses the detail. Purely presentational; token-only → both themes.
 */
const props = defineProps<{ source: SourceStatus }>()

// The trailing detail: "downloading N/cap" or "cooling <remaining> (<reason>)".
const detail = computed(() => {
  const s = props.source
  if (s.state === 'downloading') return `downloading ${s.activeCount}/${s.cap}`
  return `cooling ${formatCompactDuration(s.cooldownRemainingSeconds)} (${humanizeSourceReason(s.reason)})`
})
</script>

<template>
  <div class="src" :class="`src--${source.state}`" :title="source.lastError || undefined">
    <span class="src__dot" aria-hidden="true" />
    <span class="src__name">{{ source.sourceKey }}</span>
    <span class="src__detail">{{ detail }}</span>
  </div>
</template>

<style scoped>
.src {
  display: inline-flex;
  align-items: center;
  gap: var(--space-xs);
  padding: var(--space-2xs) var(--space-sm);
  border-radius: var(--radius-pill);
  background: var(--surface2);
  border: 1px solid var(--border);
  font-size: var(--text-sm);
  font-weight: var(--weight-semibold);
  white-space: nowrap;
}

.src__dot {
  width: 0.5rem;
  height: 0.5rem;
  border-radius: 50%;
  flex: none;
}

.src--downloading .src__dot {
  background: var(--accentBright);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--accentBright) 22%, transparent);
}

.src--cooling .src__dot {
  background: var(--warn);
}

.src__name {
  color: var(--text);
}

.src__detail {
  color: var(--muted);
  font-variant-numeric: tabular-nums;
}

.src--cooling .src__detail {
  color: var(--warn);
}
</style>
