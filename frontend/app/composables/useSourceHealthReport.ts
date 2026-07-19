/**
 * useSourceHealthReport — the orchestration layer for the Kaizoku-grade Source
 * Metrics report. It composes the three report data composables (useSourceReport
 * for the overview + per-source rollup, useSourceEvents for the global event log,
 * useSourceTimeline for a source's histogram) plus a second per-source event feed
 * for an expanded row's recent events, and adds the interactive state the report
 * screen drives: the period/sort, the accordion expansion (with LAZY per-source
 * loading), the event-log filters + pagination, and the forensic-modal selection.
 *
 * It returns ONE reactive bundle (data + methods) so the whole report threads down
 * the page → Health shell → SourceHealth screen as a single prop, keeping those
 * presentational layers thin. The lazy logic lives here (not in a page) so it is
 * unit-testable: expanding a source loads its timeline + recent events exactly
 * once per open; collapsing loads nothing; changing the period reloads the
 * expanded source's (period-scoped) timeline.
 *
 * The report is created deferred; `load()` runs the first time the Sources tab is
 * shown (LAZY tab data), mirroring useSourceMetrics.
 */
import { computed, reactive, type Ref } from 'vue'
import { useSourceReport } from '~/composables/useSourceReport'
import { useSourceEvents } from '~/composables/useSourceEvents'
import { useSourceTimeline } from '~/composables/useSourceTimeline'
import type { SourceMetric } from '~/components/screens/sourceHealth.types'
import type {
  ReportOverview,
  ReportPeriod,
  ReportSort,
  SourceEventRecord,
  SourceReport,
  TimelineBucket,
  TimelineBucketSize,
} from '~/components/health/sourceReport.types'

/** The single reactive bundle the report screen consumes. */
export interface SourceHealthReportModel {
  // ── Period dashboard ──
  overview: ReportOverview | null
  sources: SourceReport[]
  reportPending: boolean
  reportError: string | null
  period: ReportPeriod
  sort: ReportSort
  // ── Global event log ──
  events: SourceEventRecord[]
  eventsTotal: number
  eventsPage: number
  eventsPageCount: number
  eventStatus: string
  eventType: string
  eventLogPending: boolean
  eventLogError: string | null
  // ── Per-source accordion expansion ──
  expandedKey: string | null
  timeline: TimelineBucket[]
  timelinePending: boolean
  timelineBucket: TimelineBucketSize
  sourceEvents: SourceEventRecord[]
  sourceEventsPending: boolean
  // ── Forensic modal ──
  selectedEvent: SourceEventRecord | null
  eventModalOpen: boolean
  // ── Derived ──
  metricsByKey: Record<string, SourceMetric>
  // ── Methods ──
  load: () => void
  setPeriod: (p: ReportPeriod) => void
  setSort: (s: ReportSort) => void
  toggleSource: (key: string) => void
  setEventStatus: (v: string) => void
  setEventType: (v: string) => void
  eventsPrev: () => void
  eventsNext: () => void
  selectEvent: (e: SourceEventRecord) => void
  closeEvent: () => void
}

/** The sensible histogram granularity per window (hour for 24h, day beyond). */
function bucketForPeriod(period: ReportPeriod): TimelineBucketSize {
  return period === '24h' ? 'hour' : 'day'
}

export function useSourceHealthReport(opts: {
  metrics: Ref<SourceMetric[]>
  immediate?: boolean
  initialPeriod?: ReportPeriod
}): SourceHealthReportModel {
  const report = useSourceReport({ immediate: false, initialPeriod: opts.initialPeriod })
  const eventLog = useSourceEvents({ immediate: false, limit: 50 })
  const timeline = useSourceTimeline()
  const recent = useSourceEvents({ immediate: false, limit: 8 })

  // Whether the report has been loaded once (guards the lazy first load).
  let loaded = false

  // Canonical key → metrics snapshot, for the accordion's superset badges + Reset.
  // The join key is the canonical NAME (source_key = TrimSpace(name)), the same key
  // the events + breaker use.
  const metricsByKey = computed<Record<string, SourceMetric>>(() => {
    const map: Record<string, SourceMetric> = {}
    for (const m of opts.metrics.value) map[m.name.trim()] = m
    return map
  })

  // The small interactive pieces (expansion, modal selection) held in one
  // reactive object; the composable data comes from the child composables' refs.
  const state = reactive({
    expandedKey: null as string | null,
    selectedEvent: null as SourceEventRecord | null,
    eventModalOpen: false,
  })

  /** First-time report load (overview + rollup + global event log). */
  function load(): void {
    if (loaded) return
    loaded = true
    void report.refetch()
    void eventLog.refetch()
  }

  /** Switch the reporting window; reload the report + the expanded timeline. */
  function setPeriod(p: ReportPeriod): void {
    report.setPeriod(p)
    if (state.expandedKey) {
      void timeline.load(state.expandedKey, bucketForPeriod(p), p)
    }
  }

  /** Switch the per-source rollup ordering. */
  function setSort(s: ReportSort): void {
    report.setSort(s)
  }

  /**
   * Toggle a source's accordion row. Opening a DIFFERENT source loads its
   * timeline + recent events once; toggling the open source shut loads nothing.
   */
  function toggleSource(key: string): void {
    if (state.expandedKey === key) {
      state.expandedKey = null
      return
    }
    state.expandedKey = key
    void timeline.load(key, bucketForPeriod(report.period.value), report.period.value)
    recent.setSourceKey(key)
  }

  function setEventStatus(v: string): void {
    eventLog.setStatus(v as never)
  }

  function setEventType(v: string): void {
    eventLog.setEventType(v as never)
  }

  function eventsPrev(): void {
    eventLog.goToPage(eventLog.page.value - 1)
  }

  function eventsNext(): void {
    eventLog.goToPage(eventLog.page.value + 1)
  }

  function selectEvent(e: SourceEventRecord): void {
    state.selectedEvent = e
    state.eventModalOpen = true
  }

  function closeEvent(): void {
    state.eventModalOpen = false
  }

  if (opts.immediate) load()

  // The single reactive bundle. `reactive` unwraps the composable refs so the
  // consumer reads plain values (e.g. `model.overview`, not `.value`).
  return reactive({
    overview: report.overview,
    sources: report.sources,
    reportPending: report.pending,
    reportError: report.error,
    period: report.period,
    sort: report.sort,

    events: eventLog.events,
    eventsTotal: eventLog.total,
    eventsPage: eventLog.page,
    eventsPageCount: eventLog.pageCount,
    eventStatus: eventLog.status,
    eventType: eventLog.eventType,
    eventLogPending: eventLog.pending,
    eventLogError: eventLog.error,

    expandedKey: computed(() => state.expandedKey),
    timeline: timeline.buckets,
    timelinePending: timeline.pending,
    timelineBucket: computed(() => bucketForPeriod(report.period.value)),
    sourceEvents: recent.events,
    sourceEventsPending: recent.pending,

    selectedEvent: computed(() => state.selectedEvent),
    eventModalOpen: computed(() => state.eventModalOpen),

    metricsByKey,

    load,
    setPeriod,
    setSort,
    toggleSource,
    setEventStatus,
    setEventType,
    eventsPrev,
    eventsNext,
    selectEvent,
    closeEvent,
  })
}
