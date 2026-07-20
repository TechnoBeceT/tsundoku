<script setup lang="ts">
import { computed } from 'vue'
import Spinner from '../ui/Spinner.vue'
import { formatCountdown } from '../../utils/countdown'

/**
 * CycleTimers — the two live header countdowns: time until the next download cycle
 * and the next refresh sweep, rendered as one pill ("Next download 0:43 · Next
 * refresh 1:52:08"). Each segment flips to a spinner + "…running…" while that job
 * is in flight, and to a neutral "waiting…" when its schedule is not yet known
 * (first mount, before the interval loads / any SSE boundary arrives).
 *
 * Purely presentational: the parent (via useCycleTimers) owns the SSE-derived
 * running flags and the live remaining-ms values; this component only formats them.
 * Token-only styling → both themes render.
 */
const props = withDefaults(defineProps<{
  /** Whether a download cycle is running right now (SSE cycle.start/cycle.done). */
  downloadRunning?: boolean
  /** Whether a refresh sweep is running right now (SSE refresh.start/refresh.done). */
  refreshRunning?: boolean
  /** Milliseconds until the next download cycle; null = unknown ("waiting…"). */
  downloadRemainingMs?: number | null
  /** Milliseconds until the next refresh sweep; null = unknown ("waiting…"). */
  refreshRemainingMs?: number | null
}>(), {
  downloadRunning: false,
  refreshRunning: false,
  downloadRemainingMs: null,
  refreshRemainingMs: null,
})

const downloadLabel = computed(() => {
  if (props.downloadRunning) return 'Download cycle running…'
  if (props.downloadRemainingMs == null) return 'Next download waiting…'
  return `Next download ${formatCountdown(props.downloadRemainingMs)}`
})

const refreshLabel = computed(() => {
  if (props.refreshRunning) return 'Refresh running…'
  if (props.refreshRemainingMs == null) return 'Next refresh waiting…'
  return `Next refresh ${formatCountdown(props.refreshRemainingMs)}`
})
</script>

<template>
  <div class="timers">
    <span class="timers__seg">
      <Spinner v-if="downloadRunning" :size="11" tone="accent" />
      <svg v-else width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="var(--accentBright)" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <circle cx="12" cy="12" r="9" />
        <path d="M12 7v5l3 2" />
      </svg>
      <span class="timers__label">{{ downloadLabel }}</span>
    </span>

    <span class="timers__divider" aria-hidden="true">·</span>

    <span class="timers__seg">
      <Spinner v-if="refreshRunning" :size="11" tone="accent" />
      <svg v-else width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="var(--accentBright)" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <path d="M21 12a9 9 0 1 1-2.6-6.4" />
        <path d="M21 3v6h-6" />
      </svg>
      <span class="timers__label">{{ refreshLabel }}</span>
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

.timers__label {
  font-variant-numeric: tabular-nums;
}

.timers__divider {
  color: var(--faint);
}
</style>
