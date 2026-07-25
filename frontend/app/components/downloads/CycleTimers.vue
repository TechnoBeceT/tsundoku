<script setup lang="ts">
import { computed } from 'vue'
import Spinner from '../ui/Spinner.vue'
import { formatCountdown } from '../../utils/countdown'
import { isCycleBusy, isCycleUnknown } from '../../utils/cycleSchedule'
import type { CycleState, CycleTimer } from '../../utils/cycleSchedule'

/**
 * CycleTimers — the two live header countdowns rendered as one pill ("Next
 * download 0:43 · Next refresh 1:52:08"), one segment per background loop.
 *
 * Each segment renders the loop's state verbatim, and every state the schedule
 * contract can express reads differently:
 *   waiting      the countdown ("Next download 0:43");
 *   running      spinner + "Download cycle running…";
 *   overrunning  spinner + "…running · next due now" in the warning tone — the
 *                cycle is taking longer than its own period, so cycles are running
 *                back-to-back with no idle gap. Deliberately shows NO countdown:
 *                a 0:00 clock would read as broken rather than as busy;
 *   starting     spinner + "…starting…" (due, between cycles);
 *   unscheduled  muted "…not scheduled" — the loop is not running at all, which is
 *                a different claim from "the next run is late";
 *   unavailable  muted "…schedule unavailable" — the schedule could not be read.
 *
 * Purely presentational: useCycleTimers owns the fetching, the SSE liveness and the
 * server-clock correction; this component only formats what it is handed.
 * Token-only styling → both themes render.
 */
const props = withDefaults(defineProps<{
  /** The download-cycle loop's state; null renders the "unavailable" segment. */
  download?: CycleTimer | null
  /** The discovery-sweep loop's state; null renders the "unavailable" segment. */
  refresh?: CycleTimer | null
}>(), {
  download: null,
  refresh: null,
})

/** What one segment renders: its text, its tone class, and whether it spins. */
interface Segment {
  label: string
  busy: boolean
  tone: 'normal' | 'warn' | 'faint'
}

/**
 * Build one segment's view.
 *
 * @param timer    the loop's state, or null when nothing is known.
 * @param busyName how the loop is named while it works ("Download cycle").
 * @param nextName how the loop is named while it waits ("Next download").
 */
function segment(timer: CycleTimer | null, busyName: string, nextName: string): Segment {
  const state = timer?.state ?? 'unavailable'
  // Keyed by state rather than chained, so adding a state to the contract fails to
  // compile here instead of silently falling through to the countdown.
  const labels: Record<CycleState, string> = {
    running: `${busyName} running…`,
    overrunning: `${busyName} running · next due now`,
    starting: `${busyName} starting…`,
    waiting: `${nextName} ${formatCountdown(timer?.remainingMs ?? 0)}`,
    unscheduled: `${busyName} not scheduled`,
    unavailable: `${busyName} schedule unavailable`,
  }
  return {
    label: labels[state],
    busy: isCycleBusy(state),
    tone: state === 'overrunning' ? 'warn' : isCycleUnknown(state) ? 'faint' : 'normal',
  }
}

const downloadSeg = computed(() => segment(props.download, 'Download cycle', 'Next download'))
const refreshSeg = computed(() => segment(props.refresh, 'Refresh', 'Next refresh'))
</script>

<template>
  <div class="timers">
    <span class="timers__seg" :class="`timers__seg--${downloadSeg.tone}`">
      <Spinner v-if="downloadSeg.busy" :size="11" tone="accent" />
      <svg v-else width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <circle cx="12" cy="12" r="9" />
        <path d="M12 7v5l3 2" />
      </svg>
      <span class="timers__label">{{ downloadSeg.label }}</span>
    </span>

    <span class="timers__divider" aria-hidden="true">·</span>

    <span class="timers__seg" :class="`timers__seg--${refreshSeg.tone}`">
      <Spinner v-if="refreshSeg.busy" :size="11" tone="accent" />
      <svg v-else width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <path d="M21 12a9 9 0 1 1-2.6-6.4" />
        <path d="M21 3v6h-6" />
      </svg>
      <span class="timers__label">{{ refreshSeg.label }}</span>
    </span>
  </div>
</template>

<style scoped>
.timers {
  display: inline-flex;
  align-items: center;
  gap: var(--space-sm);
  padding: 0.4375rem var(--space-base); /* 7px 14px @16 — matches CycleBanner */
  border-radius: var(--radius-pill);
  background: var(--surface2);
  border: 1px solid var(--border);
  font-size: var(--text-sm);
  font-weight: var(--weight-semibold);
  color: var(--muted);
}

.timers__seg {
  display: inline-flex;
  align-items: center;
  gap: var(--space-xs);
}

/* The icon inherits the segment's tone via currentColor, so an overrunning or
   unknown segment is distinguishable at a glance, not only by its wording. */
.timers__seg--normal {
  color: var(--accentBright);
}

.timers__seg--warn {
  color: var(--warn);
}

.timers__seg--faint {
  color: var(--faint);
}

/* The label keeps the pill's neutral text colour; only the icon carries the tone,
   except in the warning case where the whole segment should catch the eye. */
.timers__seg--normal .timers__label,
.timers__seg--faint .timers__label {
  color: var(--muted);
}

.timers__label {
  font-variant-numeric: tabular-nums;
}

.timers__divider {
  color: var(--faint);
}
</style>
