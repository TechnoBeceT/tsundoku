<script setup lang="ts">
import { computed } from 'vue'
import { useNow } from '../../composables/useNow'
import { relativeTime } from '~/utils/timeFormat'
import SourceMetricRow from './SourceMetricRow.vue'
import SuccessRateMeter from './SuccessRateMeter.vue'
import TimelineHistogram from './TimelineHistogram.vue'
import EventTypeBreakdown from './EventTypeBreakdown.vue'
import EventTable from './EventTable.vue'
import type { SourceMetric } from '../screens/sourceHealth.types'
import type { SourceEventRecord, SourceReport, TimelineBucket, TimelineBucketSize } from './sourceReport.types'

/**
 * SourceReportRow — one source's accordion mini-dashboard. The always-visible
 * summary REUSES `SourceMetricRow` (when a matching metrics snapshot exists), so
 * every Tsundoku superset signal — warm/cold, backend `isSlow`, the erroring
 * badge, and the cooling-down breaker banner + Reset — is preserved verbatim,
 * never re-implemented. A chevron expands the row to reveal the event-sourced
 * report: the success-rate meter, the stacked success/fail timeline (the failure
 * cliff), the per-operation breakdown, and the source's recent events.
 *
 * Presentation-only + CONTROLLED: the parent owns `expanded` and lazy-loads the
 * timeline + recent events only once the row is opened (they arrive as props).
 *
 *   - `report` (required): the source's event rollup + breaker state.
 *   - `metric`: the matching search-metrics snapshot (warm/isSlow/latency +
 *     Reset id); null when the source has no metrics row yet.
 *   - `expanded` (default false): whether the body is open (controlled).
 *   - `timeline` / `timelinePending`: the source's bucketed series (lazy).
 *   - `recentEvents` / `eventsPending`: the source's recent events (lazy).
 *   - `timelineBucket` (default 'hour'): the histogram's axis granularity.
 *   - `resetting` (default false): the breaker reset is in flight (spins Reset).
 */
const props = withDefaults(defineProps<{
  /** The source's event rollup + breaker state. */
  report: SourceReport
  /** The matching search-metrics snapshot (null when none). */
  metric?: SourceMetric | null
  /** Whether the accordion body is open (controlled). */
  expanded?: boolean
  /** The lazily-loaded timeline buckets. */
  timeline?: TimelineBucket[]
  /** Whether the timeline is loading. */
  timelinePending?: boolean
  /** The lazily-loaded recent events for this source. */
  recentEvents?: SourceEventRecord[]
  /** Whether the recent events are loading. */
  eventsPending?: boolean
  /** The histogram axis granularity. */
  timelineBucket?: TimelineBucketSize
  /** Whether this source's breaker reset is in flight. */
  resetting?: boolean
}>(), {
  metric: null,
  expanded: false,
  timeline: () => [],
  timelinePending: false,
  recentEvents: () => [],
  eventsPending: false,
  timelineBucket: 'hour',
  resetting: false,
})

const emit = defineEmits<{
  /** The row was toggled — carries the source key so the parent lazy-loads once. */
  'toggle': [sourceKey: string]
  /** Reset this source's tripped breaker — carries the source id. */
  'reset': [id: string]
  /** A recent-event row was clicked — open its forensic detail. */
  'select-event': [event: SourceEventRecord]
}>()

const { now } = useNow()

const bodyId = computed(() => `report-row-${props.report.sourceKey.replace(/\s+/g, '-')}`)

// "Failing since" as a live relative label (only when the source is in a streak).
const failingSince = computed(() =>
  props.report.failingSince ? relativeTime(props.report.failingSince, now.value) : '')
</script>

