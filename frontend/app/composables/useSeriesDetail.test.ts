/**
 * useSeriesDetail — matchDiskProvider (the no-re-download Match action) +
 * loadProviderCoverage (the Sources panel's lazy per-source coverage).
 *
 * matchDiskProvider pins:
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
 * Only matchDiskProvider is under test in that section — the rest of
 * useSeriesDetail's mutations (setMonitored, removeSource, …) share the same
 * `mutate` wrapper and are exercised indirectly by every screen/dialog test
 * that drives them.
 *
 * loadProviderCoverage pins (the LAZY per-source coverage the Sources panel's
 * "Show coverage" row action drives):
 *   1. It is NEVER called by `refresh()` — `providerCoverage` stays `{}` after
 *      the initial series load, proving the fetch is opt-in only.
 *   2. A provider with `mangaId > 0` fetches
 *      GET /api/sources/{sourceId}/manga/{mangaId}/breakdown and caches the
 *      mapped `ScanlatorCoverage[]` under the provider's id.
 *   3. A second call for the SAME already-cached provider does not re-fetch
 *      (cache guard).
 *   4. A failed fetch caches `null` (never throws, `error` untouched).
 *   5. A provider with `mangaId <= 0` (unlinked disk provider) never fetches
 *      at all.
 *
 * vi.mock is hoisted by Vitest's transform so the apiClient mock is in place
 * before useSeriesDetail.ts is evaluated, regardless of import order here.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { useSeriesDetail } from './useSeriesDetail'

interface Call { method: string, path: string, body?: unknown, params?: unknown }
let calls: Call[] = []
let nextMatchOk = true
let nextBreakdownOk = true

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
      mangaId: 0,
      chapterCount: 8,
      scanlator: '',
      language: 'en',
      importance: 1,
      health: 'ok',
      chaptersBehind: 0,
      lastError: '',
    },
    {
      id: 'real-provider-2',
      provider: 'src-2',
      providerName: 'MangaDex',
      linked: true,
      mangaId: 99,
      chapterCount: 2,
      scanlator: '',
      language: 'en',
      importance: 2,
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
    GET: vi.fn().mockImplementation((path: string, opts?: { params?: { path?: Record<string, unknown> } }) => {
      calls.push({ method: 'GET', path, params: opts?.params?.path })
      if (path === '/api/series/{id}') {
        return Promise.resolve({ data: initialDetail, error: null, response: new Response() })
      }
      if (path === '/api/sources/{sourceId}/manga/{mangaId}/breakdown') {
        if (!nextBreakdownOk) {
          return Promise.resolve({ data: null, error: { message: 'breakdown failed' }, response: new Response(null, { status: 502 }) })
        }
        return Promise.resolve({
          data: { total: 2, scanlators: [{ scanlator: 'MangaDex', count: 2, ranges: '1-2' }] },
          error: null,
          response: new Response(),
        })
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

describe('useSeriesDetail — loadProviderCoverage (lazy per-source coverage)', () => {
  beforeEach(() => {
    calls = []
    nextBreakdownOk = true
  })

  it('never fetches coverage during refresh() — providerCoverage stays empty after the initial load', async () => {
    const { providerCoverage, refresh } = useSeriesDetail('series-1')
    await refresh()

    expect(providerCoverage.value).toEqual({})
    expect(calls.some((c) => c.path === '/api/sources/{sourceId}/manga/{mangaId}/breakdown')).toBe(false)
  })

  it('fetches and caches the breakdown for a linked provider (mangaId > 0), keyed by provider id', async () => {
    const { series, providerCoverage, refresh, loadProviderCoverage } = useSeriesDetail('series-1')
    await refresh()
    const provider = series.value!.providers.find((p) => p.id === 'real-provider-2')!

    await loadProviderCoverage(provider)

    const breakdownCall = calls.find((c) => c.path === '/api/sources/{sourceId}/manga/{mangaId}/breakdown')
    expect(breakdownCall).toBeDefined()
    expect(breakdownCall!.params).toEqual({ sourceId: 'src-2', mangaId: 99 })
    expect(providerCoverage.value['real-provider-2']).toEqual([{ scanlator: 'MangaDex', count: 2, ranges: '1-2' }])
  })

  it('does not re-fetch a provider whose coverage is already cached', async () => {
    const { series, providerCoverage, refresh, loadProviderCoverage } = useSeriesDetail('series-1')
    await refresh()
    const provider = series.value!.providers.find((p) => p.id === 'real-provider-2')!

    await loadProviderCoverage(provider)
    const countAfterFirst = calls.filter((c) => c.path === '/api/sources/{sourceId}/manga/{mangaId}/breakdown').length
    await loadProviderCoverage(provider)
    const countAfterSecond = calls.filter((c) => c.path === '/api/sources/{sourceId}/manga/{mangaId}/breakdown').length

    expect(countAfterFirst).toBe(1)
    expect(countAfterSecond).toBe(1)
    expect(providerCoverage.value['real-provider-2']).toEqual([{ scanlator: 'MangaDex', count: 2, ranges: '1-2' }])
  })

  it('caches null on a failed fetch — never throws, never touches error', async () => {
    nextBreakdownOk = false
    const { series, error, providerCoverage, refresh, loadProviderCoverage } = useSeriesDetail('series-1')
    await refresh()
    const provider = series.value!.providers.find((p) => p.id === 'real-provider-2')!

    await expect(loadProviderCoverage(provider)).resolves.toBeUndefined()

    expect(providerCoverage.value['real-provider-2']).toBeNull()
    expect(error.value).toBeNull()
  })

  it('never fetches for an unlinked disk provider (mangaId <= 0)', async () => {
    const { series, providerCoverage, refresh, loadProviderCoverage } = useSeriesDetail('series-1')
    await refresh()
    const provider = series.value!.providers.find((p) => p.id === 'disk-provider-1')!

    await loadProviderCoverage(provider)

    expect(calls.some((c) => c.path === '/api/sources/{sourceId}/manga/{mangaId}/breakdown')).toBe(false)
    expect(providerCoverage.value['disk-provider-1']).toBeUndefined()
  })
})
