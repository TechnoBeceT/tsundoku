<script setup lang="ts">
import { computed } from 'vue'

/**
 * HealthBadge — a labelled pill + dot for a provider's source-health state.
 *
 * Tints itself from the existing `--sd-hl-<health>-{fg,bg,dot}` health tokens
 * (in `tokens/seriesDetail.css`, theme-independent). Mirrors `StatusBadge`'s
 * shape but for the three provider-health states.
 *
 *   - `health` (required): 'ok' | 'stale' | 'erroring'
 *     → labelled Healthy / Stale / Erroring (prototype wording).
 */
const props = defineProps<{
  /** The provider health to render — drives both the label and the colour. */
  health: 'ok' | 'stale' | 'erroring'
}>()

const LABELS: Record<'ok' | 'stale' | 'erroring', string> = {
  ok: 'Healthy',
  stale: 'Stale',
  erroring: 'Erroring',
}

const label = computed(() => LABELS[props.health])

// Local custom-props point at the per-health token triple (lives once).
const vars = computed(() => ({
  '--badge-fg': `var(--sd-hl-${props.health}-fg)`,
  '--badge-bg': `var(--sd-hl-${props.health}-bg)`,
  '--badge-dot': `var(--sd-hl-${props.health}-dot)`,
}))
</script>

<template>
  <span class="badge" :style="vars">
    <span class="badge__dot" aria-hidden="true" />
    {{ label }}
  </span>
</template>

<style scoped>
.badge {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 2px 9px;
  border-radius: var(--radius-pill);
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  line-height: 1.7;
  white-space: nowrap;
  color: var(--badge-fg);
  background: var(--badge-bg);
}

.badge__dot {
  width: 6px;
  height: 6px;
  border-radius: var(--radius-pill);
  flex-shrink: 0;
  background: var(--badge-dot);
}
</style>
