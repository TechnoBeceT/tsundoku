/**
 * useSeriesDetail — matchDiskProvider (the no-re-download Match action) + the
 * Sources panel's provider feed (coverage straight from the series response).
 *
 * matchDiskProvider pins (ASYNC contract, GAP-096 — the merge runs in the
 * background and returns 202):
 *   1. matchDiskProvider(providerId, payload) POSTs
 *      /api/series/{id}/providers/{providerId}/match with the exact
 *      {source, mangaId, importance, scanlator} body.
 *   2. On 202 (launched) it does NOT reseed inline — it keeps `matchBusy` true
 *      + shows "Matching in progress…" and resolves true (so the dialog closes);
 *      the completion arrives via the `provider.merged` SSE event.
 *   3. On that SSE completion for this series it clears `matchBusy` and either
 *      refetches (success) or surfaces `error` (failure) — never swallowed.
 *   4. A hard failure to even START (not 202/409) sets `error`, resolves false,
 *      clears `matchBusy`, and leaves the previously-loaded `series` untouched.
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
import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { nextTick, ref } from 'vue'
import { useSeriesDetail } from './useSeriesDetail'

interface Call { method: string, path: string, body?: unknown, params?: unknown }
let calls: Call[] = []
let nextMatchOk = true
let nextConsolidateOk = true
let nextDedupOk = true
let nextDedupeFilesOk = true
let nextDeleteOk = true
let nextPatchOk = true
let nextFractionalPreviewOk = true
let nextFractionalPreviewMalformed = false
let nextFractionalRemoveOk = true
let nextReadingProgressOk = true
let nextDedupePreviewOk = true
let nextDedupePreviewMalformed = false

/** The owner's live removable set (see FractionalCleanupDialog.test.ts). */
const fractionalPreview = {
  typicalPageCount: 96,
  chapters: [
    { chapterId: 'c-1815', number: 181.5, pageCount: 1, provider: 'KaliScan', filename: 'a.cbz' },
    { chapterId: 'c-2215', number: 221.5, pageCount: 132, provider: 'KaliScan', filename: 'e.cbz' },
  ],
}

/** A dedupe-files dry-run touching two of the three removal sources. */
const dedupePreview = {
  total: 2,
  items: [
    { reason: 'ignored-fractional', number: 181.5, filename: '181.5.cbz' },
    { reason: 'orphan-superseded', number: 7, filename: '[old] 007.cbz' },
  ],
}

