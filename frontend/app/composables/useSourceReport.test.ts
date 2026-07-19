/**
 * useSourceReport — fetch mapping + period/sort refetch.
 *
 * Pins that the overview + per-source rollup map with optional→null normalisation,
 * that both endpoints are fetched together, and that changing the period or sort
 * refetches with the new query. Non-vacuous: drop the null normalisation and the
 * failingSince assertion fails on undefined; drop the setPeriod refetch and the
 * call-count assertion fails.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useSourceReport } from './useSourceReport'

const RAW_OVERVIEW = {
  period: '24h',
  since: '2026-07-18T12:00:00Z',
  kpis: { totalEvents: 100, successEvents: 90, failedEvents: 10, successRate: 0.9, activeSources: 3 },
  eventsByType: [{ eventType: 'search', total: 60, success: 55, failed: 5 }],
  slowestSources: [{ sourceKey: 'ComicK', sourceName: 'ComicK', ewmaLatencyMs: 4200 }],
  // failingSince present, cooldownUntil absent → must normalise cooldownUntil to null.
  failingSources: [{ sourceKey: 'ComicK', failingSince: '2026-07-19T10:00:00Z', consecutiveFailures: 5, lastError: 'boom', isCoolingDown: false }],
  // A failed event with the three optionals present; a success omits them.
  recentErrors: [{ id: 'e1', sourceKey: 'ComicK', sourceId: '1', sourceName: 'ComicK', language: 'en', eventType: 'download', status: 'failed', durationMs: 60000, errorMessage: 'timeout', errorCategory: 'timeout', metadata: {}, createdAt: '2026-07-19T11:00:00Z' }],
}

const RAW_SOURCES = [
  // cooldownUntil + failingSince absent → normalise to null.
  { sourceKey: 'MangaDex', sourceId: '2', sourceName: 'MangaDex', language: 'en', totalEvents: 40, successEvents: 40, failedEvents: 0, successRate: 1, byType: [], ewmaLatencyMs: 240, lastLatencyMs: 210, consecutiveFailures: 0, lastError: '', isCoolingDown: false },
]

let overviewCalls = 0
let sourcesCalls = 0
let lastSourcesQuery: unknown = null
let getError = false

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string, opts?: { params?: { query?: unknown } }) => {
      if (path === '/api/reporting/overview') {
        overviewCalls++
        if (getError) return Promise.resolve({ data: null, error: { message: 'boom' } })
        return Promise.resolve({ data: RAW_OVERVIEW, error: null })
      }
      if (path === '/api/reporting/sources') {
        sourcesCalls++
        lastSourcesQuery = opts?.params?.query
        if (getError) return Promise.resolve({ data: null, error: { message: 'boom' } })
        return Promise.resolve({ data: RAW_SOURCES, error: null })
      }
      return Promise.resolve({ data: null, error: null })
    }),
    POST: vi.fn(),
    PATCH: vi.fn(),
    DELETE: vi.fn(),
    PUT: vi.fn(),
    use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

describe('useSourceReport', () => {
  beforeEach(() => {
    overviewCalls = 0
    sourcesCalls = 0
    lastSourcesQuery = null
    getError = false
  })

  it('maps both endpoints with optional→null normalisation', async () => {
    const { overview, sources, pending } = useSourceReport()
    await vi.waitFor(() => expect(pending.value).toBe(false))

    expect(overviewCalls).toBe(1)
    expect(sourcesCalls).toBe(1)
    expect(overview.value?.kpis.successRate).toBe(0.9)
    // cooldownUntil absent on the failing source → null.
    expect(overview.value?.failingSources[0]!.cooldownUntil).toBeNull()
    expect(overview.value?.failingSources[0]!.failingSince).toBe('2026-07-19T10:00:00Z')
    // recentErrors optionals present pass through; itemsCount absent → null.
    expect(overview.value?.recentErrors[0]!.errorCategory).toBe('timeout')
    expect(overview.value?.recentErrors[0]!.itemsCount).toBeNull()
    // sources: absent breaker timestamps → null.
    expect(sources.value[0]!.failingSince).toBeNull()
    expect(sources.value[0]!.cooldownUntil).toBeNull()
  })

  it('refetches with the new window when the period changes', async () => {
    const { pending, setPeriod } = useSourceReport()
    await vi.waitFor(() => expect(pending.value).toBe(false))
    expect(sourcesCalls).toBe(1)

    setPeriod('7d')
    await vi.waitFor(() => expect(pending.value).toBe(false))
    expect(sourcesCalls).toBe(2)
    expect((lastSourcesQuery as { period: string }).period).toBe('7d')

    // Same period → no refetch.
    setPeriod('7d')
    expect(sourcesCalls).toBe(2)
  })

  it('refetches with the new ordering when the sort changes', async () => {
    const { pending, setSort } = useSourceReport()
    await vi.waitFor(() => expect(pending.value).toBe(false))

    setSort('latency')
    await vi.waitFor(() => expect(pending.value).toBe(false))
    expect((lastSourcesQuery as { sort: string }).sort).toBe('latency')
  })

  it('surfaces a load failure in error', async () => {
    getError = true
    const { pending, error } = useSourceReport()
    await vi.waitFor(() => expect(pending.value).toBe(false))
    expect(error.value).toBe('Failed to load the source report')
  })
})
