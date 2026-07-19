/**
 * useSourceHealthReport — the report orchestration facade.
 *
 * Pins the lazy first load (once), the metrics→report join by canonical key, and
 * the accordion's LAZY per-source fetch: opening a source loads its timeline +
 * recent events exactly once; collapsing loads nothing; reopening a DIFFERENT
 * source loads again; and a period change reloads the expanded source's timeline.
 *
 * Non-vacuous: drop the `loaded` guard and the second load() doubles the counts;
 * drop the collapse short-circuit and toggling shut refetches.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ref } from 'vue'
import { useSourceHealthReport } from './useSourceHealthReport'
import type { SourceMetric } from '~/components/screens/sourceHealth.types'

// ── Per-endpoint call counters ────────────────────────────────────────────────
let overviewCalls = 0
let sourcesCalls = 0
let globalEventCalls = 0
let sourceEventCalls = 0
let timelineCalls = 0
let lastTimelineQuery: Record<string, unknown> = {}

const emptyOverview = {
  period: '24h', since: '', kpis: { totalEvents: 0, successEvents: 0, failedEvents: 0, successRate: 0, activeSources: 0 },
  eventsByType: [], slowestSources: [], failingSources: [], recentErrors: [],
}

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string, opts?: { params?: { path?: { sourceKey?: string }, query?: Record<string, unknown> } }) => {
      if (path === '/api/reporting/overview') {
        overviewCalls++
        return Promise.resolve({ data: emptyOverview, error: null })
      }
      if (path === '/api/reporting/sources') {
        sourcesCalls++
        return Promise.resolve({ data: [], error: null })
      }
      if (path === '/api/reporting/source/{sourceKey}/events') {
        if (opts?.params?.path?.sourceKey === '__all__') globalEventCalls++
        else sourceEventCalls++
        return Promise.resolve({ data: { total: 0, items: [] }, error: null })
      }
      if (path === '/api/reporting/source/{sourceKey}/timeline') {
        timelineCalls++
        lastTimelineQuery = opts?.params?.query ?? {}
        return Promise.resolve({ data: { buckets: [] }, error: null })
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

function metric(name: string): SourceMetric {
  return {
    id: `id-${name}`, name, avgLatencyMs: 100, lastLatencyMs: 100, searchCount: 1, successCount: 1,
    failCount: 0, lastError: '', lastErrorAt: null, lastSuccessAt: null, lastWarmedAt: null,
    updatedAt: '', isSlow: false, breaker: null,
  }
}

describe('useSourceHealthReport', () => {
  beforeEach(() => {
    overviewCalls = 0
    sourcesCalls = 0
    globalEventCalls = 0
    sourceEventCalls = 0
    timelineCalls = 0
    lastTimelineQuery = {}
  })

  it('joins the metrics snapshot by canonical (trimmed) source key', () => {
    const metrics = ref<SourceMetric[]>([metric('ComicK '), metric('MangaDex')])
    const model = useSourceHealthReport({ metrics })
    expect(model.metricsByKey.ComicK).toBeTruthy()
    expect(model.metricsByKey.MangaDex).toBeTruthy()
  })

  it('lazy-loads the report + event log exactly once', async () => {
    const metrics = ref<SourceMetric[]>([])
    const model = useSourceHealthReport({ metrics })

    model.load()
    await vi.waitFor(() => expect(model.reportPending).toBe(false))
    expect(overviewCalls).toBe(1)
    expect(sourcesCalls).toBe(1)
    expect(globalEventCalls).toBe(1)

    // A second load() is a no-op (guarded).
    model.load()
    expect(overviewCalls).toBe(1)
    expect(globalEventCalls).toBe(1)
  })

  it('expanding a source lazy-loads its timeline + recent events once; collapsing loads nothing', async () => {
    const metrics = ref<SourceMetric[]>([])
    const model = useSourceHealthReport({ metrics, immediate: true })
    await vi.waitFor(() => expect(model.reportPending).toBe(false))

    model.toggleSource('ComicK')
    await vi.waitFor(() => expect(model.timelinePending).toBe(false))
    expect(model.expandedKey).toBe('ComicK')
    expect(timelineCalls).toBe(1)
    expect(sourceEventCalls).toBe(1)

    // Collapse — no new fetch.
    model.toggleSource('ComicK')
    expect(model.expandedKey).toBeNull()
    expect(timelineCalls).toBe(1)
    expect(sourceEventCalls).toBe(1)

    // Open a different source — fetches again.
    model.toggleSource('MangaDex')
    await vi.waitFor(() => expect(model.timelinePending).toBe(false))
    expect(timelineCalls).toBe(2)
    expect(sourceEventCalls).toBe(2)
  })

  it('reloads the expanded source timeline when the period changes', async () => {
    const metrics = ref<SourceMetric[]>([])
    const model = useSourceHealthReport({ metrics, immediate: true })
    await vi.waitFor(() => expect(model.reportPending).toBe(false))

    model.toggleSource('ComicK')
    await vi.waitFor(() => expect(model.timelinePending).toBe(false))
    expect(timelineCalls).toBe(1)

    model.setPeriod('7d')
    await vi.waitFor(() => expect(model.reportPending).toBe(false))
    // The report refetched at 7d AND the expanded timeline reloaded at day granularity.
    expect(timelineCalls).toBe(2)
    expect(lastTimelineQuery.bucket).toBe('day')
    expect(lastTimelineQuery.period).toBe('7d')
  })

  it('selecting + closing an event drives the modal state', () => {
    const metrics = ref<SourceMetric[]>([])
    const model = useSourceHealthReport({ metrics })
    const evt = { id: 'e1' } as never
    model.selectEvent(evt)
    expect(model.eventModalOpen).toBe(true)
    // The bundle is reactive, so the stored event is a reactive proxy — compare by value.
    expect(model.selectedEvent).toEqual(evt)
    model.closeEvent()
    expect(model.eventModalOpen).toBe(false)
  })
})
