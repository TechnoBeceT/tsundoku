<script setup lang="ts">
import { computed } from 'vue'
import Spinner from '../ui/Spinner.vue'

/**
 * CycleBanner — the download-cycle status pill. While a cycle is running it
 * shows a spinner + "Download cycle in progress…"; when idle it shows a clock
 * glyph and either the minutes until the next cycle ("Next download cycle
 * ~14 min") or a plain "Idle" line when the interval is unknown.
 *
 * SSE-driven upstream: the parent flips `cycleActive` on the cycle.start /
 * cycle.done events and feeds `nextCycleMinutes` from the schedule.
 */
const props = withDefaults(defineProps<{
  /** Whether a download cycle is currently running. */
  cycleActive?: boolean
  /** Minutes until the next cycle; null hides the countdown ("Idle"). */
  nextCycleMinutes?: number | null
}>(), {
  cycleActive: false,
  nextCycleMinutes: null,
})

const label = computed(() =>
  props.cycleActive
    ? 'Download cycle in progress…'
    : props.nextCycleMinutes == null
      ? 'Idle — waiting for next cycle'
      : `Next download cycle ~${props.nextCycleMinutes} min`,
)
</script>

<template>
  <div class="cycle">
    <Spinner v-if="cycleActive" :size="11" tone="accent" />
    <svg v-else width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="var(--accentBright)" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <circle cx="12" cy="12" r="9" />
      <path d="M12 7v5l3 2" />
    </svg>
    {{ label }}
  </div>
</template>

<style scoped>
.cycle {
  display: flex;
  align-items: center;
  gap: var(--space-xs);
  padding: 0.4375rem var(--space-base); /* 7px 14px @16 (7px off-ladder) */
  border-radius: var(--radius-pill);
  background: var(--surface2);
  border: 1px solid var(--border);
  font-size: var(--text-sm);
  font-weight: var(--weight-semibold);
  color: var(--muted);
}
</style>
