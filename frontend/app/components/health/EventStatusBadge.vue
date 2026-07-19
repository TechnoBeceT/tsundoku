<script setup lang="ts">
import { computed } from 'vue'
import type { EventStatus } from './sourceReport.types'

/**
 * EventStatusBadge — a labelled pill for a source-EVENT outcome (success /
 * failed).
 *
 * Distinct from `ui/StatusBadge`, which renders a chapter's download STATE; this
 * one is the report's binary event outcome (named to avoid the auto-import
 * collision). Success is emerald (the shared
 * `--set-ok-*` tokens), failed is rose (`--danger-*`). It ships with a glyph +
 * text label, never colour alone, so the good/bad distinction survives colour-
 * blindness (dataviz status rule). Token-only → both themes.
 *
 *   - `status` (required): the event outcome.
 *   - `dense` (default false): drop the label to just the dot+glyph (for tight
 *     table cells where a column header already says "Status").
 */
const props = withDefaults(defineProps<{
  /** The event outcome — drives colour, glyph, and label. */
  status: EventStatus
  /** Compact form: glyph only, no text label. */
  dense?: boolean
}>(), {
  dense: false,
})

const label = computed(() => (props.status === 'success' ? 'Success' : 'Failed'))
</script>

<template>
  <span class="ev-status" :class="`ev-status--${status}`">
    <svg v-if="status === 'success'" class="ev-status__glyph" width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M20 6 9 17l-5-5" /></svg>
    <svg v-else class="ev-status__glyph" width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M18 6 6 18M6 6l12 12" /></svg>
    <span v-if="!dense">{{ label }}</span>
  </span>
</template>

<style scoped>
.ev-status {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 8px;
  border-radius: var(--radius-pill);
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  line-height: 1.7;
  white-space: nowrap;
}

.ev-status--success {
  color: var(--set-ok-text);
  background: var(--set-ok-bg);
}

.ev-status--failed {
  color: var(--danger-text);
  background: var(--danger-bg);
}

.ev-status__glyph {
  flex: none;
}
</style>
