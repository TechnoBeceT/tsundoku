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

interface Call { method: string, path: string, query?: unknown }
let calls: Call[] = []

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string, opts?: { params?: { query?: Record<string, unknown> } }) => {
      calls.push({ method: 'GET', path, query: opts?.params?.query })
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
})
