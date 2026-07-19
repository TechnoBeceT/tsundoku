/**
 * useSourceReport — the data layer for the Source Metrics report's period
 * dashboard: the KPI cards, the slowest + currently-failing leaderboards, the
 * per-operation breakdown, the recent-errors preview (GET /api/reporting/overview)
 * AND the per-source rollup that drives the accordion (GET /api/reporting/sources).
 *
 * Both are SQL-aggregated server-side for the requested window, so this composable
 * only fetches + maps — it never folds raw events client-side. It owns the report
 * PERIOD (24h/7d/30d) and the rollup SORT; changing either refetches both
 * endpoints in parallel. The tab creates it deferred (`immediate: false`) and
 * calls `refetch()` the first time the report is shown (LAZY tab data).
 *
 * §16: `pending` (in flight) + `error` (a load failure, surfaced inline) are both
 * exposed. An empty library simply yields an empty overview + `sources: []`.
 */
import { ref } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import { mapEventRecord, mapFailingSource } from '~/composables/reportMappers'
import type {
  ReportOverview,
  ReportPeriod,
  ReportSort,
  SourceReport,
} from '~/components/health/sourceReport.types'

type OverviewDTO = components['schemas']['ReportingOverview']
type SourceReportDTO = components['schemas']['SourceReport']

// ── DTO mappers ─────────────────────────────────────────────────────────────

function mapOverview(dto: OverviewDTO): ReportOverview {
  return {
    period: dto.period,
    since: dto.since,
    kpis: dto.kpis,
    eventsByType: dto.eventsByType,
    slowestSources: dto.slowestSources,
    failingSources: dto.failingSources.map(mapFailingSource),
    recentErrors: dto.recentErrors.map(mapEventRecord),
  }
}

/** Map the per-source rollup DTO → screen SourceReport (optionals → null). */
function mapSourceReport(dto: SourceReportDTO): SourceReport {
  return {
    sourceKey: dto.sourceKey,
    sourceId: dto.sourceId,
    sourceName: dto.sourceName,
    language: dto.language,
    totalEvents: dto.totalEvents,
    successEvents: dto.successEvents,
    failedEvents: dto.failedEvents,
    successRate: dto.successRate,
    byType: dto.byType,
    ewmaLatencyMs: dto.ewmaLatencyMs,
    lastLatencyMs: dto.lastLatencyMs,
    failingSince: dto.failingSince ?? null,
    consecutiveFailures: dto.consecutiveFailures,
    lastError: dto.lastError,
    cooldownUntil: dto.cooldownUntil ?? null,
    isCoolingDown: dto.isCoolingDown,
  }
}

// ── Composable ──────────────────────────────────────────────────────────────

export function useSourceReport(options: {
  immediate?: boolean
  initialPeriod?: ReportPeriod
  initialSort?: ReportSort
} = {}) {
  const { immediate = true, initialPeriod = '24h', initialSort = 'failures' } = options

  const overview = ref<ReportOverview | null>(null)
  const sources = ref<SourceReport[]>([])
  const pending = ref(false)
  const error = ref<string | null>(null)

  const period = ref<ReportPeriod>(initialPeriod)
  const sort = ref<ReportSort>(initialSort)

  /** Load (or reload) both report endpoints for the current period + sort. */
  async function refetch(): Promise<void> {
    pending.value = true
    error.value = null
    try {
      const [overviewRes, sourcesRes] = await Promise.all([
        apiClient.GET('/api/reporting/overview', { params: { query: { period: period.value } } }),
        apiClient.GET('/api/reporting/sources', { params: { query: { period: period.value, sort: sort.value } } }),
      ])
      if (overviewRes.error || !overviewRes.data) throw new Error('Failed to load the source report')
      if (sourcesRes.error || !sourcesRes.data) throw new Error('Failed to load the source report')
      overview.value = mapOverview(overviewRes.data)
      sources.value = sourcesRes.data.map(mapSourceReport)
    }
    catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to load the source report'
    }
    finally {
      pending.value = false
    }
  }

  /** Switch the reporting window and reload (no-op if unchanged). */
  function setPeriod(next: ReportPeriod): void {
    if (next === period.value) return
    period.value = next
    void refetch()
  }

  /** Switch the per-source rollup ordering and reload (no-op if unchanged). */
  function setSort(next: ReportSort): void {
    if (next === sort.value) return
    sort.value = next
    void refetch()
  }

  if (immediate) void refetch()

  return { overview, sources, pending, error, period, sort, refetch, setPeriod, setSort }
}
