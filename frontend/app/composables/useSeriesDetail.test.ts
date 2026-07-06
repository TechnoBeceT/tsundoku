/**
 * useSeriesDetail — matchDiskProvider (the no-re-download Match action).
 *
 * Pins:
 *   1. matchDiskProvider(providerId, payload) POSTs
 *      /api/series/{id}/providers/{providerId}/match with the exact
 *      {source, mangaId, importance, scanlator} body.
 *   2. On success it reseeds `series` DIRECTLY from the response — NOT via a
 *      second GET /api/series/{id} round-trip (mutate-reseeds-from-response,
 *      §16) — and resolves true.
 *   3. On failure it sets `error` (never swallowed) and resolves false,
 *      leaving the previously-loaded `series` untouched.
 *   4. `matchBusy` flips true for the duration of the call and back to false
 *      once it resolves, win or lose.
 *
 * Only matchDiskProvider is under test here — the rest of useSeriesDetail's
 * mutations (setMonitored, removeSource, …) share the same `mutate` wrapper
 * and are exercised indirectly by every screen/dialog test that drives them.
 *
 * vi.mock is hoisted by Vitest's transform so the apiClient mock is in place
 * before useSeriesDetail.ts is evaluated, regardless of import order here.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { useSeriesDetail } from './useSeriesDetail'

interface Call { method: string, path: string, body?: unknown, params?: unknown }
let calls: Call[] = []
let nextMatchOk = true

const initialDetail = {
  id: 'series-1',
  title: 'Solo Leveling',
  displayName: 'Solo Leveling',
  slug: 'solo-leveling',
  category: 'Manhwa',
  coverUrl: '',
  monitored: true,
  completed: false,
  chapterCounts: { total: 10, downloaded: 8, wanted: 2, failed: 0 },
  chapters: [],
  providers: [
    {
      id: 'disk-provider-1',
      provider: 'disk:kaizoku',
      providerName: 'Unknown (disk)',
      linked: false,
      chapterCount: 8,
      scanlator: '',
      language: 'en',
      importance: 1,
      health: 'ok',
      chaptersBehind: 0,
      lastError: '',
    },
  ],
}

// The refreshed detail the match endpoint returns: the disk provider is gone,
// replaced by the newly-linked real source — proves a direct reseed (not a
// stale copy of `initialDetail`).
const matchedDetail = {
  ...initialDetail,
  providers: [
    {
      id: 'real-provider-1',
      provider: 'src-1',
      providerName: 'MangaDex',
      linked: true,
      chapterCount: 8,
      scanlator: '',
      language: 'en',
      importance: 1,
      health: 'ok',
      chaptersBehind: 0,
      lastError: '',
    },
  ],
}

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string) => {
      calls.push({ method: 'GET', path })
      if (path === '/api/series/{id}') {
        return Promise.resolve({ data: initialDetail, error: null, response: new Response() })
      }
      // /api/categories
      return Promise.resolve({ data: [], error: null, response: new Response() })
    }),
    POST: vi.fn().mockImplementation((path: string, opts?: { params?: { path?: Record<string, unknown> }, body?: unknown }) => {
      calls.push({ method: 'POST', path, params: opts?.params?.path, body: opts?.body })
      if (path === '/api/series/{id}/providers/{providerId}/match') {
        if (!nextMatchOk) {
          return Promise.resolve({ data: null, error: { message: 'match failed' }, response: new Response(null, { status: 400 }) })
        }
        return Promise.resolve({ data: matchedDetail, error: null, response: new Response(null, { status: 200 }) })
      }
      return Promise.resolve({ data: null, error: null, response: new Response(null, { status: 200 }) })
    }),
    PATCH: vi.fn(),
    DELETE: vi.fn(),
    use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

describe('useSeriesDetail — matchDiskProvider', () => {
  beforeEach(() => {
    calls = []
    nextMatchOk = true
  })

  it('POSTs the match endpoint with the path params and exact body', async () => {
    const { refresh, matchDiskProvider } = useSeriesDetail('series-1')
    await refresh()

    await matchDiskProvider('disk-provider-1', { source: 'src-1', mangaId: 42, importance: 1, scanlator: '' })

    const postCall = calls.find(c => c.path === '/api/series/{id}/providers/{providerId}/match')
    expect(postCall).toBeDefined()
    expect(postCall!.params).toEqual({ id: 'series-1', providerId: 'disk-provider-1' })
    expect(postCall!.body).toEqual({ source: 'src-1', mangaId: 42, importance: 1, scanlator: '' })
  })

  it('reseeds series directly from the response on success, without a second GET', async () => {
    const { series, refresh, matchDiskProvider } = useSeriesDetail('series-1')
    await refresh()
    expect(series.value?.providers[0]?.linked).toBe(false)

    const getCountBefore = calls.filter(c => c.method === 'GET' && c.path === '/api/series/{id}').length
    const ok = await matchDiskProvider('disk-provider-1', { source: 'src-1', mangaId: 42, importance: 1 })

    expect(ok).toBe(true)
    expect(series.value?.providers).toHaveLength(1)
    expect(series.value?.providers[0]?.id).toBe('real-provider-1')
    expect(series.value?.providers[0]?.linked).toBe(true)
    // No extra GET /api/series/{id} fired — the reseed came from the POST response.
    const getCountAfter = calls.filter(c => c.method === 'GET' && c.path === '/api/series/{id}').length
    expect(getCountAfter).toBe(getCountBefore)
  })

  it('failure sets error, resolves false, and never swallows or touches the existing series', async () => {
    nextMatchOk = false
    const { series, error, refresh, matchDiskProvider } = useSeriesDetail('series-1')
    await refresh()

    const ok = await matchDiskProvider('disk-provider-1', { source: 'src-1', mangaId: 42, importance: 1 })

    expect(ok).toBe(false)
    expect(error.value).toBe('match failed')
    expect(series.value?.providers[0]?.id).toBe('disk-provider-1')
    expect(series.value?.providers[0]?.linked).toBe(false)
  })

  it('matchBusy flips true during the call and back to false once it resolves', async () => {
    const { matchBusy, refresh, matchDiskProvider } = useSeriesDetail('series-1')
    await refresh()
    expect(matchBusy.value).toBe(false)

    const promise = matchDiskProvider('disk-provider-1', { source: 'src-1', mangaId: 42, importance: 1 })
    expect(matchBusy.value).toBe(true)
    await promise

    expect(matchBusy.value).toBe(false)
  })
})
