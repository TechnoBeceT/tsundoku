/**
 * useSourceSeries — the per-source dependent-series data layer.
 *
 * Pins:
 *   1. load(sourceId) GETs /api/sources/{sourceId}/series with the id as a path
 *      param and maps the rows onto SourceSeriesRow[];
 *   2. it drives pending true→false and sets activeSourceId;
 *   3. summary derives { total, dark } from the rows;
 *   4. §16: a failed load surfaces in error and leaves rows empty (never stale).
 *
 * Non-vacuous: drop the path param and test 1's endpoint assertion fails; swallow
 * the error and test 4 fails.
 *
 * vi.mock is hoisted; the factory closes over the mutable bindings below.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useSourceSeries } from './useSourceSeries'

let getCalls: { path: string, sourceId?: string }[] = []
let getShouldError = false

const rows = [
  { seriesId: 's-1', title: 'Solo Leveling', alternativeCount: 2, goesDark: false, topAlternative: 'Flame Comics' },
  { seriesId: 's-2', title: 'Only Here', alternativeCount: 0, goesDark: true, topAlternative: '' },
]

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string, opts?: { params?: { path?: { sourceId?: string } } }) => {
      getCalls.push({ path, sourceId: opts?.params?.path?.sourceId })
      if (getShouldError) return Promise.resolve({ data: null, error: { message: 'boom' } })
      return Promise.resolve({ data: rows, error: null })
    }),
    PATCH: vi.fn(),
    POST: vi.fn(),
    use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

beforeEach(() => {
  getCalls = []
  getShouldError = false
})

describe('useSourceSeries', () => {
  it('does not fetch until load is called (lazy — no source id yet)', () => {
    useSourceSeries()
    expect(getCalls).toHaveLength(0)
  })

  it('load GETs the source series by id and maps the rows', async () => {
    const { rows: got, load, activeSourceId } = useSourceSeries()
    await load('42')
    expect(getCalls).toEqual([{ path: '/api/sources/{sourceId}/series', sourceId: '42' }])
    expect(activeSourceId.value).toBe('42')
    expect(got.value).toEqual(rows)
  })

  it('derives the { total, dark } summary from the rows', async () => {
    const { load, summary } = useSourceSeries()
    await load('42')
    expect(summary.value).toEqual({ total: 2, dark: 1 })
  })

  it('drives pending false→true→false across a load', async () => {
    const { load, pending } = useSourceSeries()
    const p = load('42')
    expect(pending.value).toBe(true)
    await p
    expect(pending.value).toBe(false)
  })

  it('surfaces a failed load in error and leaves rows empty (§16)', async () => {
    getShouldError = true
    const { rows: got, load, error } = useSourceSeries()
    await load('42')
    expect(error.value).toBe('boom')
    expect(got.value).toEqual([])
  })

  it('reset clears rows, error and the active source', async () => {
    const { rows: got, load, reset, activeSourceId } = useSourceSeries()
    await load('42')
    reset()
    expect(got.value).toEqual([])
    expect(activeSourceId.value).toBeNull()
  })
})
