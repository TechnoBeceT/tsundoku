<script setup lang="ts">
import { computed } from 'vue'
import Spinner from '../ui/Spinner.vue'
import { useNow } from '../../composables/useNow'
import { formatRetryEta } from '../../utils/retryEta'

/**
 * CycleBanner — the download-cycle status pill. While a cycle is running it
 * shows a spinner + "Download cycle in progress…". When idle it is HONEST about
 * why nothing is moving:
 *   - if the queue is (mostly) DEFERRED — every waiting chapter's source is on a
 *     persisted cooldown — it shows "N waiting on a source · retry ~Nm" with the
 *     SOONEST retry (never the misleading "Idle — waiting for next cycle", which
 *     reads as "all done");
 *   - else if a next-cycle interval is known, the countdown ("Next download cycle
 *     ~14 min");
 *   - else the plain "Idle" line.
 *
 * SSE-driven upstream: the parent flips `cycleActive` on the cycle.start /
 * cycle.done events, feeds `nextCycleMinutes` from the schedule, and derives
 * `deferralSummary` from the loaded queued rows. The retry ETA is computed against
 * the shared ticking clock so it stays live without a refetch.
 */
const props = withDefaults(defineProps<{
  /** Whether a download cycle is currently running. */
  cycleActive?: boolean
  /** Minutes until the next cycle; null hides the countdown ("Idle"). */
  nextCycleMinutes?: number | null
  /**
   * Queue-deferral summary: how many loaded queued chapters are waiting on a
   * source cooldown, and the SOONEST next-attempt (ISO). null when nothing is
   * deferred — the pill then falls back to the countdown / idle text.
   */
  deferralSummary?: { count: number, soonestIso: string } | null
}>(), {
  cycleActive: false,
  nextCycleMinutes: null,
  deferralSummary: null,
})

const { now } = useNow()

// True only when idle AND the queue is waiting on a cooldown — drives the pause
// glyph + the honest waiting label (in place of the idle line).
const deferred = computed(() => !props.cycleActive && props.deferralSummary != null)

const label = computed(() => {
  if (props.cycleActive) return 'Download cycle in progress…'
  const summary = props.deferralSummary
  if (summary) {
    const eta = formatRetryEta(summary.soonestIso, now.value)
    return `${summary.count} waiting on a source · retry ${eta}`
  }
  return props.nextCycleMinutes == null
    ? 'Idle — waiting for next cycle'
    : `Next download cycle ~${props.nextCycleMinutes} min`
})
</script>

<template>
  <div class="cycle">
    <Spinner v-if="cycleActive" :size="11" tone="accent" />
    <!-- Deferred: a pause glyph — the queue is intentionally holding, not "done". -->
    <svg v-else-if="deferred" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="var(--accentBright)" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <rect x="6" y="5" width="4" height="14" rx="1" />
      <rect x="14" y="5" width="4" height="14" rx="1" />
    </svg>
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
