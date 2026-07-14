/**
 * useSeriesTracking — unit tests for one series' tracker-binding data layer
 * (Phase 3d). Pins: the series-scoped GET/POST/DELETE paths, that bind/refresh
 * apply their response directly (§16 — no extra list round-trip), and that
 * unbind removes the row locally on success.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useSeriesTracking } from './useSeriesTracking'

const SERIES_ID = 'series-1'

const BINDING_ANILIST = {
  id: 'bind-1',
  seriesId: SERIES_ID,
  trackerId: 2,
  trackerName: 'AniList',
  remoteId: '105398',
  remoteUrl: 'https://anilist.co/manga/105398',
  libraryId: '',
  title: 'Chainsaw Man',
  status: 'CURRENT',
  lastChapterRead: 12,
  totalChapters: 0,
  score: 0,
  scoreFormat: 'POINT_100',
  startDate: null,
  finishDate: null,
  private: false,
}

let getCalls: { path: string, opts: unknown }[] = []
let postCalls: { path: string, opts: unknown }[] = []
let deleteCalls: { path: string, opts: unknown }[] = []

let bindingsResponse: { data: unknown, error: unknown } = { data: [BINDING_ANILIST], error: null }
let searchResponse: { data: unknown, error: unknown } = { data: [], error: null }
let bindResponse: { data: unknown, error: unknown } = { data: BINDING_ANILIST, error: null }
let refreshResponse: { data: unknown, error: unknown } = { data: BINDING_ANILIST, error: null }
let unbindResponse: { error: unknown } = { error: null }
let updateResponse: { data: unknown, error: unknown } = { data: BINDING_ANILIST, error: null }
let syncResponse: { data: unknown, error: unknown } = { data: [BINDING_ANILIST], error: null }

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string, opts: unknown) => {
      getCalls.push({ path, opts })
      if (path === '/api/series/{id}/tracking') return Promise.resolve(bindingsResponse)
      if (path === '/api/trackers/{id}/search') return Promise.resolve(searchResponse)
      return Promise.resolve({ data: null, error: null })
    }),
    POST: vi.fn().mockImplementation((path: string, opts: unknown) => {
      postCalls.push({ path, opts })
      if (path === '/api/series/{id}/tracking') return Promise.resolve(bindResponse)
      if (path === '/api/series/{id}/tracking/{recordId}/refresh') return Promise.resolve(refreshResponse)
      if (path === '/api/series/{id}/tracking/{recordId}/update') return Promise.resolve(updateResponse)
      if (path === '/api/series/{id}/tracking/sync') return Promise.resolve(syncResponse)
      return Promise.resolve({ data: null, error: null })
    }),
    DELETE: vi.fn().mockImplementation((path: string, opts: unknown) => {
      deleteCalls.push({ path, opts })
      return Promise.resolve(unbindResponse)
    }),
    PATCH: vi.fn(),
    PUT: vi.fn(),
    use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

describe('useSeriesTracking', () => {
  beforeEach(() => {
    getCalls = []
    postCalls = []
    deleteCalls = []
    bindingsResponse = { data: [BINDING_ANILIST], error: null }
    searchResponse = { data: [], error: null }
    bindResponse = { data: BINDING_ANILIST, error: null }
    refreshResponse = { data: BINDING_ANILIST, error: null }
    unbindResponse = { error: null }
    updateResponse = { data: BINDING_ANILIST, error: null }
    syncResponse = { data: [BINDING_ANILIST], error: null }
  })

  it('loads the series\' bindings on construction', async () => {
    const { bindings, pending } = useSeriesTracking(SERIES_ID)
    await vi.waitFor(() => expect(pending.value).toBe(false))
    expect(bindings.value).toHaveLength(1)
    expect(bindings.value[0]?.trackerName).toBe('AniList')
    expect(getCalls[0]).toEqual({
      path: '/api/series/{id}/tracking',
      opts: { params: { path: { id: SERIES_ID } } },
    })
  })

  it('search() GETs the tracker-scoped search with the query', async () => {
    searchResponse = {
      data: [{ remoteId: '999', title: 'One Piece', url: 'https://x', coverUrl: '', status: 'RELEASING', totalChapters: 0 }],
      error: null,
    }
    const { search, searchResults, pending } = useSeriesTracking(SERIES_ID)
    await vi.waitFor(() => expect(pending.value).toBe(false))

    await search(2, 'one piece')

    expect(getCalls.at(-1)).toEqual({
      path: '/api/trackers/{id}/search',
      opts: { params: { path: { id: 2 }, query: { q: 'one piece' } } },
    })
    expect(searchResults.value).toHaveLength(1)
    expect(searchResults.value[0]?.title).toBe('One Piece')
  })

  it('search() carries the type/startDate/score/description enrichment fields through', async () => {
    searchResponse = {
      data: [{
        remoteId: '999',
        title: 'One Piece',
        url: 'https://x',
        coverUrl: '',
        status: 'RELEASING',
        totalChapters: 0,
        type: 'MANGA',
        startDate: '1997',
        score: 89,
        description: 'A pirate adventure.',
      }],
      error: null,
    }
    const { search, searchResults, pending } = useSeriesTracking(SERIES_ID)
    await vi.waitFor(() => expect(pending.value).toBe(false))

    await search(2, 'one piece')

    expect(searchResults.value[0]).toMatchObject({
      type: 'MANGA',
      startDate: '1997',
      score: 89,
      description: 'A pirate adventure.',
    })
  })

  it('search() sets searchError to the backend message on failure and clears any stale results (bug 2)', async () => {
    searchResponse = { data: [{ remoteId: '999', title: 'One Piece', url: 'https://x', coverUrl: '', status: 'RELEASING', totalChapters: 0 }], error: null }
    const { search, searchResults, searchError, pending } = useSeriesTracking(SERIES_ID)
    await vi.waitFor(() => expect(pending.value).toBe(false))
    await search(2, 'one piece')
    expect(searchResults.value).toHaveLength(1)

    searchResponse = { data: null, error: { message: 'anilist: rate limited — 429' } }
    await search(2, 'one piece')

    expect(searchError.value).toBe('anilist: rate limited — 429')
    expect(searchResults.value).toHaveLength(0)
  })

  it('search() clears stale results/error SYNCHRONOUSLY when switching to a different tracker (bug 1 — results must not leak across trackers)', async () => {
    searchResponse = {
      data: [{ remoteId: '1', title: 'AniList hit', url: 'https://x', coverUrl: '', status: 'RELEASING', totalChapters: 0 }],
      error: null,
    }
    const { search, searchResults } = useSeriesTracking(SERIES_ID)
    await vi.waitFor(() => expect(searchResults.value).toHaveLength(0))

    await search(2, 'query') // AniList (trackerId 2)
    expect(searchResults.value).toHaveLength(1)
    expect(searchResults.value[0]?.title).toBe('AniList hit')

    // Switching to a different tracker (MyAnimeList, id 1) must clear the
    // stale AniList results BEFORE the new request even resolves — the owner
    // must never see the previous tracker's results under the new row.
    const pending2 = search(1, 'other query')
    expect(searchResults.value).toHaveLength(0)
    await pending2
  })

  it('clearSearch() resets searchResults/searchError/bindError (bug 1 — the row-switch UI trigger)', async () => {
    searchResponse = { data: [{ remoteId: '1', title: 'Hit', url: 'https://x', coverUrl: '', status: 'RELEASING', totalChapters: 0 }], error: null }
    bindResponse = { data: null, error: { message: 'bind failed' } }

    const { search, bind, clearSearch, searchResults, searchError, bindError, pending } = useSeriesTracking(SERIES_ID)
    await vi.waitFor(() => expect(pending.value).toBe(false))

    await search(2, 'q')
    await bind(2, '1')
    expect(searchResults.value).toHaveLength(1)
    expect(bindError.value).toBe('bind failed')

    clearSearch()

    expect(searchResults.value).toHaveLength(0)
    expect(searchError.value).toBeNull()
    expect(bindError.value).toBeNull()
  })

  it('bind() POSTs {trackerId, remoteId} and applies the returned binding directly (§16)', async () => {
    bindingsResponse = { data: [], error: null }
    const NEW_BINDING = { ...BINDING_ANILIST, trackerId: 1, trackerName: 'MyAnimeList', id: 'bind-2' }
    bindResponse = { data: NEW_BINDING, error: null }

    const { bind, bindings, pending } = useSeriesTracking(SERIES_ID)
    await vi.waitFor(() => expect(pending.value).toBe(false))
    expect(bindings.value).toHaveLength(0)

    // The backend's list-view already reflects the mutation by the time the
    // BUG-3 background reconciliation refetch runs (see below) — keep the mock
    // consistent with what a real backend would return post-bind.
    bindingsResponse = { data: [NEW_BINDING], error: null }
    const ok = await bind(1, '12345')

    expect(ok).toBe(true)
    expect(postCalls.at(-1)).toEqual({
      path: '/api/series/{id}/tracking',
      opts: { params: { path: { id: SERIES_ID } }, body: { trackerId: 1, remoteId: '12345' } },
    })
    expect(bindings.value).toHaveLength(1)
    expect(bindings.value[0]?.id).toBe('bind-2')
  })

  it('bind() sets bindError to the backend message on failure and leaves bindings untouched (bug 2)', async () => {
    bindResponse = { data: null, error: { message: 'anilist: manga not found — 404' } }

    const { bind, bindings, bindError, pending } = useSeriesTracking(SERIES_ID)
    await vi.waitFor(() => expect(pending.value).toBe(false))

    const ok = await bind(2, 'bogus-id')

    expect(ok).toBe(false)
    expect(bindError.value).toBe('anilist: manga not found — 404')
    expect(bindings.value).toHaveLength(1)
  })

  it('a successful bind() triggers a silent background bindings refetch (bug 3 — no manual refresh needed)', async () => {
    bindingsResponse = { data: [], error: null }
    const NEW_BINDING = { ...BINDING_ANILIST, trackerId: 1, trackerName: 'MyAnimeList', id: 'bind-2' }
    bindResponse = { data: NEW_BINDING, error: null }

    const { bind, bindings, pending } = useSeriesTracking(SERIES_ID)
    await vi.waitFor(() => expect(pending.value).toBe(false))
    const bindingGetCallsBefore = getCalls.filter((c) => c.path === '/api/series/{id}/tracking').length

    // The backend now reflects the new binding — the refetch should reconfirm it.
    bindingsResponse = { data: [NEW_BINDING], error: null }
    await bind(1, '12345')
    // The background refetch fires on a microtask — flush it.
    await vi.waitFor(() => {
      const calls = getCalls.filter((c) => c.path === '/api/series/{id}/tracking').length
      expect(calls).toBe(bindingGetCallsBefore + 1)
    })

    // Silent — never flashes the loading skeleton over the just-applied state.
    expect(pending.value).toBe(false)
    expect(bindings.value).toHaveLength(1)
    expect(bindings.value[0]?.id).toBe('bind-2')
  })

  it('bind() sends {private: true} when the owner opts in, and omits it otherwise', async () => {
    const { bind, pending } = useSeriesTracking(SERIES_ID)
    await vi.waitFor(() => expect(pending.value).toBe(false))

    await bind(2, '105398', true)
    expect(postCalls.at(-1)).toEqual({
      path: '/api/series/{id}/tracking',
      opts: { params: { path: { id: SERIES_ID } }, body: { trackerId: 2, remoteId: '105398', private: true } },
    })

    await bind(2, '105398', false)
    expect(postCalls.at(-1)).toEqual({
      path: '/api/series/{id}/tracking',
      opts: { params: { path: { id: SERIES_ID } }, body: { trackerId: 2, remoteId: '105398' } },
    })
  })

  it('unbind() DELETEs with the deleteRemote query flag and removes the row locally on success', async () => {
    const { unbind, bindings, pending } = useSeriesTracking(SERIES_ID)
    await vi.waitFor(() => expect(pending.value).toBe(false))
    expect(bindings.value).toHaveLength(1)

    // The backend's list-view already reflects the deletion by the time the
    // BUG-3 background reconciliation refetch runs.
    bindingsResponse = { data: [], error: null }
    const ok = await unbind('bind-1', true)

    expect(ok).toBe(true)
    expect(deleteCalls.at(-1)).toEqual({
      path: '/api/series/{id}/tracking/{recordId}',
      opts: { params: { path: { id: SERIES_ID, recordId: 'bind-1' }, query: { deleteRemote: true } } },
    })
    expect(bindings.value).toHaveLength(0)
  })

  it('unbind() keeps the row and surfaces unbindError + unbindErrorId on failure (bug 2 — scoped to the failing row)', async () => {
    unbindResponse = { error: { message: 'remote deletion failed' } }
    const { unbind, bindings, unbindError, unbindErrorId, pending } = useSeriesTracking(SERIES_ID)
    await vi.waitFor(() => expect(pending.value).toBe(false))

    const ok = await unbind('bind-1', true)

    expect(ok).toBe(false)
    expect(unbindError.value).toBe('remote deletion failed')
    expect(unbindErrorId.value).toBe('bind-1')
    expect(bindings.value).toHaveLength(1)
  })

  it('a successful unbind() triggers a silent background bindings refetch (bug 3)', async () => {
    const { unbind, bindings, pending } = useSeriesTracking(SERIES_ID)
    await vi.waitFor(() => expect(pending.value).toBe(false))
    const bindingGetCallsBefore = getCalls.filter((c) => c.path === '/api/series/{id}/tracking').length

    // The backend now has nothing left — the refetch should reconfirm it.
    bindingsResponse = { data: [], error: null }
    await unbind('bind-1', true)

    await vi.waitFor(() => {
      const calls = getCalls.filter((c) => c.path === '/api/series/{id}/tracking').length
      expect(calls).toBe(bindingGetCallsBefore + 1)
    })
    expect(pending.value).toBe(false)
    expect(bindings.value).toHaveLength(0)
  })

  it('refresh() POSTs to the refresh route and replaces the row with the response (§16)', async () => {
    const REFRESHED = { ...BINDING_ANILIST, lastChapterRead: 20 }
    refreshResponse = { data: REFRESHED, error: null }

    const { refresh, bindings, pending } = useSeriesTracking(SERIES_ID)
    await vi.waitFor(() => expect(pending.value).toBe(false))

    const ok = await refresh('bind-1')

    expect(ok).toBe(true)
    expect(postCalls.at(-1)).toEqual({
      path: '/api/series/{id}/tracking/{recordId}/refresh',
      opts: { params: { path: { id: SERIES_ID, recordId: 'bind-1' } } },
    })
    expect(bindings.value[0]?.lastChapterRead).toBe(20)
  })

  it('refresh() keeps the row and surfaces refreshError + refreshErrorId on failure (bug 2 — scoped to the failing row)', async () => {
    refreshResponse = { data: null, error: { message: 'anilist: entry not found — 404' } }
    const { refresh, bindings, refreshError, refreshErrorId, pending } = useSeriesTracking(SERIES_ID)
    await vi.waitFor(() => expect(pending.value).toBe(false))

    const ok = await refresh('bind-1')

    expect(ok).toBe(false)
    expect(refreshError.value).toBe('anilist: entry not found — 404')
    expect(refreshErrorId.value).toBe('bind-1')
    expect(bindings.value[0]?.lastChapterRead).toBe(BINDING_ANILIST.lastChapterRead)
  })

  it('updateTrack() POSTs the patch and replaces the row with the response (§16)', async () => {
    const UPDATED = { ...BINDING_ANILIST, status: 'COMPLETED', score: 9 }
    updateResponse = { data: UPDATED, error: null }

    const { updateTrack, bindings, pending } = useSeriesTracking(SERIES_ID)
    await vi.waitFor(() => expect(pending.value).toBe(false))

    // The backend's list-view already reflects the update by the time the
    // BUG-3 background reconciliation refetch runs.
    bindingsResponse = { data: [UPDATED], error: null }
    const ok = await updateTrack('bind-1', { status: 'COMPLETED', score: 9 })

    expect(ok).toBe(true)
    expect(postCalls.at(-1)).toEqual({
      path: '/api/series/{id}/tracking/{recordId}/update',
      opts: { params: { path: { id: SERIES_ID, recordId: 'bind-1' } }, body: { status: 'COMPLETED', score: 9 } },
    })
    expect(bindings.value[0]?.status).toBe('COMPLETED')
    expect(bindings.value[0]?.score).toBe(9)
  })

  it('updateTrack() leaves the list intact and sets updateError on failure', async () => {
    updateResponse = { data: null, error: { message: 'tracker rejected the update' } }

    const { updateTrack, bindings, updateError, pending } = useSeriesTracking(SERIES_ID)
    await vi.waitFor(() => expect(pending.value).toBe(false))

    const ok = await updateTrack('bind-1', { score: 9 })

    expect(ok).toBe(false)
    expect(updateError.value).toBe('tracker rejected the update')
    expect(bindings.value[0]?.score).toBe(BINDING_ANILIST.score)
  })

  it('a successful updateTrack() triggers a silent background bindings refetch (bug 3)', async () => {
    const UPDATED = { ...BINDING_ANILIST, status: 'COMPLETED', score: 9 }
    updateResponse = { data: UPDATED, error: null }

    const { updateTrack, bindings, pending } = useSeriesTracking(SERIES_ID)
    await vi.waitFor(() => expect(pending.value).toBe(false))
    const bindingGetCallsBefore = getCalls.filter((c) => c.path === '/api/series/{id}/tracking').length

    bindingsResponse = { data: [UPDATED], error: null }
    await updateTrack('bind-1', { status: 'COMPLETED', score: 9 })

    await vi.waitFor(() => {
      const calls = getCalls.filter((c) => c.path === '/api/series/{id}/tracking').length
      expect(calls).toBe(bindingGetCallsBefore + 1)
    })
    expect(pending.value).toBe(false)
    expect(bindings.value[0]?.status).toBe('COMPLETED')
  })

  it('syncNow() POSTs to the sync route and replaces the WHOLE bindings list (§16)', async () => {
    const CONVERGED = { ...BINDING_ANILIST, lastChapterRead: 60 }
    syncResponse = { data: [CONVERGED], error: null }

    const { syncNow, bindings, pending } = useSeriesTracking(SERIES_ID)
    await vi.waitFor(() => expect(pending.value).toBe(false))

    const ok = await syncNow()

    expect(ok).toBe(true)
    expect(postCalls.at(-1)).toEqual({
      path: '/api/series/{id}/tracking/sync',
      opts: { params: { path: { id: SERIES_ID } } },
    })
    expect(bindings.value).toHaveLength(1)
    expect(bindings.value[0]?.lastChapterRead).toBe(60)
  })

  it('syncNow() leaves the list intact and sets syncError on failure', async () => {
    syncResponse = { data: null, error: { message: 'sync failed' } }

    const { syncNow, bindings, syncError, pending } = useSeriesTracking(SERIES_ID)
    await vi.waitFor(() => expect(pending.value).toBe(false))

    const ok = await syncNow()

    expect(ok).toBe(false)
    expect(syncError.value).toBe('sync failed')
    expect(bindings.value).toHaveLength(1)
    expect(bindings.value[0]?.id).toBe('bind-1')
  })
})
