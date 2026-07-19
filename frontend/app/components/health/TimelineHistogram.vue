<script setup lang="ts">
import { computed, ref } from 'vue'
import type { TimelineBucket, TimelineBucketSize } from './sourceReport.types'

/**
 * TimelineHistogram — the report's SIGNATURE view: a source's operations over
 * time as stacked green/red bars, one per time bucket. Success sits on the
 * baseline (emerald), failures stack above (rose), so a sudden wall of red is the
 * "failure cliff" — "failing since" made visual. Pure CSS (flex heights), zero
 * charting dependency.
 *
 * Identity is never colour-alone: a legend names both series, each bar carries a
 * hover tooltip with the exact counts + time, and the stack order is fixed
 * (failures always on top) so the split reads under colour-blindness (dataviz
 * status rule). Bar heights share ONE scale (the busiest bucket's total), so
 * magnitude is comparable across buckets.
 *
 *   - `buckets` (required): the ascending success/fail series (pre-bucketed by
 *     the API — never folded client-side).
 *   - `bucketSize` (default 'hour'): drives the time-label format.
 *   - `height` (default 132): the plot height in px.
 */
const props = withDefaults(defineProps<{
  /** The ascending time buckets (API-bucketed). */
  buckets: TimelineBucket[]
  /** Bucket granularity — drives the axis label format. */
  bucketSize?: TimelineBucketSize
  /** Plot height in px. */
  height?: number
}>(), {
  bucketSize: 'hour',
  height: 132,
})

// One shared scale: the busiest bucket's total. Guard an all-zero series.
const maxTotal = computed(() => Math.max(1, ...props.buckets.map(b => b.total)))

const hovered = ref<number | null>(null)

/** A short time-axis label for a bucket start (hour → "14:00", day → "Jul 12"). */
function axisLabel(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  return props.bucketSize === 'day'
    ? d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
    : d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
}

// The hovered bucket's tooltip payload (null when nothing is hovered).
const tip = computed(() => {
  if (hovered.value == null) return null
  const b = props.buckets[hovered.value]
  if (!b) return null
  return {
    time: axisLabel(b.bucket),
    success: b.success,
    failed: b.failed,
    total: b.total,
    left: props.buckets.length > 1 ? (hovered.value / (props.buckets.length - 1)) * 100 : 50,
  }
})

// First + last axis labels (the ends anchor the window).
const firstLabel = computed(() => (props.buckets[0] ? axisLabel(props.buckets[0].bucket) : ''))
const lastLabel = computed(() => {
  const last = props.buckets[props.buckets.length - 1]
  return last ? axisLabel(last.bucket) : ''
})

const empty = computed(() => props.buckets.length === 0 || props.buckets.every(b => b.total === 0))
</script>

<template>
  <div class="tl">
    <div class="tl__legend">
      <span class="tl__key"><span class="tl__swatch tl__swatch--ok" aria-hidden="true" />Success</span>
      <span class="tl__key"><span class="tl__swatch tl__swatch--fail" aria-hidden="true" />Failed</span>
    </div>

    <p v-if="empty" class="tl__empty">No activity in this window.</p>

    <template v-else>
      <div
        class="tl__plot"
        :style="{ height: `${height}px` }"
        role="img"
        aria-label="Operations over time, stacked by success and failure"
      >
        <div
          v-for="(b, i) in buckets"
          :key="b.bucket"
          class="tl__col"
          @mouseenter="hovered = i"
          @mouseleave="hovered = null"
        >
          <div class="tl__stack" :style="{ height: `${(b.total / maxTotal) * 100}%` }">
            <div v-if="b.failed > 0" class="tl__seg tl__seg--fail" :style="{ flexGrow: b.failed }" />
            <div v-if="b.success > 0" class="tl__seg tl__seg--ok" :style="{ flexGrow: b.success }" />
          </div>
        </div>

        <!-- Hover tooltip anchored over the active column. -->
        <div v-if="tip" class="tl__tip" :style="{ left: `${tip.left}%` }">
          <div class="tl__tip-time">{{ tip.time }}</div>
          <div class="tl__tip-row"><span class="tl__dot tl__dot--ok" />{{ tip.success }} ok</div>
          <div class="tl__tip-row"><span class="tl__dot tl__dot--fail" />{{ tip.failed }} failed</div>
        </div>
      </div>

      <div class="tl__axis">
        <span>{{ firstLabel }}</span>
        <span>{{ lastLabel }}</span>
      </div>
    </template>
  </div>
</template>

<style scoped>
.tl {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.tl__legend {
  display: flex;
  gap: 14px;
}

.tl__key {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: var(--text-xs);
  font-weight: var(--weight-semibold);
  color: var(--muted);
}

.tl__swatch {
  width: 9px;
  height: 9px;
  border-radius: 2px;
}

.tl__swatch--ok { background: var(--set-ok-dot); }
.tl__swatch--fail { background: var(--danger); }

.tl__empty {
  margin: 0;
  padding: 20px 0;
  font-size: var(--text-sm);
  color: var(--muted);
}

.tl__plot {
  position: relative;
  display: flex;
  align-items: flex-end;
  gap: 2px; /* the 2px surface gap between adjacent bars (dataviz spacer) */
  padding-top: 4px;
}

.tl__col {
  flex: 1 1 0;
  min-width: 0;
  height: 100%;
  display: flex;
  align-items: flex-end;
}

/* The stacked bar. Failure on top, success on the baseline; the 2px gap lets the
 * surface show between the two segments when both are present. */
.tl__stack {
  width: 100%;
  min-height: 2px;
  display: flex;
  flex-direction: column;
  gap: 2px;
  border-radius: 3px 3px 0 0;
  overflow: hidden;
}

.tl__seg {
  width: 100%;
}

.tl__seg--ok { background: var(--set-ok-dot); }
.tl__seg--fail { background: var(--danger); }

.tl__col:hover .tl__stack {
  filter: brightness(1.15);
}

/* Floating tooltip — centred over the hovered column, clamped inside the plot. */
.tl__tip {
  position: absolute;
  bottom: calc(100% + 6px);
  transform: translateX(-50%);
  max-width: 140px;
  padding: 7px 9px;
  border-radius: var(--radius-md);
  border: 1px solid var(--border2);
  background: var(--surface);
  box-shadow: var(--shadow);
  pointer-events: none;
  white-space: nowrap;
  z-index: 2;
}

.tl__tip-time {
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  color: var(--text);
  margin-bottom: 3px;
}

.tl__tip-row {
  display: flex;
  align-items: center;
  gap: 5px;
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  color: var(--muted);
}

.tl__dot {
  width: 7px;
  height: 7px;
  border-radius: var(--radius-pill);
}

.tl__dot--ok { background: var(--set-ok-dot); }
.tl__dot--fail { background: var(--danger); }

.tl__axis {
  display: flex;
  justify-content: space-between;
  font-family: var(--font-mono);
  font-size: var(--text-2xs);
  color: var(--faint);
}
</style>
