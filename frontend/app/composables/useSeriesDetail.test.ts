/**
 * useSeriesDetail — matchDiskProvider (the no-re-download Match action) + the
 * Sources panel's provider feed (coverage straight from the series response).
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
 * Provider-feed pins (the Sources panel's coverage, which USED to require a
 * "Show coverage" click that fired a live source fetch):
 *   1. `feedCount`/`feedRanges` — what a source OFFERS — are mapped straight off
 *      the series-detail response, alongside `chapterCount` (what it supplies).
 *   2. Loading the series makes NO call to
 *      GET /api/sources/{sourceId}/manga/{mangaId}/breakdown — the coverage the
 *      panel shows comes from our own DB, never a ping to the source. This is the
 *      regression guard for the removed lazy fetch.
 *
 * vi.mock is hoisted by Vitest's transform so the apiClient mock is in place
 * before useSeriesDetail.ts is evaluated, regardless of import order here.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { useSeriesDetail } from './useSeriesDetail'

interface Call { method: string, path: string, body?: unknown, params?: unknown }
let calls: Call[] = []
let nextMatchOk = true
let nextDedupOk = true
let nextDedupeFilesOk = true
let nextDeleteOk = true
let nextPatchOk = true

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
      feedCount: 0,
      feedRanges: '',
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
      feedCount: 270,
      feedRanges: '1-88, 90-269',
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
      if (path === '/api/series/{id}/providers/dedup') {
        if (!nextDedupOk) {
          return Promise.resolve({ data: null, error: { message: 'dedup failed' }, response: new Response(null, { status: 500 }) })
        }
        return Promise.resolve({
          data: { merged: 1, skipped: 0, series: { ...matchedDetail } },
          error: null,
          response: new Response(null, { status: 200 }),
        })
      }
      if (path === '/api/series/{id}/dedupe-files') {
        if (!nextDedupeFilesOk) {
          return Promise.resolve({ data: null, error: { message: 'dedupe-files failed' }, response: new Response(null, { status: 500 }) })
        }
        return Promise.resolve({ data: { removed: 3 }, error: null, response: new Response(null, { status: 200 }) })
      }
      return Promise.resolve({ data: null, error: null, response: new Response(null, { status: 200 }) })
    }),
    PATCH: vi.fn().mockImplementation((path: string, opts?: { params?: { path?: Record<string, unknown> }, body?: unknown }) => {
      calls.push({ method: 'PATCH', path, params: opts?.params?.path, body: opts?.body })
      if (!nextPatchOk) {
        return Promise.resolve({ data: null, error: { message: 'patch failed' }, response: new Response(null, { status: 500 }) })
      }
      return Promise.resolve({ data: null, error: null, response: new Response(null, { status: 200 }) })
    }),
    DELETE: vi.fn().mockImplementation((path: string, opts?: { params?: { path?: Record<string, unknown> } }) => {
      calls.push({ method: 'DELETE', path, params: opts?.params?.path })
      if (!nextDeleteOk) {
        return Promise.resolve({ data: null, error: { message: 'delete failed' }, response: new Response(null, { status: 500 }) })
      }
      return Promise.resolve({ data: null, error: null, response: new Response(null, { status: 204 }) })
    }),
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

/**
 * The shared `mutate` wrapper must REPORT its outcome — that is what lets a
 * caller close its confirm dialog only on success (the remove-source dialog bug:
 * it emitted into the void and stayed open forever). Pinned through removeSource
 * (which uses `mutate` unchanged) plus one other mutation, proving the contract
 * is the wrapper's, not one action's.
 */
