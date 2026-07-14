/**
 * useMetadata — data layer for the native metadata engine (Identify search +
 * confirm, cover gallery + pick), keyed to one series.
 *
 * Pins:
 *   1. search(q, providers) GETs /api/metadata/search?q=&providers=, CSV-joins
 *      providers when given and omits the param when absent, and maps each
 *      MetadataSearchResult onto the screen MetadataCandidate shape
 *      (id = `${provider}:${remoteId}`, provider label prettified, year 0 → undefined).
 *   2. search() failure clears candidates and sets searchError (never throws).
 *   3. identify(provider, remoteId) POSTs /api/series/{id}/metadata/identify
 *      with the exact {provider, remoteId} body and resolves the raw DTO.
 *   4. identify() failure sets identifyError and resolves null.
 *   5. loadCovers() GETs /api/series/{id}/metadata/covers and maps each
 *      CoverCandidate DTO onto the screen shape, carrying sourceKind/sourceRef.
 *   6. setCover(sourceKind, sourceRef, coverUrl) POSTs /api/series/{id}/cover
 *      with the exact body and resolves the raw DTO; failure sets setCoverError.
 *   7. Busy flags (searching/identifying/coversLoading/settingCover) are
 *      independent — one action's flag never leaks into another's.
 *
 * vi.mock is hoisted by Vitest's transform so the apiClient mock is in place
 * before useMetadata.ts is evaluated, regardless of import order here.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { useMetadata } from './useMetadata'

interface Call { method: string, path: string, body?: unknown, query?: unknown }
let calls: Call[] = []

let nextSearchOk = true
let nextIdentifyOk = true
let nextCoversOk = true
let nextSetCoverOk = true

const seriesDetailStub = {
  id: 'series-1',
  displayName: 'Solo Leveling',
  status: 'ongoing',
}

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string, opts?: { params?: { query?: Record<string, unknown>, path?: Record<string, unknown> } }) => {
      calls.push({ method: 'GET', path, query: opts?.params?.query })
      if (path === '/api/metadata/search') {
        if (!nextSearchOk) {
          return Promise.resolve({ data: null, error: { message: 'search failed' }, response: new Response(null, { status: 500 }) })
        }
        return Promise.resolve({
          data: [
            { provider: 'anilist', remoteId: '105398', title: 'Solo Leveling', url: 'https://anilist.co/manga/105398', coverUrl: 'https://x/anilist.jpg', year: 2018 },
            { provider: 'unknownprovider', remoteId: '7', title: 'Solo Leveling (WN)', url: '', coverUrl: '', year: 0 },
          ],
          error: null,
          response: new Response(null, { status: 200 }),
        })
      }
      if (path === '/api/series/{id}/metadata/covers') {
        if (!nextCoversOk) {
          return Promise.resolve({ data: null, error: { message: 'covers failed' }, response: new Response(null, { status: 500 }) })
        }
        return Promise.resolve({
          // Two hits from the SAME metadata provider ("anilist") — the real
          // shape a multi-result provider search returns, and the exact case
          // that used to collide on id (BUG-1: `${sourceKind}:${sourceRef}`
          // alone gave both the identical id "metadata:anilist").
          data: [
            { sourceKind: 'metadata', sourceRef: 'anilist', coverUrl: 'https://x/anilist-cover.jpg', label: 'anilist' },
            { sourceKind: 'metadata', sourceRef: 'anilist', coverUrl: 'https://x/anilist-cover-2.jpg', label: 'anilist' },
            { sourceKind: 'source', sourceRef: 'prov-1', coverUrl: 'https://x/source-cover.jpg', label: 'Asura Scans' },
          ],
          error: null,
          response: new Response(null, { status: 200 }),
        })
      }
      return Promise.resolve({ data: null, error: null, response: new Response(null, { status: 200 }) })
    }),
    POST: vi.fn().mockImplementation((path: string, opts?: { params?: { path?: Record<string, unknown> }, body?: unknown }) => {
      calls.push({ method: 'POST', path, body: opts?.body })
      if (path === '/api/series/{id}/metadata/identify') {
        if (!nextIdentifyOk) {
          return Promise.resolve({ data: null, error: { message: 'identify failed' }, response: new Response(null, { status: 400 }) })
        }
        return Promise.resolve({ data: seriesDetailStub, error: null, response: new Response(null, { status: 200 }) })
      }
      if (path === '/api/series/{id}/cover') {
        if (!nextSetCoverOk) {
          return Promise.resolve({ data: null, error: { message: 'set cover failed' }, response: new Response(null, { status: 400 }) })
        }
        return Promise.resolve({ data: seriesDetailStub, error: null, response: new Response(null, { status: 200 }) })
      }
      return Promise.resolve({ data: null, error: null, response: new Response(null, { status: 200 }) })
    }),
    PATCH: vi.fn(),
    DELETE: vi.fn(),
    use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

describe('useMetadata', () => {
  beforeEach(() => {
    calls = []
    nextSearchOk = true
    nextIdentifyOk = true
    nextCoversOk = true
    nextSetCoverOk = true
  })

  it('search(q) GETs /api/metadata/search with no providers param when omitted, and maps results', async () => {
    const { candidates, search, searchError, searching } = useMetadata('series-1')

    const pending = search('Solo Leveling')
    expect(searching.value).toBe(true)
    await pending
    expect(searching.value).toBe(false)
    expect(searchError.value).toBeNull()

    expect(calls).toEqual([{ method: 'GET', path: '/api/metadata/search', query: { q: 'Solo Leveling' } }])
    expect(candidates.value).toEqual([
      { id: 'anilist:105398', provider: 'AniList', providerKey: 'anilist', remoteId: '105398', title: 'Solo Leveling', coverUrl: 'https://x/anilist.jpg', year: 2018 },
      { id: 'unknownprovider:7', provider: 'unknownprovider', providerKey: 'unknownprovider', remoteId: '7', title: 'Solo Leveling (WN)', coverUrl: '', year: undefined },
    ])
  })

  it('search(q, providers) CSV-joins the providers param', async () => {
    const { search } = useMetadata('series-1')

    await search('Solo Leveling', ['anilist', 'mangadex'])

    expect(calls).toEqual([{
      method: 'GET',
      path: '/api/metadata/search',
      query: { q: 'Solo Leveling', providers: 'anilist,mangadex' },
    }])
  })

  it('search() failure clears candidates and sets searchError', async () => {
    nextSearchOk = false
    const { candidates, search, searchError } = useMetadata('series-1')

    await search('Solo Leveling')

    expect(candidates.value).toEqual([])
    expect(searchError.value).toBe('search failed')
  })

  it('identify(provider, remoteId) POSTs the exact body and resolves the raw DTO', async () => {
    const { identify, identifyError, identifying } = useMetadata('series-1')

    const pending = identify('anilist', '105398')
    expect(identifying.value).toBe(true)
    const result = await pending
    expect(identifying.value).toBe(false)

    expect(calls).toEqual([{
      method: 'POST',
      path: '/api/series/{id}/metadata/identify',
      body: { provider: 'anilist', remoteId: '105398' },
    }])
    expect(result).toEqual(seriesDetailStub)
    expect(identifyError.value).toBeNull()
  })

  it('identify() failure sets identifyError and resolves null', async () => {
    nextIdentifyOk = false
    const { identify, identifyError } = useMetadata('series-1')

    const result = await identify('anilist', '105398')

    expect(result).toBeNull()
    expect(identifyError.value).toBe('identify failed')
  })

  it('loadCovers() GETs the series cover-candidate gallery and maps sourceKind/sourceRef', async () => {
    const { coverCandidates, loadCovers, coversError } = useMetadata('series-1')

    await loadCovers()

    expect(calls).toEqual([{ method: 'GET', path: '/api/series/{id}/metadata/covers', query: undefined }])
    expect(coverCandidates.value).toEqual([
      { id: 'metadata:anilist:https://x/anilist-cover.jpg', provider: 'AniList', coverUrl: 'https://x/anilist-cover.jpg', sourceKind: 'metadata', sourceRef: 'anilist' },
      { id: 'metadata:anilist:https://x/anilist-cover-2.jpg', provider: 'AniList', coverUrl: 'https://x/anilist-cover-2.jpg', sourceKind: 'metadata', sourceRef: 'anilist' },
      { id: 'source:prov-1:https://x/source-cover.jpg', provider: 'Asura Scans', coverUrl: 'https://x/source-cover.jpg', sourceKind: 'source', sourceRef: 'prov-1' },
    ])
    expect(coversError.value).toBeNull()
  })

  it('loadCovers() gives two covers from the SAME provider DIFFERENT ids (BUG-1 regression guard)', async () => {
    const { coverCandidates, loadCovers } = useMetadata('series-1')

    await loadCovers()

    const anilistCandidates = coverCandidates.value.filter((c) => c.sourceKind === 'metadata' && c.sourceRef === 'anilist')
    expect(anilistCandidates).toHaveLength(2)
    const ids = anilistCandidates.map((c) => c.id)
    expect(new Set(ids).size).toBe(ids.length)
  })

  it('loadCovers() failure clears coverCandidates and sets coversError', async () => {
    nextCoversOk = false
    const { coverCandidates, loadCovers, coversError } = useMetadata('series-1')

    await loadCovers()

    expect(coverCandidates.value).toEqual([])
    expect(coversError.value).toBe('covers failed')
  })

  it('setCover(sourceKind, sourceRef, coverUrl) POSTs the exact body and resolves the raw DTO', async () => {
    const { setCover, setCoverError, settingCover } = useMetadata('series-1')

    const pending = setCover('metadata', 'anilist', 'https://x/anilist-cover.jpg')
    expect(settingCover.value).toBe(true)
    const result = await pending
    expect(settingCover.value).toBe(false)

    expect(calls).toEqual([{
      method: 'POST',
      path: '/api/series/{id}/cover',
      body: { sourceKind: 'metadata', sourceRef: 'anilist', coverUrl: 'https://x/anilist-cover.jpg' },
    }])
    expect(result).toEqual(seriesDetailStub)
    expect(setCoverError.value).toBeNull()
  })

  it('setCover() failure sets setCoverError and resolves null', async () => {
    nextSetCoverOk = false
    const { setCover, setCoverError } = useMetadata('series-1')

    const result = await setCover('metadata', 'anilist', 'https://x/anilist-cover.jpg')

    expect(result).toBeNull()
    expect(setCoverError.value).toBe('set cover failed')
  })
})
