/**
 * useMatchSource — data layer for the Series-Detail "Add a source" dialog.
 *
 * Pins:
 *   1. search({q, sources}) GETs /api/search?q=&sources= and maps the response
 *      via the shared importMappers `mapGroup` (the SAME DTO the Import/Adopt
 *      wizard uses); the sources param is CSV-joined when set and omitted when
 *      the list is empty (mirrors useImport.search).
 *   0. loadSources() GETs /api/sources once (guarded), maps via mapSource, and
 *      never re-fetches on a second call.
 *   2. search() failure sets `error` and leaves `groups` empty, never throws.
 *   3. loadBreakdowns(candidates) fetches every candidate's per-scanlator
 *      breakdown in parallel, caches by `source:mangaId`, and never touches
 *      `error` on a per-candidate failure (mirrors `useImport.loadBreakdowns`).
 *   4. batchAddProviders(providers) POSTs /api/series/{id}/providers/batch
 *      with the exact {providers} body and resolves the fresh SeriesDetail
 *      (Slice P — the batch counterpart of the retired single `addProvider`).
 *   5. batchAddProviders() failure sets `error` and resolves null (the caller
 *      decides whether to close the dialog based on that null).
 *
 * vi.mock is hoisted by Vitest's transform so the apiClient mock is in place
 * before useMatchSource.ts is evaluated, regardless of import order here.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { apiClient } from '~/utils/api/client'
import { useMatchSource } from './useMatchSource'

interface Call { method: string, path: string, body?: unknown, query?: unknown }
let calls: Call[] = []

let nextSearchOk = true
let nextBatchAddOk = true

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string, opts?: { params?: { query?: Record<string, unknown> } }) => {
      calls.push({ method: 'GET', path, query: opts?.params?.query })
      if (path === '/api/sources') {
        return Promise.resolve({
          data: [
            { id: 'src-1', name: 'MangaDex', lang: 'en' },
            { id: 'src-2', name: 'Asura Scans', lang: 'en' },
          ],
          error: null,
          response: new Response(null, { status: 200 }),
        })
      }
      if (path === '/api/search') {
        if (!nextSearchOk) {
          return Promise.resolve({ data: null, error: { message: 'search failed' }, response: new Response(null, { status: 500 }) })
        }
        return Promise.resolve({
          data: [{
            title: 'Solo Leveling',
            candidates: [{
              source: 'src-1',
              sourceName: 'MangaDex',
              lang: 'en',
              mangaId: 42,
              title: 'Solo Leveling',
              url: 'https://mangadex.org/title/42',
              thumbnailUrl: 'https://example.com/thumb.jpg',
              author: '',
              artist: '',
              description: '',
              genres: [],
            }],
          }],
          error: null,
          response: new Response(null, { status: 200 }),
        })
      }
      return Promise.resolve({ data: null, error: null, response: new Response(null, { status: 200 }) })
    }),
    POST: vi.fn().mockImplementation((path: string, opts?: { params?: { path?: Record<string, unknown> }, body?: unknown }) => {
      calls.push({ method: 'POST', path, body: opts?.body })
      if (path === '/api/series/{id}/providers/batch') {
        if (!nextBatchAddOk) {
          return Promise.resolve({ data: null, error: { message: 'add failed' }, response: new Response(null, { status: 409 }) })
        }
        return Promise.resolve({
          data: { id: 'series-1', displayName: 'Solo Leveling' },
          error: null,
          response: new Response(null, { status: 200 }),
        })
      }
      return Promise.resolve({ data: null, error: null, response: new Response(null, { status: 200 }) })
    }),
    PATCH: vi.fn(),
    DELETE: vi.fn(),
    use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

describe('useMatchSource', () => {
  beforeEach(() => {
    calls = []
    nextSearchOk = true
    nextBatchAddOk = true
  })

  it('loadSources() GETs /api/sources once, maps it, and never re-fetches on a second call', async () => {
    const { sources, loadSources } = useMatchSource('series-1')

    await loadSources()
    expect(sources.value).toEqual([
      { id: 'src-1', name: 'MangaDex', lang: 'en' },
      { id: 'src-2', name: 'Asura Scans', lang: 'en' },
    ])
    expect(calls.filter(c => c.path === '/api/sources')).toHaveLength(1)

    // A second call must be a no-op — the source list is loaded once per composable.
    await loadSources()
    expect(calls.filter(c => c.path === '/api/sources')).toHaveLength(1)
  })

  it('search({q, sources}) CSV-joins the sources param when set', async () => {
    const { search } = useMatchSource('series-1')

    await search({ q: 'x', sources: ['a', 'b'] })

    expect(calls).toContainEqual({ method: 'GET', path: '/api/search', query: { q: 'x', sources: 'a,b' } })
  })

  it('search({q, sources}) omits the sources param when the list is empty', async () => {
    const { search } = useMatchSource('series-1')

    await search({ q: 'x', sources: [] })

    expect(calls).toContainEqual({ method: 'GET', path: '/api/search', query: { q: 'x' } })
  })

  it('search({q, sources}) GETs /api/search with q and maps the response into groups', async () => {
    const { groups, search } = useMatchSource('series-1')

    await search({ q: 'Solo Leveling', sources: [] })

    expect(calls).toContainEqual({ method: 'GET', path: '/api/search', query: { q: 'Solo Leveling' } })
    expect(groups.value).toEqual([
      {
        title: 'Solo Leveling',
        candidates: [{
          source: 'src-1',
          sourceName: 'MangaDex',
          lang: 'en',
          mangaId: 42,
          title: 'Solo Leveling',
          thumbnailUrl: 'https://example.com/thumb.jpg',
        }],
      },
    ])
  })

  it('search() discards a stale response when an earlier (slower) request resolves after a later (faster) one', async () => {
    // The owner searches "naruto" (slow), then edits the box and searches
    // "one piece" (fast) before "naruto"'s response lands. Without the
    // generation guard, "naruto"'s late response would silently overwrite
    // `groups` even though the box reads "one piece" — letting the owner
    // attach a candidate from the WRONG query. Control the resolution order
    // with deferred promises: the SECOND (later) call resolves FIRST.
    interface DeferredGetResult { data: unknown, error: unknown, response: Response }
    let resolveNaruto!: (v: DeferredGetResult) => void
    let resolveOnePiece!: (v: DeferredGetResult) => void
    const responseNaruto = new Promise<DeferredGetResult>((resolve) => { resolveNaruto = resolve })
    const responseOnePiece = new Promise<DeferredGetResult>((resolve) => { resolveOnePiece = resolve })

    vi.mocked(apiClient.GET)
      .mockImplementationOnce(() => responseNaruto)
      .mockImplementationOnce(() => responseOnePiece)

    const { groups, error, search } = useMatchSource('series-1')

    const searchNaruto = search({ q: 'naruto', sources: [] }) // slow, started first
    const searchOnePiece = search({ q: 'one piece', sources: [] }) // fast, started second

    // The LATER request ("one piece") resolves FIRST.
    resolveOnePiece({
      data: [{ title: 'One Piece', candidates: [] }],
      error: null,
      response: new Response(null, { status: 200 }),
    })
    await searchOnePiece

    expect(groups.value).toEqual([{ title: 'One Piece', candidates: [] }])

    // The EARLIER request ("naruto") finally resolves AFTER "one piece"
    // already landed — its response must be discarded, not overwrite groups.
    resolveNaruto({
      data: [{ title: 'Naruto', candidates: [] }],
      error: null,
      response: new Response(null, { status: 200 }),
    })
    await searchNaruto

    expect(groups.value).toEqual([{ title: 'One Piece', candidates: [] }])
    expect(error.value).toBeNull()
  })

  it('search() failure sets error and leaves groups empty', async () => {
    nextSearchOk = false
    const { groups, error, search } = useMatchSource('series-1')

    await search({ q: 'Solo Leveling', sources: [] })

    expect(error.value).toBe('search failed')
    expect(groups.value).toEqual([])
  })

  describe('loadBreakdowns (per-scanlator auto-split fetch, copied from useImport)', () => {
    it('fetches every candidate in parallel and caches the mapped scanlators, keyed by source:mangaId', async () => {
      const breakdownGet = vi.fn((sourceId: string) => {
        if (sourceId === 'src-1') {
          return Promise.resolve({
            data: {
              total: 101,
              scanlators: [
                { scanlator: 'ZScans', count: 90, ranges: '1-90' },
                { scanlator: 'HiveToons', count: 11, ranges: '92-101' },
              ],
            },
            error: null,
          })
        }
        return Promise.resolve({
          data: { total: 12, scanlators: [{ scanlator: 'src-2', count: 12, ranges: '1-12' }] },
          error: null,
        })
      })
      vi.mocked(apiClient.GET).mockImplementation((path: string, opts?: { params?: { path?: { sourceId: string, mangaId: number } } }) => {
        calls.push({ method: 'GET', path })
        if (path === '/api/sources/{sourceId}/manga/{mangaId}/breakdown') {
          return breakdownGet(opts!.params!.path!.sourceId)
        }
        return Promise.resolve({ data: null, error: null, response: new Response(null, { status: 200 }) })
      })

      const { breakdowns, loadBreakdowns } = useMatchSource('series-1')
      await loadBreakdowns([
        { source: 'src-1', mangaId: 1 } as never,
        { source: 'src-2', mangaId: 2 } as never,
      ])

      expect(breakdownGet).toHaveBeenCalledTimes(2)
      expect(breakdowns.value['src-1:1']).toEqual([
        { scanlator: 'ZScans', count: 90, ranges: '1-90' },
        { scanlator: 'HiveToons', count: 11, ranges: '92-101' },
      ])
      expect(breakdowns.value['src-2:2']).toEqual([{ scanlator: 'src-2', count: 12, ranges: '1-12' }])
    })

    it('caches by source:mangaId — a second loadBreakdowns call for an already-loaded candidate does not re-fetch', async () => {
      const breakdownGet = vi.fn(() => Promise.resolve({
        data: { total: 12, scanlators: [{ scanlator: 'src-1', count: 12, ranges: '1-12' }] },
        error: null,
      }))
      vi.mocked(apiClient.GET).mockImplementation((path: string) => {
        calls.push({ method: 'GET', path })
        if (path === '/api/sources/{sourceId}/manga/{mangaId}/breakdown') return breakdownGet()
        return Promise.resolve({ data: null, error: null, response: new Response(null, { status: 200 }) })
      })

      const { loadBreakdowns } = useMatchSource('series-1')
      const candidate = { source: 'src-1', mangaId: 1 } as never
      await loadBreakdowns([candidate])
      expect(breakdownGet).toHaveBeenCalledTimes(1)

      await loadBreakdowns([candidate])
      expect(breakdownGet).toHaveBeenCalledTimes(1)
    })

    it('caches a failed fetch as null (non-fatal — never touches `error`) and never retries it', async () => {
      const breakdownGet = vi.fn(() => Promise.resolve({ data: null, error: { message: 'upstream failure' } }))
      vi.mocked(apiClient.GET).mockImplementation((path: string) => {
        calls.push({ method: 'GET', path })
        if (path === '/api/sources/{sourceId}/manga/{mangaId}/breakdown') return breakdownGet()
        return Promise.resolve({ data: null, error: null, response: new Response(null, { status: 200 }) })
      })

      const { breakdowns, error, loadBreakdowns } = useMatchSource('series-1')
      const candidate = { source: 'src-1', mangaId: 1 } as never
      await loadBreakdowns([candidate])

      expect(breakdowns.value['src-1:1']).toBeNull()
      expect(error.value).toBeNull()

      await loadBreakdowns([candidate])
      expect(breakdownGet).toHaveBeenCalledTimes(1)
    })
  })

  describe('batchAddProviders (Slice P batch attach)', () => {
    it('POSTs /api/series/{id}/providers/batch with the exact {providers} body and resolves the fresh detail', async () => {
      const { batchAddProviders } = useMatchSource('series-1')

      const providers = [
        { source: 'src-1', mangaId: 42, scanlator: '' },
        { source: 'src-2', mangaId: 7, scanlator: 'Asura Scans' },
      ]
      const result = await batchAddProviders(providers)

      const postCall = calls.find(c => c.path === '/api/series/{id}/providers/batch')
      expect(postCall).toBeDefined()
      expect(postCall!.body).toEqual({ providers })
      expect(result).toEqual({ id: 'series-1', displayName: 'Solo Leveling' })
    })

    it('failure sets error and resolves null', async () => {
      nextBatchAddOk = false
      const { error, batchAddProviders } = useMatchSource('series-1')

      const result = await batchAddProviders([{ source: 'src-1', mangaId: 42, scanlator: '' }])

      expect(result).toBeNull()
      expect(error.value).toBe('add failed')
    })

    it('saving flips true during batchAddProviders and back to false once it resolves', async () => {
      const { saving, batchAddProviders } = useMatchSource('series-1')
      expect(saving.value).toBe(false)

      const promise = batchAddProviders([{ source: 'src-1', mangaId: 42, scanlator: '' }])
      expect(saving.value).toBe(true)
      await promise

      expect(saving.value).toBe(false)
    })
  })
})
