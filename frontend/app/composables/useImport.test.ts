/**
 * useImport — data layer for the Import / Adopt wizard (Screen G).
 *
 * Pins:
 *   1. search() discards a stale (superseded) response — the generation-counter
 *      guard mirrors the identical fix in useMatchSource.search() /
 *      useScanLibrary.match(): if the owner edits the query and re-searches
 *      before the previous request resolves, a slower earlier response must
 *      NOT clobber a faster later one.
 *   2. GAP-140: loadBreakdowns tracks the breakdown snapshot's own lifecycle
 *      (`breakdownSnapshots`, mirrors `useScanLibrary.ts`) instead of
 *      collapsing a pending/failed response to `null` — a `pending` walk now
 *      caches `[]` + `{status:'pending'}` so `useSourceConfigure` renders
 *      "Computing coverage…", not "Coverage unavailable".
 *   3. A pending row updates itself when `imports.coverage.done` lands
 *      (matched by source+url, the event's own identity — never the
 *      mangaId-keyed cache key), which is how the "permanent cache" problem
 *      is resolved: the cache entry is OVERWRITTEN in place, not expired.
 *   4. refreshBreakdown(candidate) forces `?refresh=true` and is a no-op while
 *      a fetch for that candidate is already in flight.
 *
 * Uses the same FakeEventSource stub as useScanLibrary.test.ts so the
 * NAMED_EVENTS loop in useProgressStream registers real addEventListener
 * calls our stub can fire through.
 *
 * vi.mock is hoisted by Vitest's transform so the apiClient mock is in place
 * before useImport.ts is evaluated, regardless of import order here.
 */
import { describe, it, expect, vi, beforeAll, beforeEach, afterEach } from 'vitest'
import { defineComponent } from 'vue'
import { mount } from '@vue/test-utils'
import { apiClient } from '~/utils/api/client'
import { useImport } from './useImport'
import { useProgressStream } from './useProgressStream'

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

// ── EventSource stub (mirrors useScanLibrary.test.ts) ────────────────────────

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

// ── Per-test isolation harness (useImport registers onUnmounted cleanup —
// mounting inside a real component instance gives it something to attach to,
// mirrors useScanLibrary.test.ts's mountScanLibrary) ─────────────────────────

type ImportApi = ReturnType<typeof useImport>

let activeWrapper: ReturnType<typeof mount> | null = null

function mountUseImport(): ImportApi {
  let api!: ImportApi
  const Harness = defineComponent({
    setup() {
      api = useImport()
      return () => null
    },
  })
  activeWrapper = mount(Harness)
  return api
}

// File-wide hooks (NOT nested in a single describe) so every test below —
// regardless of which top-level describe it lives in — gets the EventSource
// stub connected once and its mounted harness torn down afterward. Vitest's
// beforeAll/beforeEach/afterEach only reach tests inside the SAME describe
// (or nested under it); declaring them at file scope is what makes them apply
// file-wide. Without this, a composable instance mounted in one describe
// block leaks its `imports.coverage.done` subscription into every later
// test — several of which reuse the exact same candidate identity — so an
// SSE fire in a later test re-triggers EVERY still-subscribed earlier
// instance's fetch too (confirmed live: an expected single re-fetch counted
// seven before this fix).
beforeAll(() => {
  vi.stubGlobal('EventSource', FakeEventSource)
  useProgressStream().connect()
})

beforeEach(() => {
  calls = []
})

afterEach(() => {
  activeWrapper?.unmount()
  activeWrapper = null
})

