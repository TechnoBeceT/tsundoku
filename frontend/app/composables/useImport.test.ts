/**
 * useImport — data layer for the Import / Adopt wizard (Screen G).
 *
 * Pins:
 *   1. search() discards a stale (superseded) response — the generation-counter
 *      guard mirrors the identical fix in useMatchSource.search() /
 *      useScanLibrary.match(): if the owner edits the query and re-searches
 *      before the previous request resolves, a slower earlier response must
 *      NOT clobber a faster later one.
 *
 * vi.mock is hoisted by Vitest's transform so the apiClient mock is in place
 * before useImport.ts is evaluated, regardless of import order here.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { apiClient } from '~/utils/api/client'
import { useImport } from './useImport'

interface Call { method: string, path: string, query?: unknown, params?: unknown }
let calls: Call[] = []

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string, opts?: { params?: { query?: Record<string, unknown>, path?: Record<string, unknown> } }) => {
      calls.push({ method: 'GET', path, query: opts?.params?.query, params: opts?.params?.path })
      return Promise.resolve({ data: null, error: null, response: new Response(null, { status: 200 }) })
    }),
    POST: vi.fn().mockImplementation((path: string) => {
      calls.push({ method: 'POST', path })
      return Promise.resolve({ data: null, error: null, response: new Response(null, { status: 200 }) })
    }),
    PATCH: vi.fn(),
    DELETE: vi.fn(),
    use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

describe('useImport', () => {
  beforeEach(() => {
    calls = []
  })

  it('search() discards a stale response when an earlier (slower) request resolves after a later (faster) one', async () => {
    // The owner searches "naruto" (slow), then edits the box and searches
    // "one piece" (fast) before "naruto"'s response lands. Without the
    // generation guard, "naruto"'s late response would silently overwrite
    // `searchResults` even though the box reads "one piece" — letting the
    // owner adopt a candidate from the WRONG query. Control the resolution
    // order with deferred promises: the SECOND (later) call resolves FIRST.
    interface DeferredGetResult { data: unknown, error: unknown, response: Response }
    let resolveNaruto!: (v: DeferredGetResult) => void
    let resolveOnePiece!: (v: DeferredGetResult) => void
    const responseNaruto = new Promise<DeferredGetResult>((resolve) => { resolveNaruto = resolve })
    const responseOnePiece = new Promise<DeferredGetResult>((resolve) => { resolveOnePiece = resolve })

    // Route by query.q (not call order) — useImport's bootstrap also fires
    // GET /api/sources + GET /api/categories, which would otherwise consume
    // mockImplementationOnce slots meant for the two search() calls.
    vi.mocked(apiClient.GET).mockImplementation((path: string, opts?: { params?: { query?: Record<string, unknown> } }) => {
      calls.push({ method: 'GET', path, query: opts?.params?.query })
      if (path === '/api/search') {
        const q = opts?.params?.query?.q
        if (q === 'naruto') return responseNaruto
        if (q === 'one piece') return responseOnePiece
      }
      return Promise.resolve({ data: null, error: null, response: new Response(null, { status: 200 }) })
    })

    const { searchResults, error, search } = useImport()

    const searchNaruto = search({ q: 'naruto', sources: [] }) // slow, started first
    const searchOnePiece = search({ q: 'one piece', sources: [] }) // fast, started second

    // The LATER request ("one piece") resolves FIRST.
    resolveOnePiece({
      data: [{ title: 'One Piece', candidates: [] }],
      error: null,
      response: new Response(null, { status: 200 }),
    })
    await searchOnePiece

    expect(searchResults.value).toEqual([{ title: 'One Piece', candidates: [] }])

    // The EARLIER request ("naruto") finally resolves AFTER "one piece"
    // already landed — its response must be discarded, not overwrite
    // searchResults.
    resolveNaruto({
      data: [{ title: 'Naruto', candidates: [] }],
      error: null,
      response: new Response(null, { status: 200 }),
    })
    await searchNaruto

    expect(searchResults.value).toEqual([{ title: 'One Piece', candidates: [] }])
    expect(error.value).toBe('')
  })

  it('maps the degraded/degradedReason flags from GET /api/sources through to the filter list', async () => {
    // The picker must carry the backend's per-source degraded hint so the chip
    // row can mark a cooling-down source; a healthy source carries the flags
    // false/"" verbatim (never dropped in the mapper).
    vi.mocked(apiClient.GET).mockImplementation((path: string) => {
      calls.push({ method: 'GET', path })
      if (path === '/api/sources') {
        return Promise.resolve({
          data: [
            { id: '1', name: 'MangaDex', lang: 'en', degraded: false, degradedReason: '' },
            { id: '2', name: 'Asura Scans', lang: 'en', degraded: true, degradedReason: 'Temporarily unavailable — 4 consecutive failures' },
          ],
          error: null,
          response: new Response(null, { status: 200 }),
        })
      }
      return Promise.resolve({ data: null, error: null, response: new Response(null, { status: 200 }) })
    })

    const { sources } = useImport()
    await vi.waitFor(() => expect(sources.value).toHaveLength(2))

    expect(sources.value[0]).toEqual({ id: '1', name: 'MangaDex', lang: 'en', degraded: false, degradedReason: '' })
    expect(sources.value[1]).toEqual({ id: '2', name: 'Asura Scans', lang: 'en', degraded: true, degradedReason: 'Temporarily unavailable — 4 consecutive failures' })
  })
})

describe('useImport — inspect (Stage 2 chapter-count preview)', () => {
  it('inspect({source, mangaId, url}) GETs the chapters endpoint with ?url= (P2 Suwayomi-removal — the backend 400s without it)', async () => {
    // Re-assert the default GET mock explicitly (self-contained — an earlier
    // test in this file overrides apiClient.GET's mockImplementation and
    // there is no restoreMocks/clearMocks between tests, so this test cannot
    // rely on the module-level default still being in effect by file order).
    vi.mocked(apiClient.GET).mockImplementation((path: string, opts?: { params?: { query?: Record<string, unknown>, path?: Record<string, unknown> } }) => {
      calls.push({ method: 'GET', path, query: opts?.params?.query, params: opts?.params?.path })
      return Promise.resolve({ data: null, error: null, response: new Response(null, { status: 200 }) })
    })

    const { inspect } = useImport()
    calls = []

    await inspect({ source: 'src-1', mangaId: 42, url: 'https://mangadex.org/title/42' })

    const inspectCalls = calls.filter(c => c.path === '/api/sources/{sourceId}/manga/{mangaId}/chapters')
    expect(inspectCalls).toContainEqual(expect.objectContaining({
      query: { url: 'https://mangadex.org/title/42' },
      params: { sourceId: 'src-1', mangaId: 42 },
    }))
  })
})

describe('useImport — loadBreakdowns (per-scanlator auto-split fetch)', () => {
  it('fetches every candidate in parallel and maps the DTO scanlators onto the screen type, keyed by source:mangaId', async () => {
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
    vi.mocked(apiClient.GET).mockImplementation((path: string, opts?: { params?: { path?: { sourceId: string, mangaId: number }, query?: { url?: string } } }) => {
      calls.push({ method: 'GET', path, query: opts?.params?.query, params: opts?.params?.path })
      if (path === '/api/sources/{sourceId}/manga/{mangaId}/breakdown') {
        return breakdownGet(opts!.params!.path!.sourceId)
      }
      return Promise.resolve({ data: null, error: null, response: new Response(null, { status: 200 }) })
    })

    const { breakdowns, loadBreakdowns } = useImport()
    await loadBreakdowns([
      { source: 'src-1', mangaId: 1, url: 'https://src-1.example/title/1' },
      { source: 'src-2', mangaId: 2, url: 'https://src-2.example/title/2' },
    ])

    expect(breakdownGet).toHaveBeenCalledTimes(2)
    expect(breakdowns.value['src-1:1']).toEqual([
      { scanlator: 'ZScans', count: 90, ranges: '1-90' },
      { scanlator: 'HiveToons', count: 11, ranges: '92-101' },
    ])
    expect(breakdowns.value['src-2:2']).toEqual([{ scanlator: 'src-2', count: 12, ranges: '1-12' }])
    // Every breakdown fetch carries the candidate's url query (P2 Suwayomi-removal —
    // the backend 400s without it).
    const breakdownCalls = calls.filter(c => c.path === '/api/sources/{sourceId}/manga/{mangaId}/breakdown')
    expect(breakdownCalls).toContainEqual(expect.objectContaining({ query: { url: 'https://src-1.example/title/1' } }))
    expect(breakdownCalls).toContainEqual(expect.objectContaining({ query: { url: 'https://src-2.example/title/2' } }))
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

    const { loadBreakdowns } = useImport()
    const candidate = { source: 'src-1', mangaId: 1, url: 'https://src-1.example/title/1' }
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

    const { breakdowns, error, loadBreakdowns } = useImport()
    const candidate = { source: 'src-1', mangaId: 1, url: 'https://src-1.example/title/1' }
    await loadBreakdowns([candidate])

    expect(breakdowns.value['src-1:1']).toBeNull()
    expect(error.value).toBe('')

    await loadBreakdowns([candidate])
    expect(breakdownGet).toHaveBeenCalledTimes(1)
  })
})
