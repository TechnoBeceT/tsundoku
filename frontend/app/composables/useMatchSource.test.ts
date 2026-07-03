/**
 * useMatchSource — data layer for the Series-Detail "Match source" dialog.
 *
 * Pins:
 *   1. search(q) GETs /api/search?q= and maps the response via the shared
 *      importMappers `mapGroup` (the SAME DTO the Import/Adopt wizard uses).
 *   2. search() failure sets `error` and leaves `groups` empty, never throws.
 *   3. addProvider(payload) POSTs /api/series/{id}/providers with the exact
 *      {source, mangaId, importance} body and resolves the fresh SeriesDetail.
 *   4. addProvider() failure sets `error` and resolves null (the caller decides
 *      whether to close the dialog based on that null).
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
let nextAddOk = true

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string, opts?: { params?: { query?: Record<string, unknown> } }) => {
      calls.push({ method: 'GET', path, query: opts?.params?.query })
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
      if (path === '/api/series/{id}/providers') {
        if (!nextAddOk) {
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
    nextAddOk = true
  })

  it('search(q) GETs /api/search with q and maps the response into groups', async () => {
    const { groups, search } = useMatchSource('series-1')

    await search('Solo Leveling')

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

    const searchNaruto = search('naruto') // slow, started first
    const searchOnePiece = search('one piece') // fast, started second

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

    await search('Solo Leveling')

    expect(error.value).toBe('search failed')
    expect(groups.value).toEqual([])
  })

  it('addProvider(payload) POSTs /api/series/{id}/providers with the exact body and resolves the fresh detail', async () => {
    const { addProvider } = useMatchSource('series-1')

    const result = await addProvider({ source: 'src-1', mangaId: 42, importance: 5 })

    const postCall = calls.find(c => c.path === '/api/series/{id}/providers')
    expect(postCall).toBeDefined()
    expect(postCall!.body).toEqual({ source: 'src-1', mangaId: 42, importance: 5 })
    expect(result).toEqual({ id: 'series-1', displayName: 'Solo Leveling' })
  })

  it('addProvider() failure sets error and resolves null', async () => {
    nextAddOk = false
    const { error, addProvider } = useMatchSource('series-1')

    const result = await addProvider({ source: 'src-1', mangaId: 42, importance: 5 })

    expect(result).toBeNull()
    expect(error.value).toBe('add failed')
  })

  it('saving flips true during addProvider and back to false once it resolves', async () => {
    const { saving, addProvider } = useMatchSource('series-1')
    expect(saving.value).toBe(false)

    const promise = addProvider({ source: 'src-1', mangaId: 42, importance: 5 })
    expect(saving.value).toBe(true)
    await promise

    expect(saving.value).toBe(false)
  })
})