describe('useImport', () => {
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

    const { searchResults, error, search } = mountUseImport()

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

    const { sources } = mountUseImport()
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

    const { inspect } = mountUseImport()
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
      calls.push({ method: 'GET', path, query: opts?.params?.query, params: opts?.params?.path })
      if (path === '/api/sources/{sourceId}/manga/{mangaId}/breakdown') {
        return breakdownGet(opts!.params!.path!.sourceId)
      }
      return Promise.resolve({ data: null, error: null, response: new Response(null, { status: 200 }) })
    })

    const { breakdowns, loadBreakdowns } = mountUseImport()
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
    // Every breakdown fetch carries the candidate's url query (P2 Suwayomi-removal —
    // the backend 400s without it).
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

    const { loadBreakdowns } = mountUseImport()
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

    const { breakdowns, error, loadBreakdowns } = mountUseImport()
    const candidate = { source: 'src-1', mangaId: 1, url: 'https://src-1.example/title/1' } as never
    await loadBreakdowns([candidate])

    expect(breakdowns.value['src-1:1']).toBeNull()
    expect(error.value).toBe('')

    await loadBreakdowns([candidate])
    expect(breakdownGet).toHaveBeenCalledTimes(1)
  })

  it('GAP-140: a pending snapshot caches as [] with a pending status, not null — the row must read "Computing coverage…", never "Coverage unavailable"', async () => {
    // The backend now answers 200 with an empty scanlators array while a
    // large series' walk is still running (never a 502). Collapsing this to
    // `null` (the pre-GAP-140-follow-up behaviour) rendered a still-running
    // walk identically to a genuine failure — this composable now tracks the
    // snapshot's own status (mirrors useScanLibrary.ts) so the two are
    // distinguishable.
    const breakdownGet = vi.fn(() => Promise.resolve({
      data: { total: 0, scanlators: [], status: 'pending' },
      error: null,
    }))
    vi.mocked(apiClient.GET).mockImplementation((path: string) => {
      calls.push({ method: 'GET', path })
      if (path === '/api/sources/{sourceId}/manga/{mangaId}/breakdown') return breakdownGet()
      return Promise.resolve({ data: null, error: null, response: new Response(null, { status: 200 }) })
    })

    const { breakdowns, breakdownSnapshots, loadBreakdowns } = mountUseImport()
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

    const { breakdowns, breakdownSnapshots, loadBreakdowns } = mountUseImport()
    const candidate = { source: 'src-1', mangaId: 1, url: 'https://src-1.example/title/1' } as never
    await loadBreakdowns([candidate])

    expect(breakdowns.value['src-1:1']).toEqual([])
    expect(breakdownSnapshots.value['src-1:1']).toEqual({ status: 'failed', computedAt: '', error: 'upstream timed out' })
  })

  it('a ready snapshot carries its as-of instant', async () => {
    vi.mocked(apiClient.GET).mockImplementation((path: string) => {
      calls.push({ method: 'GET', path })
      if (path === '/api/sources/{sourceId}/manga/{mangaId}/breakdown') {
        return Promise.resolve({
          data: { total: 12, scanlators: [{ scanlator: 'src-1', count: 12, ranges: '1-12' }], status: 'ready', computedAt: '2026-07-31T09:00:00Z' },
          error: null,
        })
      }
      return Promise.resolve({ data: null, error: null, response: new Response(null, { status: 200 }) })
    })

    const { breakdownSnapshots, loadBreakdowns } = mountUseImport()
    const candidate = { source: 'src-1', mangaId: 1, url: 'https://src-1.example/title/1' } as never
    await loadBreakdowns([candidate])

    expect(breakdownSnapshots.value['src-1:1']).toEqual({ status: 'ready', computedAt: '2026-07-31T09:00:00Z', error: '' })
  })
})

describe('useImport — imports.coverage.done (GAP-140) — resolving the "permanent cache" problem', () => {
  it('re-fetches the matching candidate (by source+url, not the mangaId cache key) and refreshes both caches in place', async () => {
    const { breakdowns, breakdownSnapshots, loadBreakdowns } = mountUseImport()
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

    // The row is "stuck" — a second loadBreakdowns for the same candidate is
    // a no-op (already cached) — proving the fix must come from the SSE path,
    // not from re-calling loadBreakdowns by hand.
    calls = []
    await loadBreakdowns([candidate])
    expect(calls.filter(c => c.path === '/api/sources/{sourceId}/manga/{mangaId}/breakdown').length).toBe(0)

    // The background walk finishes.
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
    const { loadBreakdowns } = mountUseImport()
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

describe('useImport — refreshBreakdown (GAP-140 follow-up)', () => {
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

    const { breakdowns, breakdownSnapshots, loadBreakdowns, refreshBreakdown } = mountUseImport()
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
    // The row reflects that work restarted.
    expect(breakdowns.value['src-1:1']).toEqual([])
    expect(breakdownSnapshots.value['src-1:1']).toEqual({ status: 'pending', computedAt: '', error: '' })
  })

  it('is a no-op while a fetch for the same candidate is already in flight', async () => {
    const { loadBreakdowns, refreshBreakdown } = mountUseImport()
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
