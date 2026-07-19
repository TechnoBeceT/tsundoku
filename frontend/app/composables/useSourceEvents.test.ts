/**
 * useSourceEvents — the event-log feed: __all__ sentinel, filters, pagination.
 *
 * Pins the default global source key, the optional→null event mapping, that a
 * filter change resets to page 0, that paging updates the offset, and the empty
 * path. Non-vacuous: drop the offset reset on filter change and that assertion
 * fails; drop the mapping and errorMessage stays undefined.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useSourceEvents, ALL_SOURCES_KEY } from './useSourceEvents'

const RAW_ITEMS = [
  // A failure with optionals present.
  { id: 'e1', sourceKey: 'ComicK', sourceId: '1', sourceName: 'ComicK', language: 'en', eventType: 'download', status: 'failed', durationMs: 60000, errorMessage: 'timeout', errorCategory: 'timeout', itemsCount: null, metadata: { series: 'X' }, createdAt: '2026-07-19T11:00:00Z' },
  // A success omitting the three optionals → normalise to null.
  { id: 'e2', sourceKey: 'MangaDex', sourceId: '2', sourceName: 'MangaDex', language: 'en', eventType: 'search', status: 'success', durationMs: 240, metadata: {}, createdAt: '2026-07-19T11:01:00Z' },
]

let lastPath: string | null = null
let lastQuery: Record<string, unknown> = {}
let empty = false

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string, opts?: { params?: { path?: { sourceKey?: string }, query?: Record<string, unknown> } }) => {
      lastPath = opts?.params?.path?.sourceKey ?? null
      lastQuery = opts?.params?.query ?? {}
      void path
      if (empty) return Promise.resolve({ data: { total: 0, items: [] }, error: null })
      return Promise.resolve({ data: { total: 120, items: RAW_ITEMS }, error: null })
    }),
    POST: vi.fn(),
    PATCH: vi.fn(),
    DELETE: vi.fn(),
    PUT: vi.fn(),
    use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

describe('useSourceEvents', () => {
  beforeEach(() => {
    lastPath = null
    lastQuery = {}
    empty = false
  })

  it('defaults to the global __all__ feed and maps events (optional→null)', async () => {
    const { events, total, pending, sourceKey } = useSourceEvents()
    await vi.waitFor(() => expect(pending.value).toBe(false))

    expect(sourceKey.value).toBe(ALL_SOURCES_KEY)
    expect(lastPath).toBe(ALL_SOURCES_KEY)
    expect(total.value).toBe(120)
    expect(events.value[0]!.errorCategory).toBe('timeout')
    // The success event's absent optionals normalise to null.
    expect(events.value[1]!.errorMessage).toBeNull()
    expect(events.value[1]!.errorCategory).toBeNull()
    expect(events.value[1]!.itemsCount).toBeNull()
  })

  it('points at a specific source key + resets to page 0', async () => {
    const { pending, setSourceKey, offset } = useSourceEvents()
    await vi.waitFor(() => expect(pending.value).toBe(false))

    setSourceKey('ComicK')
    await vi.waitFor(() => expect(pending.value).toBe(false))
    expect(lastPath).toBe('ComicK')
    expect(offset.value).toBe(0)
  })

  it('sends filters and resets the offset when a filter changes', async () => {
    const { pending, setStatus, setEventType, offset, goToPage } = useSourceEvents()
    await vi.waitFor(() => expect(pending.value).toBe(false))

    // Page forward first so we can prove the filter resets it.
    goToPage(1)
    await vi.waitFor(() => expect(pending.value).toBe(false))
    expect(offset.value).toBe(50)

    setStatus('failed')
    await vi.waitFor(() => expect(pending.value).toBe(false))
    expect(offset.value).toBe(0)
    expect(lastQuery.status).toBe('failed')

    setEventType('download')
    await vi.waitFor(() => expect(pending.value).toBe(false))
    expect(lastQuery.eventType).toBe('download')
  })

  it('omits empty filters from the query', async () => {
    const { pending } = useSourceEvents()
    await vi.waitFor(() => expect(pending.value).toBe(false))
    expect(lastQuery.status).toBeUndefined()
    expect(lastQuery.eventType).toBeUndefined()
  })

  it('computes the page count and clamps paging to range', async () => {
    const { pending, pageCount, page, goToPage } = useSourceEvents()
    await vi.waitFor(() => expect(pending.value).toBe(false))
    expect(pageCount.value).toBe(3) // ceil(120/50)

    goToPage(99) // clamp to the last page
    await vi.waitFor(() => expect(pending.value).toBe(false))
    expect(page.value).toBe(2)
  })

  it('handles an empty feed', async () => {
    empty = true
    const { events, total, pending, pageCount } = useSourceEvents()
    await vi.waitFor(() => expect(pending.value).toBe(false))
    expect(events.value).toEqual([])
    expect(total.value).toBe(0)
    expect(pageCount.value).toBe(1)
  })
})
