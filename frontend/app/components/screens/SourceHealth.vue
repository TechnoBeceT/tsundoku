<script setup lang="ts">
import { computed } from 'vue'
import SegmentedTabs from '../ui/SegmentedTabs.vue'
import SelectField from '../ui/SelectField.vue'
import SurfaceCard from '../ui/SurfaceCard.vue'
import ErrorBanner from '../ui/ErrorBanner.vue'
import Skeleton from '../ui/Skeleton.vue'
import StatCard from '../health/StatCard.vue'
import HBarChart, { type HBarDatum } from '../health/HBarChart.vue'
import RecentErrorsTable from '../health/RecentErrorsTable.vue'
import SourceReportRow from '../health/SourceReportRow.vue'
import EventLogFilters from '../health/EventLogFilters.vue'
import EventTable from '../health/EventTable.vue'
import EventDetailDialog from '../health/EventDetailDialog.vue'
import SourceMetricsPane from '../health/SourceMetricsPane.vue'
import { REPORT_PERIOD_TABS } from '~/utils/healthTabs'
import { formatDurationMs, relativeTime } from '~/utils/timeFormat'
import type { SourceHealthReportModel } from '~/composables/useSourceHealthReport'
import type { ReportPeriod, ReportSort } from '../health/sourceReport.types'
import type { SourceMetric } from './sourceHealth.types'

/**
 * SourceHealth — the "Sources" tab of the `/health` console. It stacks the
 * Kaizoku-grade Source Metrics report (KPI cards → leaderboards → recent errors →
 * per-source accordion → event log) ABOVE the relocated search-latency metrics
 * pane, on one scroll (the single-column GROW model).
 *
 * Presentation only (props-down): the `report` bundle carries every piece of
 * report data + the handlers the sections drive (period/sort, accordion
 * expansion, event-log filters/paging, forensic-modal selection), assembled by
 * the page's `useSourceHealthReport`. The relocated metrics pane keeps its own
 * slice-3 props/emits (`warm-now`, `reset-breaker`) unchanged, so warm/cold +
 * isSlow + breaker Reset never regress. `report` is optional — when absent only
 * the metrics pane shows (the slice-3 shape). Token-only colours → both themes.
 */
const props = withDefaults(defineProps<{
  // ── Kaizoku-grade report (optional; absent → metrics pane only) ────────────
  /** The whole report data + handler bundle (from useSourceHealthReport). */
  report?: SourceHealthReportModel | null
  // ── Relocated search-metrics pane (slice 3 — unchanged) ────────────────────
  /** The per-source metric rows (slowest-first). */
  metrics: SourceMetric[]
  /** Whether the metrics list is loading. */
  pending?: boolean
  /** A metrics-load failure, surfaced inline in the pane. */
  error?: string | null
  /** Whether a warm-up pass is in flight. */
  warming?: boolean
  /** The last warm-up's success note. */
  warmMessage?: string | null
  /** The last warm-up's failure message. */
  warmError?: string | null
  /** The source id whose breaker reset is in flight (null when none). */
  resetting?: string | null
  /** The last breaker-reset failure message. */
  resetError?: string | null
}>(), {
  report: null,
  pending: false,
  error: null,
  warming: false,
  warmMessage: null,
  warmError: null,
  resetting: null,
  resetError: null,
})

const emit = defineEmits<{
  /** Trigger a manual warm-up pass across all sources. */
  'warm-now': []
  /** Reset a source's tripped circuit-breaker — carries the source id. */
  'reset-breaker': [id: string]
  /** Purge all of Tsundoku's DB state for a source — carries its id + name. */
  'purge-source': [source: { id: string, name: string }]
}>()

// Convenience alias — the template reads `r.*` for the report bundle.
const r = computed(() => props.report)

// Whether the initial report load is still in flight (no data yet).
const reportLoading = computed(() => r.value?.reportPending === true && r.value?.overview == null)

