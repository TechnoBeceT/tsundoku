/**
 * useMatchDiskProvider — search + breakdown data layer for the "Match to
 * source" dialog.
 *
 * Pins:
 *   0. loadSources() GETs /api/sources once (guarded), maps via mapSource, and
 *      never re-fetches on a second call.
 *   1. search({q, sources}) GETs /api/search?q=&sources= and maps the response
 *      via the shared importMappers `mapGroup` (same DTO every search surface
 *      uses); sources is CSV-joined when set and omitted when the list is empty.
 *   2. search()'s stale-response guard: a slower, earlier request must never
 *      overwrite `groups` after a faster, later one already landed.
 *   3. search() failure sets `error` and leaves `groups` empty, never throws.
 *   4. loadBreakdown(source, mangaId, url) GETs the breakdown endpoint with
 *      `?url=` and maps `scanlators` via `mapScanlatorCoverage`.
 *   5. loadBreakdown() failure resolves `breakdown` to null WITHOUT touching
 *      `error` (informational coverage, not a hard match failure) and never
 *      throws.
 *   6. breakdownLoading flips true during the call and back to false once it
 *      resolves, win or lose.
 *
 * vi.mock is hoisted by Vitest's transform so the apiClient mock is in place
 * before useMatchDiskProvider.ts is evaluated, regardless of import order.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { apiClient } from '~/utils/api/client'
import { useMatchDiskProvider } from './useMatchDiskProvider'

interface Call { method: string, path: string, query?: unknown, params?: unknown }
let calls: Call[] = []
let nextSearchOk = true
let nextBreakdownOk = true

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string, opts?: { params?: { query?: Record<string, unknown>, path?: Record<string, unknown> } }) => {
      calls.push({ method: 'GET', path, query: opts?.params?.query, params: opts?.params?.path })
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
      if (path === '/api/sources/{sourceId}/manga/{mangaId}/breakdown') {
        if (!nextBreakdownOk) {
          return Promise.resolve({ data: null, error: { message: 'breakdown failed' }, response: new Response(null, { status: 502 }) })
        }
        return Promise.resolve({
          data: {
            total: 90,
            scanlators: [
              { scanlator: 'Reset Scans', count: 60, ranges: '1-60' },
              { scanlator: 'Asura Scans', count: 30, ranges: '61-90' },
            ],
          },
          error: null,
          response: new Response(null, { status: 200 }),
        })
      }
      return Promise.resolve({ data: null, error: null, response: new Response(null, { status: 200 }) })
    }),
    POST: vi.fn(),
    PATCH: vi.fn(),
    DELETE: vi.fn(),
    use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

describe('useMatchDiskProvider', () => {
  beforeEach(() => {
    calls = []
    nextSearchOk = true
    nextBreakdownOk = true
  })

  it('loadSources() GETs /api/sources once, maps it, and never re-fetches on a second call', async () => {
    const { sources, loadSources } = useMatchDiskProvider()

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
    const { search } = useMatchDiskProvider()

    await search({ q: 'x', sources: ['a', 'b'] })

    expect(calls).toContainEqual({ method: 'GET', path: '/api/search', query: { q: 'x', sources: 'a,b' }, params: undefined })
  })

  it('search({q, sources}) omits the sources param when the list is empty', async () => {
    const { search } = useMatchDiskProvider()

    await search({ q: 'x', sources: [] })

    expect(calls).toContainEqual({ method: 'GET', path: '/api/search', query: { q: 'x' }, params: undefined })
  })

  it('search({q, sources}) GETs /api/search with q and maps the response into groups', async () => {
    const { groups, search } = useMatchDiskProvider()

    await search({ q: 'Solo Leveling', sources: [] })

    expect(calls).toContainEqual({ method: 'GET', path: '/api/search', query: { q: 'Solo Leveling' }, params: undefined })
    expect(groups.value).toEqual([
      {
        title: 'Solo Leveling',
        candidates: [{
          source: 'src-1',
          sourceName: 'MangaDex',
          lang: 'en',
          mangaId: 42,
          title: 'Solo Leveling',
          thumbnailUrl: `/api/sources/src-1/cover?url=${encodeURIComponent('https://example.com/thumb.jpg')}`,
          url: 'https://mangadex.org/title/42',
        }],
      },
    ])
  })

  it('search() discards a stale response when an earlier (slower) request resolves after a later (faster) one', async () => {
    interface DeferredGetResult { data: unknown, error: unknown, response: Response }
    let resolveNaruto!: (v: DeferredGetResult) => void
    let resolveOnePiece!: (v: DeferredGetResult) => void
    const responseNaruto = new Promise<DeferredGetResult>((resolve) => { resolveNaruto = resolve })
    const responseOnePiece = new Promise<DeferredGetResult>((resolve) => { resolveOnePiece = resolve })

    vi.mocked(apiClient.GET)
      .mockImplementationOnce(() => responseNaruto)
      .mockImplementationOnce(() => responseOnePiece)

    const { groups, error, search } = useMatchDiskProvider()

    const searchNaruto = search({ q: 'naruto', sources: [] }) // slow, started first
    const searchOnePiece = search({ q: 'one piece', sources: [] }) // fast, started second

    resolveOnePiece({
      data: [{ title: 'One Piece', candidates: [] }],
      error: null,
      response: new Response(null, { status: 200 }),
    })
    await searchOnePiece

    expect(groups.value).toEqual([{ title: 'One Piece', candidates: [] }])

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
    const { groups, error, search } = useMatchDiskProvider()

    await search({ q: 'Solo Leveling', sources: [] })

    expect(error.value).toBe('search failed')
    expect(groups.value).toEqual([])
  })

  it('loadBreakdown(source, mangaId, url) GETs the breakdown endpoint with ?url= and maps scanlators', async () => {
    const { breakdown, loadBreakdown } = useMatchDiskProvider()

    await loadBreakdown('src-1', 42, 'https://mangadex.org/title/42')

    expect(calls).toContainEqual({
      method: 'GET',
      path: '/api/sources/{sourceId}/manga/{mangaId}/breakdown',
      query: { url: 'https://mangadex.org/title/42' },
      params: { sourceId: 'src-1', mangaId: 42 },
    })
    expect(breakdown.value).toEqual([
      { scanlator: 'Reset Scans', count: 60, ranges: '1-60' },
      { scanlator: 'Asura Scans', count: 30, ranges: '61-90' },
    ])
  })

  it('loadBreakdown() failure resolves breakdown to null without touching error', async () => {
    nextBreakdownOk = false
    const { breakdown, error, loadBreakdown } = useMatchDiskProvider()

    await loadBreakdown('src-1', 42, 'https://mangadex.org/title/42')

    expect(breakdown.value).toBeNull()
    expect(error.value).toBeNull()
  })

  it('breakdownLoading flips true during loadBreakdown and back to false once it resolves', async () => {
    const { breakdownLoading, loadBreakdown } = useMatchDiskProvider()
    expect(breakdownLoading.value).toBe(false)

    const promise = loadBreakdown('src-1', 42, 'https://mangadex.org/title/42')
    expect(breakdownLoading.value).toBe(true)
    await promise

    expect(breakdownLoading.value).toBe(false)
  })
})
