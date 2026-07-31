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
 *      breakdown in parallel (each fetch carrying the candidate's `?url=`,
 *      required by the backend), caches by `source:mangaId`, and never
 *      touches `error` on a per-candidate failure (mirrors
 *      `useScanLibrary.loadBreakdowns`).
 *   4. batchAddProviders(providers) POSTs /api/series/{id}/providers/batch
 *      with the exact {providers} body and resolves the fresh SeriesDetail
 *      (Slice P — the batch counterpart of the retired single `addProvider`).
 *   5. batchAddProviders() failure sets `error` and resolves null (the caller
 *      decides whether to close the dialog based on that null).
 *   6. GAP-140: loadBreakdowns tracks the breakdown snapshot's own lifecycle
 *      (`breakdownSnapshots`) instead of collapsing pending/failed to `null`,
 *      a pending row updates itself in place when `imports.coverage.done`
 *      lands (matched by source+url, not the mangaId cache key), and
 *      refreshBreakdown(candidate) forces `?refresh=true` (a no-op while a
 *      fetch for that candidate is already in flight).
 *
 * Uses the same FakeEventSource stub as useScanLibrary.test.ts /
 * useImport.test.ts so the NAMED_EVENTS loop in useProgressStream registers
 * real addEventListener calls our stub can fire through.
 *
 * vi.mock is hoisted by Vitest's transform so the apiClient mock is in place
 * before useMatchSource.ts is evaluated, regardless of import order here.
 */
import { describe, it, expect, vi, beforeAll, beforeEach, afterEach } from 'vitest'
import { defineComponent } from 'vue'
import { mount } from '@vue/test-utils'
import { apiClient } from '~/utils/api/client'
import { useMatchSource } from './useMatchSource'
import { useProgressStream } from './useProgressStream'

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

// ── EventSource stub (mirrors useScanLibrary.test.ts / useImport.test.ts) ────

interface StubSource {
  fire: (name: string, data: unknown) => void
}

let stubSource: StubSource | null = null

class FakeEventSource {
  onopen: ((ev: Event) => void) | null = null
  onerror: ((ev: Event) => void) | null = null

  private _handlers = new Map<string, ((ev: Event) => void)[]>()

  constructor(_url: string) {
    const handlers = this._handlers
    const onOpenRef = () => this.onopen?.(new Event('open'))

    stubSource = {
      fire(name: string, data: unknown) {
        const ev = { data: JSON.stringify(data) } as MessageEvent
        ;(handlers.get(name) ?? []).forEach(h => h(ev))
      },
    }
    queueMicrotask(onOpenRef)
  }

  addEventListener(name: string, handler: (ev: Event) => void): void {
    if (!this._handlers.has(name)) this._handlers.set(name, [])
    this._handlers.get(name)!.push(handler)
  }

  removeEventListener(_name?: string, _handler?: (ev: Event) => void): void { void 0 }
  close(): void { stubSource = null }
}

// ── Per-test isolation harness — useMatchSource registers onUnmounted cleanup
// (its imports.coverage.done subscription), so it is mounted inside a real
// component instance (mirrors useScanLibrary.test.ts's mountScanLibrary). ────

type MatchSourceApi = ReturnType<typeof useMatchSource>

let activeWrapper: ReturnType<typeof mount> | null = null

function mountUseMatchSource(seriesId = 'series-1'): MatchSourceApi {
  let api!: MatchSourceApi
  const Harness = defineComponent({
    setup() {
      api = useMatchSource(seriesId)
      return () => null
    },
  })
  activeWrapper = mount(Harness)
  return api
}

// File-wide hooks (see useImport.test.ts's identical note): declaring these
// OUTSIDE every describe block is what makes them apply to every test in this
// file, including the nested `describe('loadBreakdowns …')` /
// `describe('batchAddProviders …')` blocks below — otherwise a mounted
// harness from an earlier describe never gets torn down and leaks its SSE
// subscription into later tests that reuse the same candidate identity.
beforeAll(() => {
  vi.stubGlobal('EventSource', FakeEventSource)
  useProgressStream().connect()
})

beforeEach(() => {
  calls = []
  nextSearchOk = true
  nextBatchAddOk = true
})

afterEach(() => {
  activeWrapper?.unmount()
  activeWrapper = null
})