// ── KPI cards (from the overview) ─────────────────────────────────────────────
const kpiCards = computed(() => {
  const o = r.value?.overview
  if (!o) return []
  const k = o.kpis
  const ratePct = Math.round(k.successRate * 100)
  const rateTone = k.successRate >= 0.95
    ? 'var(--set-ok-dot)'
    : k.successRate >= 0.8 ? 'var(--set-update-text)' : 'var(--danger)'
  return [
    { key: 'rate', label: 'Success rate', value: `${ratePct}%`, tone: rateTone, hint: `${k.successEvents.toLocaleString()} / ${k.totalEvents.toLocaleString()} events` },
    { key: 'total', label: 'Operations', value: k.totalEvents.toLocaleString(), tone: 'var(--text)', hint: `in the last ${o.period}` },
    { key: 'failed', label: 'Failures', value: k.failedEvents.toLocaleString(), tone: k.failedEvents > 0 ? 'var(--danger)' : 'var(--set-ok-dot)', hint: k.failedEvents > 0 ? 'need attention' : 'all clear' },
    { key: 'active', label: 'Active sources', value: k.activeSources.toLocaleString(), tone: 'var(--text)', hint: 'produced events' },
    { key: 'failing', label: 'Failing now', value: o.failingSources.length.toLocaleString(), tone: o.failingSources.length > 0 ? 'var(--danger)' : 'var(--set-ok-dot)', hint: o.failingSources.length > 0 ? 'in a streak' : 'none failing' },
  ]
})

// ── Leaderboards ──────────────────────────────────────────────────────────────
const slowestBars = computed<HBarDatum[]>(() =>
  (r.value?.overview?.slowestSources ?? []).map(s => ({
    key: s.sourceKey,
    label: s.sourceName,
    value: s.ewmaLatencyMs,
    valueLabel: formatDurationMs(s.ewmaLatencyMs),
  })))

const failingBars = computed<HBarDatum[]>(() =>
  (r.value?.overview?.failingSources ?? []).map(s => ({
    key: s.sourceKey,
    label: s.sourceKey,
    value: s.consecutiveFailures,
    valueLabel: `${s.consecutiveFailures}× · ${relativeTime(s.failingSince)}`,
  })))

// ── Sort control (orders the per-source accordion) ────────────────────────────
const sortOptions: { value: ReportSort, label: string }[] = [
  { value: 'failures', label: 'Most failures' },
  { value: 'latency', label: 'Slowest' },
  { value: 'events', label: 'Most active' },
]

const skeletons = [0, 1, 2, 3]

function onModalOpen(open: boolean): void {
  if (!open) r.value?.closeEvent()
}
</script>