describe('useSeriesDetail — mutate reports success/failure', () => {
  beforeEach(() => {
    calls = []
    nextDeleteOk = true
    nextPatchOk = true
  })

  it('removeSource DELETEs the provider and resolves true on success', async () => {
    const { refresh, removeSource } = useSeriesDetail('series-1')
    await refresh()

    const ok = await removeSource('real-provider-2')

    expect(ok).toBe(true)
    const call = calls.find(c => c.path === '/api/series/{id}/providers/{providerId}')
    expect(call?.params).toEqual({ id: 'series-1', providerId: 'real-provider-2' })
  })

  it('removeSource resolves false and surfaces the error on failure', async () => {
    nextDeleteOk = false
    const { error, refresh, removeSource } = useSeriesDetail('series-1')
    await refresh()

    const ok = await removeSource('real-provider-2')

    expect(ok).toBe(false)
    expect(error.value).toBe('Update failed')
  })

  it('removeBusy flips true during the call and back to false once it resolves', async () => {
    const { removeBusy, refresh, removeSource } = useSeriesDetail('series-1')
    await refresh()
    expect(removeBusy.value).toBe(false)

    const promise = removeSource('real-provider-2')
    expect(removeBusy.value).toBe(true)
    await promise

    expect(removeBusy.value).toBe(false)
  })

  it('carries the same true/false contract on the other mutations (setMonitored)', async () => {
    const { refresh, setMonitored } = useSeriesDetail('series-1')
    await refresh()
    expect(await setMonitored(false)).toBe(true)

    nextPatchOk = false
    expect(await setMonitored(true)).toBe(false)
  })

  it('setCategory resolves false without calling the API when the name is unknown', async () => {
    const { error, refresh, setCategory } = useSeriesDetail('series-1')
    await refresh()

    const ok = await setCategory('Nope')

    expect(ok).toBe(false)
    expect(error.value).toBe('Unknown category: Nope')
    expect(calls.some(c => c.path === '/api/series/{id}/category')).toBe(false)
  })
})

describe('useSeriesDetail — provider feed (coverage without a source ping)', () => {
  beforeEach(() => {
    calls = []
  })

  it('maps feedCount/feedRanges (offered) alongside chapterCount (supplied)', async () => {
    const { series, refresh } = useSeriesDetail('series-1')
    await refresh()

    const provider = series.value!.providers.find((p) => p.id === 'real-provider-2')!
    expect(provider.feedCount).toBe(270)
    expect(provider.feedRanges).toBe('1-88, 90-269')
    expect(provider.chapterCount).toBe(2)
  })

  it('maps an empty feed to 0 / "" (an unlinked disk provider offers nothing)', async () => {
    const { series, refresh } = useSeriesDetail('series-1')
    await refresh()

    const provider = series.value!.providers.find((p) => p.id === 'disk-provider-1')!
    expect(provider.feedCount).toBe(0)
    expect(provider.feedRanges).toBe('')
  })

  it('never calls the live per-source breakdown endpoint — coverage comes from the series response', async () => {
    const { refresh } = useSeriesDetail('series-1')
    await refresh()

    expect(calls.some((c) => c.path === '/api/sources/{sourceId}/manga/{mangaId}/breakdown')).toBe(false)
  })
})

describe('useSeriesDetail — dedupProviders / dedupeFiles', () => {
  beforeEach(() => {
    calls = []
    nextDedupOk = true
    nextDedupeFilesOk = true
  })

  it('dedupProviders reseeds series from the response and sets the message', async () => {
    const { series, refresh, dedupProviders, dedupMessage } = useSeriesDetail('series-1')
    await refresh()
    const getBefore = calls.filter(c => c.method === 'GET' && c.path === '/api/series/{id}').length

    await dedupProviders()

    // Reseeded from the POST response (matchedDetail has one linked provider).
    expect(series.value?.providers).toHaveLength(1)
    expect(series.value?.providers[0]?.linked).toBe(true)
    expect(dedupMessage.value).toContain('1')
    // No extra GET — reseed came from the response.
    const getAfter = calls.filter(c => c.method === 'GET' && c.path === '/api/series/{id}').length
    expect(getAfter).toBe(getBefore)
  })

  it('dedupProviders failure sets error and leaves series untouched', async () => {
    nextDedupOk = false
    const { series, error, refresh, dedupProviders } = useSeriesDetail('series-1')
    await refresh()
    await dedupProviders()
    expect(error.value).toBeTruthy()
    expect(series.value?.providers).toHaveLength(2)
  })

  it('dedupeFiles sets the removed message and does not reseed', async () => {
    const { series, refresh, dedupeFiles, dedupMessage } = useSeriesDetail('series-1')
    await refresh()
    await dedupeFiles()
    expect(dedupMessage.value).toContain('3')
    // dedupe-files makes no DB change → providers unchanged (still 2).
    expect(series.value?.providers).toHaveLength(2)
  })

  it('dedupeFiles failure sets error', async () => {
    nextDedupeFilesOk = false
    const { error, refresh, dedupeFiles } = useSeriesDetail('series-1')
    await refresh()
    await dedupeFiles()
    expect(error.value).toBeTruthy()
  })
})