const initialDetail = {
  id: 'series-1',
  title: 'Solo Leveling',
  displayName: 'Solo Leveling',
  slug: 'solo-leveling',
  category: 'Manhwa',
  coverUrl: '',
  monitored: true,
  completed: false,
  // Native-metadata-engine rich fields (Slice D) — required on the real DTO;
  // an unidentified series' zero-values (empty string/array, year 0, null refs).
  status: '',
  genres: [],
  tags: [],
  altTitles: [],
  authors: [],
  year: 0,
  links: [],
  metadataSource: null,
  coverSource: null,
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
      fractionalCount: 2,
      fractionalChapters: ['1.1', '2.1'],
      ignoreFractional: false,
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

// The refreshed detail /reading-progress returns: chapterCounts.unread is
// distinct from initialDetail's, so a direct-reseed assertion can't be
// satisfied by a stale copy of initialDetail sneaking through.
const readingProgressDetail = {
  ...initialDetail,
  chapterCounts: { total: 10, downloaded: 8, wanted: 2, failed: 0, unread: 3 },
}

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string, opts?: { params?: { path?: Record<string, unknown> } }) => {
      calls.push({ method: 'GET', path, params: opts?.params?.path })
      if (path === '/api/series/{id}') {
        // A mutable seam so a post-merge refetch (fired by the provider.merged
        // SSE event) can return the consolidated detail, proving the reseed.
        return Promise.resolve({ data: seriesDetailResponse, error: null, response: new Response() })
      }
      if (path === '/api/series/{id}/fractional-cleanup') {
        if (!nextFractionalPreviewOk) {
          return Promise.resolve({ data: null, error: { message: 'preview failed' }, response: new Response(null, { status: 500 }) })
        }
        if (nextFractionalPreviewMalformed) {
          // A 200 whose body carries no `chapters` array (a partial/garbled payload).
          return Promise.resolve({ data: { typicalPageCount: 96 }, error: null, response: new Response() })
        }
        return Promise.resolve({ data: fractionalPreview, error: null, response: new Response() })
      }
      if (path === '/api/series/{id}/dedupe-files') {
        if (!nextDedupePreviewOk) {
          return Promise.resolve({ data: null, error: { message: 'preview failed' }, response: new Response(null, { status: 500 }) })
        }
        if (nextDedupePreviewMalformed) {
          // A 200 whose body carries no `items` array (a partial/garbled payload).
          return Promise.resolve({ data: { total: 0 }, error: null, response: new Response() })
        }
        return Promise.resolve({ data: dedupePreview, error: null, response: new Response() })
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
        // ASYNC: the merge is launched and runs detached; completion arrives via
        // the provider.merged SSE event (see fireProgress in the tests).
        return Promise.resolve({ data: { started: true }, error: null, response: new Response(null, { status: 202 }) })
      }
      if (path === '/api/series/{id}/providers/consolidate') {
        if (!nextConsolidateOk) {
          return Promise.resolve({ data: null, error: { message: 'consolidate failed' }, response: new Response(null, { status: 400 }) })
        }
        // ASYNC: launched + detached; completion arrives via the provider.merged SSE event.
        return Promise.resolve({ data: { started: true }, error: null, response: new Response(null, { status: 202 }) })
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
      if (path === '/api/series/{id}/fractional-cleanup') {
        if (!nextFractionalRemoveOk) {
          return Promise.resolve({ data: null, error: { message: 'not removable' }, response: new Response(null, { status: 400 }) })
        }
        return Promise.resolve({ data: { removed: 1 }, error: null, response: new Response(null, { status: 200 }) })
      }
      if (path === '/api/series/{id}/dedupe-files') {
        if (!nextDedupeFilesOk) {
          return Promise.resolve({ data: null, error: { message: 'dedupe-files failed' }, response: new Response(null, { status: 500 }) })
        }
        return Promise.resolve({ data: { removed: 3 }, error: null, response: new Response(null, { status: 200 }) })
      }
      if (path === '/api/series/{id}/reading-progress') {
        if (!nextReadingProgressOk) {
          return Promise.resolve({ data: null, error: { message: 'chapter must be >= 0' }, response: new Response(null, { status: 400 }) })
        }
        return Promise.resolve({ data: readingProgressDetail, error: null, response: new Response(null, { status: 200 }) })
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

// The body a GET /api/series/{id} returns — mutable so a post-merge refetch can
// return the consolidated detail (defaults back to initialDetail each test).
let seriesDetailResponse: unknown

// useProgressStream is mocked so tests can capture the registered
// `provider.merged` handler and fire it manually to simulate the completion SSE,
// and toggle `connected` to simulate an SSE stream drop+reconnect.
const progressHandlers = new Map<string, Set<(d: unknown) => void>>()
const progressConnected = ref(true)
function fireProgress(event: string, data: unknown): void {
  progressHandlers.get(event)?.forEach(cb => cb(data))
}

// flushAll drains the microtask queue + Vue's scheduler so an ASYNC watcher body
// (the reconnect reconcile awaits refresh()) fully settles before assertions.
async function flushAll(): Promise<void> {
  for (let i = 0; i < 5; i++) {
    await Promise.resolve()
    await nextTick()
  }
}
vi.mock('~/composables/useProgressStream', () => ({
  useProgressStream: () => ({
    on: (event: string, cb: (d: unknown) => void) => {
      if (!progressHandlers.has(event)) progressHandlers.set(event, new Set())
      progressHandlers.get(event)!.add(cb)
      return () => progressHandlers.get(event)?.delete(cb)
    },
    connected: progressConnected,
    connect: vi.fn(),
    disconnect: vi.fn(),
  }),
}))

describe('useSeriesDetail — matchDiskProvider (async)', () => {
  beforeEach(() => {
    calls = []
    nextMatchOk = true
    seriesDetailResponse = initialDetail
    progressConnected.value = true
  })
  afterEach(() => {
    progressHandlers.clear()
  })

  it('POSTs the match endpoint with the path params and exact body', async () => {
    const { refresh, matchDiskProvider } = useSeriesDetail('series-1')
    await refresh()

    await matchDiskProvider('disk-provider-1', { source: 'src-1', mangaId: 42, url: '/manga/42', importance: 1, scanlator: '' })

    const postCall = calls.find(c => c.path === '/api/series/{id}/providers/{providerId}/match')
    expect(postCall).toBeDefined()
    expect(postCall!.params).toEqual({ id: 'series-1', providerId: 'disk-provider-1' })
    expect(postCall!.body).toEqual({ source: 'src-1', mangaId: 42, url: '/manga/42', importance: 1, scanlator: '' })
  })

  it('on 202 keeps matchBusy + shows in-progress, resolves true, and does NOT reseed yet', async () => {
    const { series, matchBusy, dedupMessage, refresh, matchDiskProvider } = useSeriesDetail('series-1')
    await refresh()
    expect(series.value?.providers[0]?.linked).toBe(false)

    const ok = await matchDiskProvider('disk-provider-1', { source: 'src-1', mangaId: 42, url: '/manga/42', importance: 1 })

    expect(ok).toBe(true) // dialog closes
    expect(matchBusy.value).toBe(true) // still "matching…" until the SSE lands
    expect(dedupMessage.value).toBe('Matching in progress…')
    // Series is UNCHANGED — the merge has not completed yet.
    expect(series.value?.providers[0]?.id).toBe('disk-provider-1')
    expect(series.value?.providers[0]?.linked).toBe(false)
  })

  it('refetches + clears the busy state when the provider.merged SSE event arrives', async () => {
    const { series, matchBusy, dedupMessage, refresh, matchDiskProvider } = useSeriesDetail('series-1')
    await refresh()
    await matchDiskProvider('disk-provider-1', { source: 'src-1', mangaId: 42, url: '/manga/42', importance: 1 })

    // The backend finishes the merge; the refetch now returns the consolidated detail.
    seriesDetailResponse = matchedDetail
    fireProgress('provider.merged', { seriesId: 'series-1' })
    await nextTick()
    await nextTick()

    expect(matchBusy.value).toBe(false)
    expect(dedupMessage.value).toBe('Match complete')
    expect(series.value?.providers).toHaveLength(1)
    expect(series.value?.providers[0]?.id).toBe('real-provider-1')
    expect(series.value?.providers[0]?.linked).toBe(true)
  })

  it('ignores a provider.merged event for a DIFFERENT series', async () => {
    const { matchBusy, refresh, matchDiskProvider } = useSeriesDetail('series-1')
    await refresh()
    await matchDiskProvider('disk-provider-1', { source: 'src-1', mangaId: 42, url: '/manga/42', importance: 1 })

    fireProgress('provider.merged', { seriesId: 'some-other-series' })
    await nextTick()

    expect(matchBusy.value).toBe(true) // untouched — the event was for another series
  })

  it('surfaces the error (never swallowed) when the provider.merged event reports a failure', async () => {
    const { series, error, matchBusy, dedupMessage, refresh, matchDiskProvider } = useSeriesDetail('series-1')
    await refresh()
    await matchDiskProvider('disk-provider-1', { source: 'src-1', mangaId: 42, url: '/manga/42', importance: 1 })

    fireProgress('provider.merged', { seriesId: 'series-1', error: 'provider does not belong to series' })
    await nextTick()

    expect(matchBusy.value).toBe(false)
    expect(error.value).toBe('provider does not belong to series')
    expect(dedupMessage.value).toBeNull()
    // Series untouched — the failed merge changed nothing.
    expect(series.value?.providers[0]?.linked).toBe(false)
  })

  it('an SSE reconnect while matching refetches and clears busy once the merge finished (missed event)', async () => {
    const { series, matchBusy, dedupMessage, refresh, matchDiskProvider } = useSeriesDetail('series-1')
    await refresh()
    await matchDiskProvider('disk-provider-1', { source: 'src-1', mangaId: 42, url: '/manga/42', importance: 1 })
    expect(matchBusy.value).toBe(true)

    // The merge finished server-side (disk twin gone) but its provider.merged
    // event was MISSED because the stream dropped. The stream now reconnects.
    // (nextTick between the toggles so the watcher observes the drop — Vue would
    // otherwise batch false→true into a no-op.)
    seriesDetailResponse = matchedDetail
    progressConnected.value = false
    await nextTick()
    progressConnected.value = true
    await flushAll()

    expect(matchBusy.value).toBe(false)
    expect(dedupMessage.value).toBe('Match complete')
    expect(series.value?.providers[0]?.id).toBe('real-provider-1')
  })

  it('an SSE reconnect while the merge is still running keeps the busy state', async () => {
    const { matchBusy, refresh, matchDiskProvider } = useSeriesDetail('series-1')
    await refresh()
    await matchDiskProvider('disk-provider-1', { source: 'src-1', mangaId: 42, url: '/manga/42', importance: 1 })

    // Still running: the refetch still shows the disk provider (disk-provider-1),
    // so the reconnect reconcile must NOT clear the indicator.
    seriesDetailResponse = initialDetail
    progressConnected.value = false
    await nextTick()
    progressConnected.value = true
    await flushAll()

    expect(matchBusy.value).toBe(true)
  })

  it('a hard failure to START sets error, resolves false, clears busy, and leaves series untouched', async () => {
    nextMatchOk = false
    const { series, error, matchBusy, refresh, matchDiskProvider } = useSeriesDetail('series-1')
    await refresh()

    const ok = await matchDiskProvider('disk-provider-1', { source: 'src-1', mangaId: 42, url: '/manga/42', importance: 1 })

    expect(ok).toBe(false)
    expect(matchBusy.value).toBe(false)
    expect(error.value).toBe('match failed')
    expect(series.value?.providers[0]?.id).toBe('disk-provider-1')
    expect(series.value?.providers[0]?.linked).toBe(false)
  })

  it('matchBusy flips true synchronously when the call starts', async () => {
    const { matchBusy, refresh, matchDiskProvider } = useSeriesDetail('series-1')
    await refresh()
    expect(matchBusy.value).toBe(false)

    const promise = matchDiskProvider('disk-provider-1', { source: 'src-1', mangaId: 42, url: '/manga/42', importance: 1 })
    expect(matchBusy.value).toBe(true)
    await promise
    expect(matchBusy.value).toBe(true) // stays true until the SSE completion
  })
})

describe('useSeriesDetail — consolidateProviders (async, QCAT-295 Part B)', () => {
  beforeEach(() => {
    calls = []
    nextConsolidateOk = true
    seriesDetailResponse = initialDetail
    progressConnected.value = true
  })
  afterEach(() => {
    progressHandlers.clear()
  })

  it('POSTs the consolidate endpoint with the exact {providerIds, target} body (existing-provider arm)', async () => {
    const { refresh, consolidateProviders } = useSeriesDetail('series-1')
    await refresh()

    await consolidateProviders(['d1', 'd2'], { existingProviderId: 'target-1' })

    const postCall = calls.find(c => c.path === '/api/series/{id}/providers/consolidate')
    expect(postCall).toBeDefined()
    expect(postCall!.params).toEqual({ id: 'series-1' })
    expect(postCall!.body).toEqual({ providerIds: ['d1', 'd2'], target: { existingProviderId: 'target-1' } })
  })

  it('POSTs the match-to-source arm body verbatim', async () => {
    const { refresh, consolidateProviders } = useSeriesDetail('series-1')
    await refresh()

    await consolidateProviders(['d1'], { source: { source: '7', url: '/manga/7', scanlator: '', importance: 20 } })

    const postCall = calls.find(c => c.path === '/api/series/{id}/providers/consolidate')
    expect(postCall!.body).toEqual({ providerIds: ['d1'], target: { source: { source: '7', url: '/manga/7', scanlator: '', importance: 20 } } })
  })

  it('on 202 keeps matchBusy + shows in-progress, resolves true, and does NOT reseed yet', async () => {
    const { series, matchBusy, dedupMessage, refresh, consolidateProviders } = useSeriesDetail('series-1')
    await refresh()

    const ok = await consolidateProviders(['disk-provider-1'], { existingProviderId: 'target-1' })

    expect(ok).toBe(true)
    expect(matchBusy.value).toBe(true)
    expect(dedupMessage.value).toBe('Merging sources…')
    // Unchanged until the SSE lands.
    expect(series.value?.providers[0]?.id).toBe('disk-provider-1')
  })

  it('refetches + clears the busy state when the provider.merged SSE event arrives', async () => {
    const { matchBusy, refresh, consolidateProviders } = useSeriesDetail('series-1')
    await refresh()
    await consolidateProviders(['disk-provider-1'], { existingProviderId: 'target-1' })

    seriesDetailResponse = matchedDetail
    fireProgress('provider.merged', { seriesId: 'series-1', merged: 1, skipped: 0 })
    await flushAll()

    expect(matchBusy.value).toBe(false)
  })

  it('surfaces a hard start failure via error, resolves false (dialog stays open)', async () => {
    nextConsolidateOk = false
    const { error, matchBusy, refresh, consolidateProviders } = useSeriesDetail('series-1')
    await refresh()

    const ok = await consolidateProviders(['d1'], { existingProviderId: 'target-1' })

    expect(ok).toBe(false)
    expect(matchBusy.value).toBe(false)
    expect(error.value).toBe('consolidate failed')
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

  it('maps the fractional evidence (count + list + the owner\'s switch) off the same response', async () => {
    const { series, refresh } = useSeriesDetail('series-1')
    await refresh()

    const provider = series.value!.providers.find((p) => p.id === 'real-provider-2')!
    expect(provider.fractionalCount).toBe(2)
    expect(provider.fractionalChapters).toEqual(['1.1', '2.1'])
    expect(provider.ignoreFractional).toBe(false)
  })
})

describe('useSeriesDetail — setIgnoreFractional', () => {
  beforeEach(() => {
    calls = []
    nextPatchOk = true
  })

  it('PATCHes the ignore-fractional endpoint with both path params and the exact body', async () => {
    const { setIgnoreFractional } = useSeriesDetail('series-1')

    const ok = await setIgnoreFractional('real-provider-2', true)

    expect(ok).toBe(true)
    const call = calls.find((c) => c.path === '/api/series/{id}/providers/{providerId}/ignore-fractional')!
    expect(call.method).toBe('PATCH')
    expect(call.params).toEqual({ id: 'series-1', providerId: 'real-provider-2' })
    expect(call.body).toEqual({ ignoreFractional: true })
  })

  it('resolves false and surfaces the error on failure (never swallowed)', async () => {
    const { setIgnoreFractional, error } = useSeriesDetail('series-1')
    nextPatchOk = false

    const ok = await setIgnoreFractional('real-provider-2', true)

    expect(ok).toBe(false)
    expect(error.value).toBe('Update failed')
  })
})

/**
 * The owner-triggered fractional cleanup: a plain preview GET (it decides whether
 * the button is offered at all) + the removal POST, which carries ONLY the ticked
 * ids and reports its outcome so the dialog closes only on success (§16).
 */
describe('useSeriesDetail — fractional cleanup', () => {
  beforeEach(() => {
    calls = []
    nextFractionalPreviewOk = true
    nextFractionalPreviewMalformed = false
    nextFractionalRemoveOk = true
  })

  it('fetchFractionalCleanup GETs the preview and maps the evidence (pages + yardstick)', async () => {
    const { fetchFractionalCleanup } = useSeriesDetail('series-1')

    const preview = await fetchFractionalCleanup()

    const call = calls.find((c) => c.method === 'GET' && c.path === '/api/series/{id}/fractional-cleanup')!
    expect(call.params).toEqual({ id: 'series-1' })
    expect(preview!.typicalPageCount).toBe(96)
    expect(preview!.chapters.map((c) => c.pageCount)).toEqual([1, 132])
  })

  it('fetchFractionalCleanup resolves null on failure (the button just stays hidden)', async () => {
    nextFractionalPreviewOk = false
    const { fetchFractionalCleanup, error } = useSeriesDetail('series-1')

    expect(await fetchFractionalCleanup()).toBeNull()
    // A background read the owner never asked for must not raise a page error.
    expect(error.value).toBeNull()
  })

  it('resolves null (never throws) on a 200 whose body carries no chapters array', async () => {
    nextFractionalPreviewMalformed = true
    const { fetchFractionalCleanup } = useSeriesDetail('series-1')

    await expect(fetchFractionalCleanup()).resolves.toBeNull()
  })

  it('removeFractionalChapters POSTs exactly the TICKED ids and refreshes the series', async () => {
    const { refresh, removeFractionalChapters } = useSeriesDetail('series-1')
    await refresh()
    const getBefore = calls.filter((c) => c.method === 'GET' && c.path === '/api/series/{id}').length

    const ok = await removeFractionalChapters(['c-1815', 'c-31'])

    expect(ok).toBe(true)
    const post = calls.find((c) => c.method === 'POST' && c.path === '/api/series/{id}/fractional-cleanup')!
    expect(post.params).toEqual({ id: 'series-1' })
    expect(post.body).toEqual({ chapterIds: ['c-1815', 'c-31'] })
    // Removal changes the chapter list → the series is refetched (mutate's default onSuccess).
    const getAfter = calls.filter((c) => c.method === 'GET' && c.path === '/api/series/{id}').length
    expect(getAfter).toBe(getBefore + 1)
  })

  it('removeFractionalChapters resolves false and surfaces the error (the dialog stays open)', async () => {
    nextFractionalRemoveOk = false
    const { error, removeFractionalChapters } = useSeriesDetail('series-1')

    const ok = await removeFractionalChapters(['c-2215'])

    expect(ok).toBe(false)
    expect(error.value).toBe('Update failed')
  })

  it('fractionalBusy flips true during the removal and back to false once it resolves', async () => {
    const { fractionalBusy, removeFractionalChapters } = useSeriesDetail('series-1')
    expect(fractionalBusy.value).toBe(false)

    const promise = removeFractionalChapters(['c-1815'])
    expect(fractionalBusy.value).toBe(true)
    await promise

    expect(fractionalBusy.value).toBe(false)
  })
})

describe('useSeriesDetail — dedupProviders / dedupeFiles', () => {
  beforeEach(() => {
    calls = []
    nextDedupOk = true
    nextDedupeFilesOk = true
    nextDedupePreviewOk = true
    nextDedupePreviewMalformed = false
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

  it('dedupeFiles sets the removed message and refreshes the series', async () => {
    const { series, refresh, dedupeFiles, dedupMessage } = useSeriesDetail('series-1')
    await refresh()
    const getBefore = calls.filter(c => c.method === 'GET' && c.path === '/api/series/{id}').length

    await dedupeFiles()

    expect(dedupMessage.value).toContain('3')
    // The merge pass can delete chapter rows, so dedupeFiles refreshes the series
    // (one extra GET) rather than reseeding from the count-only response.
    const getAfter = calls.filter(c => c.method === 'GET' && c.path === '/api/series/{id}').length
    expect(getAfter).toBe(getBefore + 1)
    expect(series.value?.providers).toHaveLength(2)
  })

  it('dedupeFiles resolves true on success and false on failure (dialog closes only on true)', async () => {
    const ok = useSeriesDetail('series-1')
    await ok.refresh()
    expect(await ok.dedupeFiles()).toBe(true)

    nextDedupeFilesOk = false
    const bad = useSeriesDetail('series-1')
    await bad.refresh()
    expect(await bad.dedupeFiles()).toBe(false)
  })

  it('fetchDedupePreview maps the dry-run plan (GET, not POST — deletes nothing)', async () => {
    const { fetchDedupePreview } = useSeriesDetail('series-1')
    const postBefore = calls.filter(c => c.method === 'POST' && c.path === '/api/series/{id}/dedupe-files').length

    const plan = await fetchDedupePreview()

    expect(plan).toEqual(dedupePreview)
    // A preview is a READ — it must never fire the destructive POST.
    const postAfter = calls.filter(c => c.method === 'POST' && c.path === '/api/series/{id}/dedupe-files').length
    expect(postAfter).toBe(postBefore)
  })

  it('fetchDedupePreview surfaces a failure via error and resolves null (owner-triggered, §16)', async () => {
    nextDedupePreviewOk = false
    const { error, fetchDedupePreview } = useSeriesDetail('series-1')
    expect(await fetchDedupePreview()).toBeNull()
    expect(error.value).toBeTruthy()
  })

  it('fetchDedupePreview treats a body without an items array as a hard error, not an empty plan', async () => {
    nextDedupePreviewMalformed = true
    const { error, fetchDedupePreview } = useSeriesDetail('series-1')
    expect(await fetchDedupePreview()).toBeNull()
    expect(error.value).toBeTruthy()
  })
})

/**
 * setReadingProgress (QCAT-242) — the owner's "re-read from start"/"jump to
 * chapter N" action. Pins:
 *   1. It POSTs /api/series/{id}/reading-progress with the exact {chapter} body.
 *   2. On success it reseeds `series` DIRECTLY from the response — NOT via a
 *      second GET /api/series/{id} (mutate-reseeds-from-response, §16) —
 *      and resolves true.
 *   3. On failure it sets `progressError` to the backend's OWN message
 *      (never a generic fallback when the backend supplied one), resolves
 *      false, and leaves `series` untouched.
 *   4. `settingProgress` flips true for the duration of the call and back to
 *      false once it resolves, win or lose.
 *   5. It never touches the shared `error` ref — a failure here must not be
 *      mistaken for a different mutation's error by a screen watching `error`.
 */
describe('useSeriesDetail — setReadingProgress', () => {
  beforeEach(() => {
    calls = []
    nextReadingProgressOk = true
  })

  it('POSTs the reading-progress endpoint with the path param and exact {chapter} body', async () => {
    const { refresh, setReadingProgress } = useSeriesDetail('series-1')
    await refresh()

    await setReadingProgress(42)

    const post = calls.find((c) => c.path === '/api/series/{id}/reading-progress')
    expect(post).toBeDefined()
    expect(post!.params).toEqual({ id: 'series-1' })
    expect(post!.body).toEqual({ chapter: 42 })
  })

  it('accepts chapter 0 (re-read from scratch) — a falsy-but-valid value', async () => {
    const { setReadingProgress } = useSeriesDetail('series-1')

    await setReadingProgress(0)

    const post = calls.find((c) => c.path === '/api/series/{id}/reading-progress')
    expect(post!.body).toEqual({ chapter: 0 })
  })

  it('reseeds series directly from the response on success, without a second GET', async () => {
    const { series, refresh, setReadingProgress } = useSeriesDetail('series-1')
    await refresh()
    const getCountBefore = calls.filter((c) => c.method === 'GET' && c.path === '/api/series/{id}').length

    const ok = await setReadingProgress(5)

    expect(ok).toBe(true)
    expect(series.value?.chapterCounts.unread).toBe(3) // readingProgressDetail's distinct value
    const getCountAfter = calls.filter((c) => c.method === 'GET' && c.path === '/api/series/{id}').length
    expect(getCountAfter).toBe(getCountBefore)
  })

  it('failure surfaces the backend\'s own message via progressError, resolves false, and leaves series untouched', async () => {
    nextReadingProgressOk = false
    const { series, error, progressError, refresh, setReadingProgress } = useSeriesDetail('series-1')
    await refresh()

    const ok = await setReadingProgress(-1)

    expect(ok).toBe(false)
    expect(progressError.value).toBe('chapter must be >= 0')
    // The shared `error` ref belongs to OTHER mutations — this one never touches it.
    expect(error.value).toBeNull()
    expect(series.value?.chapterCounts.unread).toBeUndefined()
  })

  it('settingProgress flips true during the call and back to false once it resolves', async () => {
    const { settingProgress, setReadingProgress } = useSeriesDetail('series-1')
    expect(settingProgress.value).toBe(false)

    const promise = setReadingProgress(5)
    expect(settingProgress.value).toBe(true)
    await promise

    expect(settingProgress.value).toBe(false)
  })
})