<template>
  <section class="rr" :class="{ 'rr--open': expanded, 'rr--failing': report.failingSince != null }">
    <div class="rr__summary">
      <button
        type="button"
        class="rr__toggle"
        :aria-expanded="expanded"
        :aria-controls="bodyId"
        @click="emit('toggle', report.sourceKey)"
      >
        <svg class="rr__chevron" :class="{ 'rr__chevron--open': expanded }" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M9 18l6-6-6-6" /></svg>
      </button>

      <div class="rr__row">
        <!-- Preferred: the full metrics row (all superset badges + breaker + Reset). -->
        <SourceMetricRow
          v-if="metric"
          :source="metric"
          :resetting="resetting"
          @reset="emit('reset', $event)"
        />
        <!-- Fallback when a source has events but no metrics snapshot yet. -->
        <div v-else class="rr__fallback">
          <span class="rr__name">{{ report.sourceName }}</span>
          <span v-if="report.isCoolingDown" class="rr__badge rr__badge--cooling">Cooling down</span>
          <span v-else-if="report.failingSince" class="rr__badge rr__badge--failing">Failing</span>
          <span class="rr__fallback-stat">{{ report.totalEvents.toLocaleString() }} events</span>
        </div>
      </div>
    </div>

    <div v-show="expanded" :id="bodyId" class="rr__body">
      <div class="rr__metrics">
        <div class="rr__meter">
          <p class="rr__meter-label">Success rate · {{ report.totalEvents.toLocaleString() }} events</p>
          <SuccessRateMeter
            :rate="report.successRate"
            :success="report.successEvents"
            :total="report.totalEvents"
          />
        </div>
        <div v-if="report.failingSince" class="rr__streak">
          <span class="rr__streak-label">Failing since</span>
          <span class="rr__streak-value">{{ failingSince }} · {{ report.consecutiveFailures }} in a row</span>
        </div>
      </div>

      <div class="rr__section">
        <p class="rr__section-title">Activity over time</p>
        <p v-if="timelinePending" class="rr__loading">Loading timeline…</p>
        <TimelineHistogram v-else :buckets="timeline" :bucket-size="timelineBucket" />
      </div>

      <div class="rr__section">
        <p class="rr__section-title">By operation</p>
        <EventTypeBreakdown :items="report.byType" />
      </div>

      <div class="rr__section">
        <p class="rr__section-title">Recent events</p>
        <EventTable
          :events="recentEvents"
          :show-source="false"
          :pending="eventsPending"
          empty-label="No recent events for this source."
          @select="emit('select-event', $event)"
        />
      </div>
    </div>
  </section>
</template>

<style scoped>
.rr {
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  background: var(--surface);
  overflow: hidden;
}

.rr--failing {
  border-color: var(--danger-border);
}

.rr__summary {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 6px 6px 6px 2px;
}

/* The chevron toggle — a generous tap target that never nests the metric row's
 * own Reset button (which stays independently clickable). */
.rr__toggle {
  flex: none;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  min-height: 44px;
  border: none;
  background: none;
  color: var(--faint);
  cursor: pointer;
  border-radius: var(--radius-md);
}

.rr__toggle:hover { color: var(--text); }

.rr__toggle:focus-visible {
  outline: none;
  box-shadow: var(--ring-focus);
}

.rr__chevron {
  transition: transform 0.15s ease;
}

.rr__chevron--open {
  transform: rotate(90deg);
}

/* The metric row fills the summary; its own border reads as an inset panel. */
.rr__row {
  flex: 1 1 auto;
  min-width: 0;
}

.rr__fallback {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 4px;
}

.rr__name {
  font-weight: var(--weight-bold);
  font-size: 13.5px;
  color: var(--text);
}

.rr__badge {
  font-size: 9.5px;
  font-weight: var(--weight-extrabold);
  letter-spacing: 0.03em;
  text-transform: uppercase;
  padding: 2px 7px;
  border-radius: var(--radius-pill);
  color: var(--danger-text);
  background: var(--danger-bg);
}

.rr__fallback-stat {
  margin-left: auto;
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  color: var(--muted);
}

/* ---- Expanded body -------------------------------------------------------- */
.rr__body {
  display: flex;
  flex-direction: column;
  gap: 18px;
  padding: 6px 16px 18px 40px;
}

.rr__metrics {
  display: flex;
  flex-wrap: wrap;
  gap: 18px;
  align-items: flex-end;
}

.rr__meter {
  flex: 1 1 240px;
  min-width: 0;
}

.rr__meter-label,
.rr__section-title {
  margin: 0 0 8px;
  font-size: var(--text-2xs);
  font-weight: var(--weight-extrabold);
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--faint);
}

.rr__streak {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 8px 12px;
  border-radius: var(--radius-md);
  background: var(--danger-bg);
}

.rr__streak-label {
  font-size: var(--text-2xs);
  font-weight: var(--weight-extrabold);
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--danger-text);
}

.rr__streak-value {
  font-size: var(--text-sm);
  font-weight: var(--weight-semibold);
  color: var(--danger-text);
}

.rr__loading {
  margin: 0;
  font-size: var(--text-sm);
  color: var(--muted);
}

@media (max-width: 640px) {
  .rr__body {
    padding-left: 16px;
  }
}
</style>
