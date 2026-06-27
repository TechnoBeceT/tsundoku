<script setup lang="ts">
import { computed } from 'vue'

/**
 * ProgressBar — a slim rounded track with a filled bar.
 *
 * Two modes, chosen by whether `value` is given:
 *   - DETERMINATE: pass `value` (0–100) → the bar fills to that percentage.
 *   - INDETERMINATE: omit `value` → a short bar slides back and forth via the
 *     global `slide` keyframe (an in-flight, unknown-duration job).
 *
 * Props:
 *   - `value` (0–100, optional): fill percentage; omit for indeterminate.
 *   - `track` (CSS colour, default `var(--surface3)`): the unfilled groove.
 *   - `tone`  (CSS colour, default `var(--accent)`): the filled bar colour.
 *     Pass a token-backed `var(--…)` value; never a raw hex.
 */
const props = withDefaults(defineProps<{
  /** Fill percentage 0–100; omit for an indeterminate sliding bar. */
  value?: number
  /** Track (groove) colour — a token-backed CSS value. */
  track?: string
  /** Filled-bar colour — a token-backed CSS value. */
  tone?: string
}>(), {
  value: undefined,
  track: 'var(--surface3)',
  tone: 'var(--accent)',
})

const indeterminate = computed(() => props.value === undefined)

// Clamp a determinate value into [0, 100] so a stray prop can't overflow.
const pct = computed(() => Math.min(100, Math.max(0, props.value ?? 0)))

const barStyle = computed(() => indeterminate.value
  ? { width: '42%', background: props.tone }
  : { width: `${pct.value}%`, background: props.tone })
</script>

<template>
  <div
    class="progress"
    role="progressbar"
    :aria-valuenow="indeterminate ? undefined : pct"
    aria-valuemin="0"
    aria-valuemax="100"
    :style="{ background: track }"
  >
    <div
      class="progress__bar"
      :class="{ 'progress__bar--indeterminate': indeterminate }"
      :style="barStyle"
    />
  </div>
</template>

<style scoped>
.progress {
  width: 100%;
  height: 5px;
  border-radius: var(--radius-pill);
  overflow: hidden;
}

.progress__bar {
  height: 100%;
  border-radius: var(--radius-pill);
}

.progress__bar--indeterminate {
  animation: slide 1.2s ease-in-out infinite;
}
</style>