describe('useMatchSource', () => {
  it('loadSources() GETs /api/sources once, maps it, and never re-fetches on a second call', async () => {
    const { sources, loadSources } = mountUseMatchSource()

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
    const { search } = mountUseMatchSource()

    await search({ q: 'x', sources: ['a', 'b'] })

    expect(calls).toContainEqual({ method: 'GET', path: '/api/search', query: { q: 'x', sources: 'a,b' } })
  })

  it('search({q, sources}) omits the sources param when the list is empty', async () => {
    const { search } = mountUseMatchSource()

    await search({ q: 'x', sources: [] })

    expect(calls).toContainEqual({ method: 'GET', path: '/api/search', query: { q: 'x' } })
  })

  it('search({q, sources}) GETs /api/search with q and maps the response into groups', async () => {
    const { groups, search } = mountUseMatchSource()

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
          thumbnailUrl: `/api/sources/src-1/cover?url=${encodeURIComponent('https://example.com/thumb.jpg')}`,
          url: 'https://mangadex.org/title/42',
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

    const { groups, error, search } = mountUseMatchSource()

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
    const { groups, error, search } = mountUseMatchSource()

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
              status: 'ready',
            },
            error: null,
          })
        }
        return Promise.resolve({
          data: { total: 12, scanlators: [{ scanlator: 'src-2', count: 12, ranges: '1-12' }], status: 'ready' },
          error: null,
        })
      })
      vi.mocked(apiClient.GET).mockImplementation((path: string, opts?: { params?: { path?: { sourceId: string, mangaId: number }, query?: { url?: string } } }) => {
        calls.push({ method: 'GET', path, query: opts?.params?.query })
        if (path === '/api/sources/{sourceId}/manga/{mangaId}/breakdown') {
          return breakdownGet(opts!.params!.path!.sourceId)
        }
        return Promise.resolve({ data: null, error: null, response: new Response(null, { status: 200 }) })
      })

      const { breakdowns, loadBreakdowns } = mountUseMatchSource()
      await loadBreakdowns([
        { source: 'src-1', mangaId: 1, url: 'https://src-1.example/title/1' } as never,
        { source: 'src-2', mangaId: 2, url: 'https://src-2.example/title/2' } as never,
      ])

      expect(breakdownGet).toHaveBeenCalledTimes(2)
      expect(breakdowns.value['src-1:1']).toEqual([
        { scanlator: 'ZScans', count: 90, ranges: '1-90' },
        { scanlator: 'HiveToons', count: 11, ranges: '92-101' },
      ])
      expect(breakdowns.value['src-2:2']).toEqual([{ scanlator: 'src-2', count: 12, ranges: '1-12' }])
      // Every breakdown fetch carries the candidate's url query (P2 Suwayomi-removal
      // — the backend 400s without it).
      const breakdownCalls = calls.filter(c => c.path === '/api/sources/{sourceId}/manga/{mangaId}/breakdown')
      expect(breakdownCalls).toContainEqual(expect.objectContaining({ query: { url: 'https://src-1.example/title/1' } }))
      expect(breakdownCalls).toContainEqual(expect.objectContaining({ query: { url: 'https://src-2.example/title/2' } }))
    })

    it('caches by source:mangaId — a second loadBreakdowns call for an already-loaded candidate does not re-fetch', async () => {
      const breakdownGet = vi.fn(() => Promise.resolve({
        data: { total: 12, scanlators: [{ scanlator: 'src-1', count: 12, ranges: '1-12' }], status: 'ready' },
        error: null,
      }))
      vi.mocked(apiClient.GET).mockImplementation((path: string) => {
        calls.push({ method: 'GET', path })
        if (path === '/api/sources/{sourceId}/manga/{mangaId}/breakdown') return breakdownGet()
        return Promise.resolve({ data: null, error: null, response: new Response(null, { status: 200 }) })
      })

      const { loadBreakdowns } = mountUseMatchSource()
      const candidate = { source: 'src-1', mangaId: 1, url: 'https://src-1.example/title/1' } as never
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

      const { breakdowns, error, loadBreakdowns } = mountUseMatchSource()
      const candidate = { source: 'src-1', mangaId: 1, url: 'https://src-1.example/title/1' } as never
      await loadBreakdowns([candidate])

      expect(breakdowns.value['src-1:1']).toBeNull()
      expect(error.value).toBeNull()

      await loadBreakdowns([candidate])
      expect(breakdownGet).toHaveBeenCalledTimes(1)
    })

    it('GAP-140: a pending snapshot caches as [] with a pending status, not null — the row must read "Computing coverage…", never "Coverage unavailable"', async () => {
      const breakdownGet = vi.fn(() => Promise.resolve({
        data: { total: 0, scanlators: [], status: 'pending' },
        error: null,
      }))
      vi.mocked(apiClient.GET).mockImplementation((path: string) => {
        calls.push({ method: 'GET', path })
        if (path === '/api/sources/{sourceId}/manga/{mangaId}/breakdown') return breakdownGet()
        return Promise.resolve({ data: null, error: null, response: new Response(null, { status: 200 }) })
      })

      const { breakdowns, breakdownSnapshots, loadBreakdowns } = mountUseMatchSource()
      const candidate = { source: 'src-1', mangaId: 1, url: 'https://src-1.example/title/1' } as never
      await loadBreakdowns([candidate])

      expect(breakdowns.value['src-1:1']).toEqual([])
      expect(breakdownSnapshots.value['src-1:1']).toEqual({ status: 'pending', computedAt: '', error: '' })
    })

    it('GAP-140: a failed snapshot caches as [] with a failed status and its reason', async () => {
      const breakdownGet = vi.fn(() => Promise.resolve({
        data: { total: 0, scanlators: [], status: 'failed', error: 'upstream timed out' },
        error: null,
      }))
      vi.mocked(apiClient.GET).mockImplementation((path: string) => {
        calls.push({ method: 'GET', path })
        if (path === '/api/sources/{sourceId}/manga/{mangaId}/breakdown') return breakdownGet()
        return Promise.resolve({ data: null, error: null, response: new Response(null, { status: 200 }) })
      })

      const { breakdowns, breakdownSnapshots, loadBreakdowns } = mountUseMatchSource()
      const candidate = { source: 'src-1', mangaId: 1, url: 'https://src-1.example/title/1' } as never
      await loadBreakdowns([candidate])

      expect(breakdowns.value['src-1:1']).toEqual([])
      expect(breakdownSnapshots.value['src-1:1']).toEqual({ status: 'failed', computedAt: '', error: 'upstream timed out' })
    })
  })

  describe('imports.coverage.done (GAP-140) — resolving the "permanent cache" problem', () => {
    it('re-fetches the matching candidate (by source+url, not the mangaId cache key) and refreshes both caches in place', async () => {
      const { breakdowns, breakdownSnapshots, loadBreakdowns } = mountUseMatchSource()
      await vi.waitFor(() => expect(stubSource).not.toBeNull())

      vi.mocked(apiClient.GET).mockImplementationOnce((path: string) => {
        calls.push({ method: 'GET', path })
        return Promise.resolve({
          data: { total: 0, scanlators: [], status: 'pending' },
          error: null,
          response: new Response(null, { status: 200 }),
        })
      })
      calls = []
      const candidate = { source: 'src-1', mangaId: 1, url: 'https://src-1.example/title/1' } as never
      await loadBreakdowns([candidate])

      expect(breakdownSnapshots.value['src-1:1']).toEqual({ status: 'pending', computedAt: '', error: '' })

      vi.mocked(apiClient.GET).mockImplementationOnce((path: string) => {
        calls.push({ method: 'GET', path })
        return Promise.resolve({
          data: { total: 12, scanlators: [{ scanlator: 'src-1', count: 12, ranges: '1-12' }], status: 'ready', computedAt: '2026-07-31T09:00:00Z' },
          error: null,
          response: new Response(null, { status: 200 }),
        })
      })
      calls = []
      stubSource!.fire('imports.coverage.done', {
        sourceId: 'src-1',
        mangaUrl: 'https://src-1.example/title/1',
        status: 'ready',
        total: 12,
      })

      await vi.waitFor(() => {
        expect(calls.filter(c => c.path === '/api/sources/{sourceId}/manga/{mangaId}/breakdown').length).toBe(1)
      })
      expect(breakdowns.value['src-1:1']).toEqual([{ scanlator: 'src-1', count: 12, ranges: '1-12' }])
      expect(breakdownSnapshots.value['src-1:1']).toEqual({ status: 'ready', computedAt: '2026-07-31T09:00:00Z', error: '' })
    })

    it('ignores an event for a different (source, url) pair — no extra fetch fires', async () => {
      const { loadBreakdowns } = mountUseMatchSource()
      await vi.waitFor(() => expect(stubSource).not.toBeNull())
      const candidate = { source: 'src-1', mangaId: 1, url: 'https://src-1.example/title/1' } as never
      await loadBreakdowns([candidate])

      calls = []
      stubSource!.fire('imports.coverage.done', {
        sourceId: 'src-2',
        mangaUrl: 'https://src-2.example/title/2',
        status: 'ready',
        total: 5,
      })

      await Promise.resolve()
      expect(calls.filter(c => c.path === '/api/sources/{sourceId}/manga/{mangaId}/breakdown').length).toBe(0)
    })
  })

  describe('refreshBreakdown (GAP-140 follow-up)', () => {
    it('sends ?refresh=true and overwrites both caches with the fresh response', async () => {
      vi.mocked(apiClient.GET).mockImplementation((path: string) => {
        calls.push({ method: 'GET', path })
        if (path === '/api/sources/{sourceId}/manga/{mangaId}/breakdown') {
          return Promise.resolve({
            data: { total: 12, scanlators: [{ scanlator: 'src-1', count: 12, ranges: '1-12' }], status: 'ready', computedAt: '2026-07-30T00:00:00Z' },
            error: null,
          })
        }
        return Promise.resolve({ data: null, error: null, response: new Response(null, { status: 200 }) })
      })

      const { breakdowns, breakdownSnapshots, loadBreakdowns, refreshBreakdown } = mountUseMatchSource()
      const candidate = { source: 'src-1', mangaId: 1, url: 'https://src-1.example/title/1' } as never
      await loadBreakdowns([candidate])
      expect(breakdownSnapshots.value['src-1:1']).toEqual({ status: 'ready', computedAt: '2026-07-30T00:00:00Z', error: '' })

      vi.mocked(apiClient.GET).mockImplementationOnce((path: string, opts?: { params?: { query?: unknown } }) => {
        calls.push({ method: 'GET', path, query: opts?.params?.query })
        return Promise.resolve({
          data: { total: 0, scanlators: [], status: 'pending' },
          error: null,
          response: new Response(null, { status: 200 }),
        })
      })
      calls = []

      await refreshBreakdown(candidate)

      const refreshCall = calls.find(c => c.path === '/api/sources/{sourceId}/manga/{mangaId}/breakdown')
      expect(refreshCall).toBeDefined()
      expect(refreshCall!.query).toEqual({ url: 'https://src-1.example/title/1', refresh: true })
      expect(breakdowns.value['src-1:1']).toEqual([])
      expect(breakdownSnapshots.value['src-1:1']).toEqual({ status: 'pending', computedAt: '', error: '' })
    })

    it('is a no-op while a fetch for the same candidate is already in flight', async () => {
      const { loadBreakdowns, refreshBreakdown } = mountUseMatchSource()
      const candidate = { source: 'src-1', mangaId: 1, url: 'https://src-1.example/title/1' } as never

      const originalGet = vi.mocked(apiClient.GET).getMockImplementation()!
      let release: (() => void) | null = null
      vi.mocked(apiClient.GET).mockImplementation((path: string) => {
        calls.push({ method: 'GET', path })
        return new Promise(resolve => (release = () => resolve({
          data: { total: 3, scanlators: [], status: 'ready', computedAt: '2026-07-30T00:00:00Z' },
          error: null,
          response: new Response(null, { status: 200 }),
        })))
      })

      try {
        const first = loadBreakdowns([candidate])
        await vi.waitFor(() => expect(release).not.toBeNull())
        calls = []

        await refreshBreakdown(candidate)
        expect(calls.filter(c => c.path === '/api/sources/{sourceId}/manga/{mangaId}/breakdown').length).toBe(0)

        release!()
        await first
      }
      finally {
        vi.mocked(apiClient.GET).mockImplementation(originalGet)
      }
    })
  })

  describe('batchAddProviders (Slice P batch attach)', () => {
    it('POSTs /api/series/{id}/providers/batch with the exact {providers} body and resolves the fresh detail', async () => {
      const { batchAddProviders } = mountUseMatchSource()

      const providers = [
        { source: 'src-1', mangaId: 42, url: '/manga/42', scanlator: '' },
        { source: 'src-2', mangaId: 7, url: '/manga/7', scanlator: 'Asura Scans' },
      ]
      const result = await batchAddProviders(providers)

      const postCall = calls.find(c => c.path === '/api/series/{id}/providers/batch')
      expect(postCall).toBeDefined()
      expect(postCall!.body).toEqual({ providers })
      expect(result).toEqual({ id: 'series-1', displayName: 'Solo Leveling' })
    })

    it('failure sets error and resolves null', async () => {
      nextBatchAddOk = false
      const { error, batchAddProviders } = mountUseMatchSource()

      const result = await batchAddProviders([{ source: 'src-1', mangaId: 42, url: '/manga/42', scanlator: '' }])

      expect(result).toBeNull()
      expect(error.value).toBe('add failed')
    })

    it('saving flips true during batchAddProviders and back to false once it resolves', async () => {
      const { saving, batchAddProviders } = mountUseMatchSource()
      expect(saving.value).toBe(false)

      const promise = batchAddProviders([{ source: 'src-1', mangaId: 42, url: '/manga/42', scanlator: '' }])
      expect(saving.value).toBe(true)
      await promise

      expect(saving.value).toBe(false)
    })
  })
})