<template>
  <div class="source-health">
    <!-- ── Kaizoku-grade Source Metrics report ─────────────────────────────── -->
    <template v-if="r">
      <!-- Report header: title + period selector. -->
      <header class="report-head">
        <div>
          <h1 class="report-head__title">Source metrics</h1>
          <p class="report-head__sub">Reliability, latency, and the full operation log across every source.</p>
        </div>
        <SegmentedTabs
          :model-value="r.period"
          :tabs="REPORT_PERIOD_TABS"
          @update:model-value="r.setPeriod($event as ReportPeriod)"
        />
      </header>

      <ErrorBanner v-if="r.reportError" :message="r.reportError" />

      <!-- KPI cards. -->
      <div v-if="reportLoading" class="report-kpis">
        <Skeleton v-for="n in skeletons" :key="n" variant="card" height="86px" />
      </div>
      <div v-else class="report-kpis">
        <StatCard
          v-for="c in kpiCards"
          :key="c.key"
          :label="c.label"
          :value="c.value"
          :tone="c.tone"
          :hint="c.hint"
        />
      </div>

      <!-- Leaderboards + recent errors. -->
      <div class="report-grid">
        <SurfaceCard title="Slowest sources" sub="Rolling search latency (EWMA)">
          <HBarChart :items="slowestBars" tone="var(--set-update-text)" empty-label="No latency recorded yet." />
        </SurfaceCard>

        <SurfaceCard title="Failing now" sub="Consecutive failures, longest streak first">
          <HBarChart :items="failingBars" tone="var(--danger)" empty-label="No source is failing — all clear." />
        </SurfaceCard>

        <SurfaceCard class="report-grid__wide" title="Recent errors" sub="The latest failures — click one for its diagnosis">
          <RecentErrorsTable :errors="r.overview?.recentErrors ?? []" @select="r.selectEvent($event)" />
        </SurfaceCard>
      </div>

      <!-- Per-source accordion. -->
      <SurfaceCard title="Sources" sub="Expand a source for its timeline, operation breakdown, and recent events">
        <template #actions>
          <SelectField
            :model-value="r.sort"
            :options="sortOptions"
            aria-label="Sort sources"
            @update:model-value="r.setSort($event as ReportSort)"
          />
        </template>

        <div v-if="reportLoading" class="report-rows">
          <Skeleton v-for="n in skeletons" :key="n" variant="row" />
        </div>
        <p v-else-if="r.sources.length === 0" class="report-empty">No source activity in this window.</p>
        <div v-else class="report-rows">
          <SourceReportRow
            v-for="s in r.sources"
            :key="s.sourceKey"
            :report="s"
            :metric="r.metricsByKey[s.sourceKey] ?? null"
            :expanded="r.expandedKey === s.sourceKey"
            :timeline="r.expandedKey === s.sourceKey ? r.timeline : []"
            :timeline-pending="r.expandedKey === s.sourceKey && r.timelinePending"
            :recent-events="r.expandedKey === s.sourceKey ? r.sourceEvents : []"
            :events-pending="r.expandedKey === s.sourceKey && r.sourceEventsPending"
            :timeline-bucket="r.timelineBucket"
            :resetting="resetting === (r.metricsByKey[s.sourceKey]?.id ?? s.sourceId)"
            @toggle="r.toggleSource($event)"
            @reset="emit('reset-breaker', $event)"
            @select-event="r.selectEvent($event)"
          />
        </div>
      </SurfaceCard>

      <!-- Full event log. -->
      <SurfaceCard title="Event log" sub="Every source operation, filterable and searchable">
        <EventLogFilters
          class="report-log-filters"
          :status="r.eventStatus"
          :event-type="r.eventType"
          :page="r.eventsPage"
          :page-count="r.eventsPageCount"
          :total="r.eventsTotal"
          :pending="r.eventLogPending"
          @update:status="r.setEventStatus($event)"
          @update:event-type="r.setEventType($event)"
          @prev="r.eventsPrev()"
          @next="r.eventsNext()"
        />
        <ErrorBanner v-if="r.eventLogError" :message="r.eventLogError" />
        <EventTable
          :events="r.events"
          :pending="r.eventLogPending"
          @select="r.selectEvent($event)"
        />
      </SurfaceCard>

      <EventDetailDialog
        :open="r.eventModalOpen"
        :event="r.selectedEvent"
        @update:open="onModalOpen"
        @close="r.closeEvent()"
      />
    </template>

    <!-- ── Relocated search-latency metrics pane (slice 3 — unchanged) ──────── -->
    <SourceMetricsPane
      :metrics="metrics"
      :pending="pending"
      :error="error"
      :warming="warming"
      :warm-message="warmMessage"
      :warm-error="warmError"
      :resetting="resetting"
      :reset-error="resetError"
      @warm-now="emit('warm-now')"
      @reset-breaker="emit('reset-breaker', $event)"
      @purge-source="emit('purge-source', $event)"
    />
  </div>
</template>

<style scoped>
/* GROW screen (QCAT-265): the document scrolls, the content grows. */
.source-health {
  display: flex;
  flex-direction: column;
  gap: 16px;
  padding: var(--space-2xl) var(--space-3xl)
    calc(var(--space-3xl) + var(--app-nav-bottom));
  background: var(--bg);
}

.report-head {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 16px;
  flex-wrap: wrap;
}

.report-head__title {
  margin: 0;
  font-family: var(--font-display);
  font-size: var(--text-2xl);
  font-weight: var(--weight-bold);
  color: var(--text);
}

.report-head__sub {
  margin: 3px 0 0;
  font-size: var(--text-sm);
  color: var(--muted);
}

.report-kpis {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
  gap: 12px;
}

.report-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px;
}

.report-grid__wide {
  grid-column: 1 / -1;
}

.report-rows {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.report-log-filters {
  margin-bottom: 12px;
}

.report-empty {
  padding: 14px 2px;
  font-size: var(--text-sm);
  color: var(--muted);
}

@media (max-width: 760px) {
  .report-grid {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 900px) {
  .source-health {
    padding: var(--space-lg) var(--space-lg)
      calc(var(--space-lg) + var(--app-nav-bottom));
  }
}
</style>
