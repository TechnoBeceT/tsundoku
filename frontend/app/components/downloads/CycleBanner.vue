<script setup lang="ts">
import { computed } from 'vue'
import Spinner from '../ui/Spinner.vue'
import { useNow } from '../../composables/useNow'
import { formatRetryEta } from '../../utils/retryEta'
import { formatCountdown } from '../../utils/countdown'
import { isCycleBusy } from '../../utils/cycleSchedule'
import type { CycleState, CycleTimer } from '../../utils/cycleSchedule'

/**
 * CycleBanner — the download-cycle status pill beside the tabs. It states what the
 * engine is doing, taken from the server's own schedule (never guessed):
 *   - a cycle is running → spinner + "Download cycle in progress…", and when that
 *     cycle has already passed its own next-due instant, "· next due now" — cycles
 *     are running back-to-back with no idle gap, which is normal when a cycle takes
 *     longer than the configured period;
 *   - idle with a (mostly) DEFERRED queue — every waiting chapter's source is on a
 *     persisted cooldown — "N waiting on a source · retry ~Nm" with the SOONEST
 *     retry, never the misleading "Idle", which reads as "all done";
 *   - idle with a scheduled next cycle → the countdown ("Next download cycle 1:23");
 *   - the loop is not scheduled, or its schedule could not be read → said plainly,
 *     because those are different claims from "idle".
 *
 * The `cycle` prop comes from useCycleTimers (GET /api/engine/schedule, kept live by
 * the SSE boundaries); `deferralSummary` is derived from the loaded queued rows. The
 * retry ETA is computed against the shared ticking clock so it stays live without a
 * refetch.
 */
const props = withDefaults(defineProps<{
  /** The download loop's state from useCycleTimers; null = schedule not known. */
  cycle?: CycleTimer | null
  /**
   * Queue-deferral summary: how many loaded queued chapters are waiting on a
   * source cooldown, and the SOONEST next-attempt (ISO). null when nothing is
   * deferred — the pill then falls back to the schedule text.
   */
  deferralSummary?: { count: number, soonestIso: string } | null
}>(), {
  cycle: null,
  deferralSummary: null,
})

const { now } = useNow()

const state = computed<CycleState>(() => props.cycle?.state ?? 'unavailable')

// A cycle running (or about to) always wins the pill — a spinner while work is
// happening, never a "waiting" line beside a busy source strip.
const busy = computed(() => isCycleBusy(state.value))

// True only when nothing is running AND the queue is waiting on a cooldown — drives
// the pause glyph + the honest waiting label (in place of the schedule line).
const deferred = computed(() => !busy.value && props.deferralSummary != null)

const label = computed(() => {
  const summary = props.deferralSummary
  // Keyed by state so a new state in the contract fails to compile here.
  const scheduleLabels: Record<CycleState, string> = {
    running: 'Download cycle in progress…',
    overrunning: 'Download cycle in progress · next due now',
    starting: 'Starting next download cycle…',
    waiting: `Next download cycle ${formatCountdown(props.cycle?.remainingMs ?? 0)}`,
    unscheduled: 'Download cycle not scheduled',
    unavailable: 'Cycle schedule unavailable',
  }
  if (!busy.value && summary) {
    return `${summary.count} waiting on a source · retry ${formatRetryEta(summary.soonestIso, now.value)}`
  }
  return scheduleLabels[state.value]
})
</script>

<template>
  <div class="cycle" :class="{ 'cycle--warn': state === 'overrunning', 'cycle--faint': state === 'unscheduled' || state === 'unavailable' }">
    <Spinner v-if="busy" :size="11" tone="accent" />
    <!-- Deferred: a pause glyph — the queue is intentionally holding, not "done". -->
    <svg v-else-if="deferred" class="cycle__icon" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <rect x="6" y="5" width="4" height="14" rx="1" />
      <rect x="14" y="5" width="4" height="14" rx="1" />
    </svg>
    <svg v-else class="cycle__icon" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
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

/* The glyph carries the tone, so the pill's state is legible before the words are
   read: accent normally, amber when cycles are overrunning, faint when the cadence
   is unknown (unscheduled / unreadable). */
.cycle__icon {
  color: var(--accentBright);
}

.cycle--warn {
  color: var(--warn);
}

.cycle--warn .cycle__icon {
  color: var(--warn);
}

.cycle--faint .cycle__icon {
  color: var(--faint);
}
</style>
