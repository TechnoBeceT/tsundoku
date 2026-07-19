/**
 * useSourceTimeline — load + map the bucketed series.
 *
 * Pins that load() passes the source key + bucket + period and stores the
 * returned buckets, and that a failure clears the series + surfaces an error.
 * Non-vacuous: drop the query pass-through and the query assertion fails.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useSourceTimeline } from './useSourceTimeline'

const RAW_BUCKETS = [
  { bucket: '2026-07-19T10:00:00Z', success: 8, failed: 0, total: 8 },
  { bucket: '2026-07-19T11:00:00Z', success: 2, failed: 6, total: 8 },
]

let lastPath: string | null = null
let lastQuery: Record<string, unknown> = {}
let getError = false

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((_path: string, opts?: { params?: { path?: { sourceKey?: string }, query?: Record<string, unknown> } }) => {
      lastPath = opts?.params?.path?.sourceKey ?? null
      lastQuery = opts?.params?.query ?? {}
      if (getError) return Promise.resolve({ data: null, error: { message: 'boom' } })
      return Promise.resolve({ data: { buckets: RAW_BUCKETS }, error: null })
    }),
    POST: vi.fn(),
    PATCH: vi.fn(),
    DELETE: vi.fn(),
    PUT: vi.fn(),
    use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

describe('useSourceTimeline', () => {
  beforeEach(() => {
    lastPath = null
    lastQuery = {}
    getError = false
  })

  it('loads a source timeline with the given bucket + period', async () => {
    const { buckets, pending, load } = useSourceTimeline()
    await load('ComicK', 'hour', '24h')

    expect(pending.value).toBe(false)
    expect(lastPath).toBe('ComicK')
    expect(lastQuery.bucket).toBe('hour')
    expect(lastQuery.period).toBe('24h')
    expect(buckets.value).toHaveLength(2)
    expect(buckets.value[1]!.failed).toBe(6)
  })

  it('clears the series and surfaces an error on failure', async () => {
    getError = true
    const { buckets, error, load } = useSourceTimeline()
    await load('ComicK')
    expect(error.value).toBe('Failed to load the timeline')
    expect(buckets.value).toEqual([])
  })
})
