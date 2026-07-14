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

  it('bind() POSTs {trackerId, remoteId} and applies the returned binding directly (§16, no refetch)', async () => {
    bindingsResponse = { data: [], error: null }
    const NEW_BINDING = { ...BINDING_ANILIST, trackerId: 1, trackerName: 'MyAnimeList', id: 'bind-2' }
    bindResponse = { data: NEW_BINDING, error: null }

    const { bind, bindings, pending } = useSeriesTracking(SERIES_ID)
    await vi.waitFor(() => expect(pending.value).toBe(false))
    expect(bindings.value).toHaveLength(0)

    const ok = await bind(1, '12345')

    expect(ok).toBe(true)
    expect(postCalls.at(-1)).toEqual({
      path: '/api/series/{id}/tracking',
      opts: { params: { path: { id: SERIES_ID } }, body: { trackerId: 1, remoteId: '12345' } },
    })
    expect(bindings.value).toHaveLength(1)
    expect(bindings.value[0]?.id).toBe('bind-2')
    // No extra GET round-trip — the response is applied directly.
    const bindingGetCalls = getCalls.filter((c) => c.path === '/api/series/{id}/tracking').length
    expect(bindingGetCalls).toBe(1)
  })

  it('unbind() DELETEs with the deleteRemote query flag and removes the row locally on success', async () => {
    const { unbind, bindings, pending } = useSeriesTracking(SERIES_ID)
    await vi.waitFor(() => expect(pending.value).toBe(false))
    expect(bindings.value).toHaveLength(1)

    const ok = await unbind('bind-1', true)

    expect(ok).toBe(true)
    expect(deleteCalls.at(-1)).toEqual({
      path: '/api/series/{id}/tracking/{recordId}',
      opts: { params: { path: { id: SERIES_ID, recordId: 'bind-1' }, query: { deleteRemote: true } } },
    })
    expect(bindings.value).toHaveLength(0)
  })

  it('unbind() keeps the row and surfaces unbindError on failure', async () => {
    unbindResponse = { error: { message: 'remote deletion failed' } }
    const { unbind, bindings, unbindError, pending } = useSeriesTracking(SERIES_ID)
    await vi.waitFor(() => expect(pending.value).toBe(false))

    const ok = await unbind('bind-1', true)

    expect(ok).toBe(false)
    expect(unbindError.value).toBe('remote deletion failed')
    expect(bindings.value).toHaveLength(1)
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
})
