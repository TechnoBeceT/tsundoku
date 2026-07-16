/**
 * useScanLibrary – data layer for the Scan Library wizard (Task 5).
 *
 * Pins the behaviours the Task-3 concurrency review flagged as load-bearing:
 *   1. startScan() POSTs /api/library/scan and flips scanState to 'scanning'.
 *   2. startScan() treats a 409 {started:false} (a scan already in flight) as
 *      "already scanning", NOT an error — no error string is set.
 *   3. scan.done is TERMINAL: status -> 'done', entries refetch, and a
 *      scan.progress frame that arrives AFTER scan.done (the backend's
 *      leaked-goroutine-on-timeout case) is ignored — it must never flip
 *      status back to 'scanning'.
 *   4. scan.done carries its `error` string onto scanState (§16 — the wizard
 *      must be able to show "scan timed out / failed").
 *   5. skip(path) POSTs the skip endpoint, then refetches the entries list.
 *   6. importWithMatches(path, matches) POSTs /api/library/import with
 *      {path, matches} in the body (Slice P — was a single fixed-importance
 *      `match`).
 *   7. loadBreakdowns(candidates) fetches every candidate's per-scanlator
 *      breakdown in parallel (each fetch carrying the candidate's `?url=`,
 *      required by the backend) and caches it by `source:mangaId` (Slice P,
 *      copied from `useMatchSource.loadBreakdowns`).
 *   8. loadSources() GETs /api/sources once and maps via mapSource (drives the
 *      page-level "Limit matches to:" filter chips).
 *   9. match(path, sourceIDs) CSV-joins the sources param when set and omits
 *      it when the list is empty.
 *
 * Uses the same FakeEventSource stub as useProgressStream.extensions.test.ts /
 * useExtensions.refetch.test.ts so the NAMED_EVENTS loop in useProgressStream
 * registers real addEventListener calls our stub can fire through — this
 * exercises the real subscription wiring, not a re-implemented mock of it.
 *
 * vi.mock is hoisted by Vitest's transform so the apiClient mock is in place
 * before useScanLibrary.ts is evaluated, regardless of import order here.
 */
import { describe, it, expect, vi, beforeAll, beforeEach, afterEach } from 'vitest'
import { defineComponent } from 'vue'
import { mount } from '@vue/test-utils'
import { useScanLibrary } from './useScanLibrary'
import { useProgressStream } from './useProgressStream'
import { apiClient } from '~/utils/api/client'

// ── Call tracking ─────────────────────────────────────────────────────────────

interface Call { method: string, path: string, body?: unknown, query?: unknown }
let calls: Call[] = []

// Controls the next POST /api/library/scan response.
let nextScanStatus = 202

// One "found" staged entry, shaped like the FoundSeries DTO — used to seed
// GET /api/library/imports?status=pending for the drain-all test below.
interface FoundEntryLike {
  path: string
  title: string
  category: string
  chapterCount: number
  providers: string[]
  status: string
  alreadyInDb: boolean
}

// Backing store for GET /api/library/imports?status=pending, paginated by
// the mock itself (offset/limit read off the request query) — lets the
// import-all-drains-everything test seed an arbitrarily large pending list
// without hand-writing every page's response.
let pendingSeed: FoundEntryLike[] = []

// ── Module mock ───────────────────────────────────────────────────────────────

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string, opts?: {
      params?: {
        query?: Record<string, unknown>
        path?: { sourceId?: string, mangaId?: number }
      }
    }) => {
      const query = opts?.params?.query
      calls.push({ method: 'GET', path, query })
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
      if (path === '/api/library/imports') {
        if (query?.status === 'pending') {
          const limit = typeof query.limit === 'number' ? query.limit : 50
          const offset = typeof query.offset === 'number' ? query.offset : 0
          const page = pendingSeed.slice(offset, offset + limit)
          return Promise.resolve({ data: page, error: null, response: new Response(null, { status: 200 }) })
        }
        return Promise.resolve({ data: [], error: null, response: new Response(null, { status: 200 }) })
      }
      if (path === '/api/library/imports/match') {
        return Promise.resolve({
          data: [{
            title: 'Test Manga',
            candidates: [{
              source: 'src-1',
              sourceName: 'MangaDex',
              lang: 'en',
              mangaId: 42,
              title: 'Test Manga',
              thumbnailUrl: 'https://example.com/thumb.jpg',
            }],
          }],
          error: null,
          response: new Response(null, { status: 200 }),
        })
      }
      if (path === '/api/sources/{sourceId}/manga/{mangaId}/breakdown') {
        const sourceId = opts?.params?.path?.sourceId ?? ''
        return Promise.resolve({
          data: { total: 12, scanlators: [{ scanlator: sourceId, count: 12, ranges: '1-12' }] },
          error: null,
          response: new Response(null, { status: 200 }),
        })
      }
      return Promise.resolve({ data: null, error: null, response: new Response(null, { status: 200 }) })
    }),
    POST: vi.fn().mockImplementation((path: string, opts?: { body?: { paths?: string[] } }) => {
      calls.push({ method: 'POST', path, body: opts?.body })
      if (path === '/api/library/scan') {
        if (nextScanStatus === 409) {
          return Promise.resolve({
            data: null,
            error: { started: false },
            response: new Response(null, { status: 409 }),
          })
        }
        return Promise.resolve({
          data: { started: true },
          error: null,
          response: new Response(null, { status: 202 }),
        })
      }
      if (path === '/api/library/imports/skip') {
        return Promise.resolve({ data: null, error: null, response: new Response(null, { status: 204 }) })
      }
      if (path === '/api/library/import') {
        return Promise.resolve({
          data: { id: 'series-1' },
          error: null,
          response: new Response(null, { status: 200 }),
        })
      }
      if (path === '/api/library/import/batch') {
        // Echo the chunk size back as `imported` so the drain-all test can
        // assert the accumulated total across every chunk.
        const chunkSize = opts?.body?.paths?.length ?? 0
        return Promise.resolve({
          data: { imported: chunkSize, failed: [] },
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

// ── EventSource stub (mirrors useProgressStream.extensions.test.ts) ──────────

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

// ── Per-test isolation harness ───────────────────────────────────────────────

type ScanLibraryApi = ReturnType<typeof useScanLibrary>

let activeWrapper: ReturnType<typeof mount> | null = null

/**
 * Mounts useScanLibrary() inside a real component instance instead of
 * calling it bare at test-body scope. useScanLibrary registers its
 * scan.start/scan.progress/scan.done subscriptions via `on()` and tears
 * them down in `onUnmounted` (see useScanLibrary.ts) — with no active
 * component instance `onUnmounted` is a silent no-op, so calling the
 * composable directly would leak listeners onto the useProgressStream
 * singleton across every test in this file. Mounting gives `onUnmounted` a
 * real instance to attach to, and the `afterEach` below unmounts it so each
 * test starts with a clean listener set.
 */
function mountScanLibrary(): ScanLibraryApi {
  let api!: ScanLibraryApi
  const Harness = defineComponent({
    setup() {
      api = useScanLibrary()
      return () => null
    },
  })
  activeWrapper = mount(Harness)
  return api
}

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('useScanLibrary', () => {
  beforeAll(() => {
    vi.stubGlobal('EventSource', FakeEventSource)
    // Connect the singleton stream once so the stub exists before any
    // composable under test registers its scan.* subscriptions.
    useProgressStream().connect()
  })

  beforeEach(() => {
    calls = []
    nextScanStatus = 202
    pendingSeed = []
  })

  afterEach(() => {
    // Unmount the previous test's harness so its onUnmounted cleanup fires
    // (unsubscribing from scan.start/scan.progress/scan.done) before the
    // next test mounts a fresh instance.
    activeWrapper?.unmount()
    activeWrapper = null
  })

  it('startScan() POSTs /api/library/scan and flips scanState to scanning', async () => {
    const { scanState, startScan } = mountScanLibrary()
    expect(scanState.value.status).toBe('idle')

    await startScan()

    expect(calls).toContainEqual({ method: 'POST', path: '/api/library/scan', body: undefined })
    expect(scanState.value.status).toBe('scanning')
    expect(scanState.value.error).toBe('')
  })

  it('startScan() treats a 409 (already scanning) as scanning, not an error', async () => {
    nextScanStatus = 409
    const { scanState, startScan } = mountScanLibrary()

    await startScan()

    expect(scanState.value.status).toBe('scanning')
    expect(scanState.value.error).toBe('')
  })

  it('scan.done is terminal: flips status to done, refetches entries, and ignores a late scan.progress', async () => {
    const { scanState, startScan } = mountScanLibrary()
    await startScan()
    expect(scanState.value.status).toBe('scanning')

    await vi.waitFor(() => expect(stubSource).not.toBeNull())

    calls = []
    stubSource!.fire('scan.done', { total: 12, found: 12 })

    expect(scanState.value.status).toBe('done')
    // scan.done triggers a refetch of the staged entries list.
    await vi.waitFor(() => {
      expect(calls.some(c => c.method === 'GET' && c.path === '/api/library/imports')).toBe(true)
    })

    // A late scan.progress (the backend's leaked-goroutine-on-timeout case)
    // must NOT flip status back to 'scanning'.
    stubSource!.fire('scan.progress', { processed: 99, total: 100, path: '/late' })
    expect(scanState.value.status).toBe('done')
  })

  it('scan.done carries its error string onto scanState', async () => {
    const { scanState, startScan } = mountScanLibrary()
    await startScan()
    await vi.waitFor(() => expect(stubSource).not.toBeNull())

    stubSource!.fire('scan.done', { error: 'scan timed out after 30m0s' })

    expect(scanState.value.status).toBe('done')
    expect(scanState.value.error).toBe('scan timed out after 30m0s')
  })

  it('skip(path) POSTs the skip endpoint then refetches entries', async () => {
    const { skip } = mountScanLibrary()
    calls = []

    await skip('/library/Manga/Foo')

    const skipCall = calls.find(c => c.path === '/api/library/imports/skip')
    expect(skipCall).toBeDefined()
    expect(skipCall!.body).toEqual({ path: '/library/Manga/Foo' })

    expect(calls.some(c => c.method === 'GET' && c.path === '/api/library/imports')).toBe(true)
  })

  it('importWithMatches(path, matches) POSTs /api/library/import with {path, matches}', async () => {
    const { importWithMatches } = mountScanLibrary()
    calls = []

    await importWithMatches('/library/Manga/Foo', [
      { source: 'src-1', mangaId: 42, url: '/manga/42', scanlator: '' },
      { source: 'src-2', mangaId: 7, url: '/manga/7', scanlator: 'Asura Scans' },
    ])

    const importCall = calls.find(c => c.path === '/api/library/import')
    expect(importCall).toBeDefined()
    expect(importCall!.body).toEqual({
      path: '/library/Manga/Foo',
      matches: [
        { source: 'src-1', mangaId: 42, url: '/manga/42', scanlator: '' },
        { source: 'src-2', mangaId: 7, url: '/manga/7', scanlator: 'Asura Scans' },
      ],
    })
  })

  it('importWithMatches(path, []) is a valid disk-only import (empty matches, no attach)', async () => {
    const { importWithMatches } = mountScanLibrary()
    calls = []

    await importWithMatches('/library/Manga/Foo', [])

    const importCall = calls.find(c => c.path === '/api/library/import')
    expect(importCall).toBeDefined()
    expect(importCall!.body).toEqual({ path: '/library/Manga/Foo', matches: [] })
  })

  describe('loadBreakdowns (per-scanlator auto-split fetch, copied from useMatchSource)', () => {
    it('fetches every candidate in parallel and caches the mapped scanlators, keyed by source:mangaId', async () => {
      const { breakdowns, loadBreakdowns } = mountScanLibrary()
      calls = []

      await loadBreakdowns([
        { source: 'src-1', mangaId: 1, url: 'https://src-1.example/title/1' } as never,
        { source: 'src-2', mangaId: 2, url: 'https://src-2.example/title/2' } as never,
      ])

      const breakdownCalls = calls.filter(c => c.path === '/api/sources/{sourceId}/manga/{mangaId}/breakdown')
      expect(breakdownCalls.length).toBe(2)
      expect(breakdowns.value['src-1:1']).toEqual([{ scanlator: 'src-1', count: 12, ranges: '1-12' }])
      expect(breakdowns.value['src-2:2']).toEqual([{ scanlator: 'src-2', count: 12, ranges: '1-12' }])
      // Every breakdown fetch carries the candidate's url query (P2 Suwayomi-removal
      // — the backend 400s without it).
      expect(breakdownCalls).toContainEqual(expect.objectContaining({ query: { url: 'https://src-1.example/title/1' } }))
      expect(breakdownCalls).toContainEqual(expect.objectContaining({ query: { url: 'https://src-2.example/title/2' } }))
    })

    it('caches by source:mangaId — a second loadBreakdowns call for an already-loaded candidate does not re-fetch', async () => {
      const { loadBreakdowns } = mountScanLibrary()
      calls = []
      const candidate = { source: 'src-1', mangaId: 1, url: 'https://src-1.example/title/1' } as never

      await loadBreakdowns([candidate])
      expect(calls.filter(c => c.path === '/api/sources/{sourceId}/manga/{mangaId}/breakdown').length).toBe(1)

      await loadBreakdowns([candidate])
      expect(calls.filter(c => c.path === '/api/sources/{sourceId}/manga/{mangaId}/breakdown').length).toBe(1)
    })

    it('caches a failed fetch as null (non-fatal) and never retries it', async () => {
      const { breakdowns, loadBreakdowns } = mountScanLibrary()
      vi.mocked(apiClient.GET).mockImplementationOnce((path: string) => {
        calls.push({ method: 'GET', path })
        return Promise.resolve({ data: null, error: { message: 'upstream failure' }, response: new Response(null, { status: 502 }) })
      })
      calls = []
      const candidate = { source: 'src-1', mangaId: 1, url: 'https://src-1.example/title/1' } as never

      await loadBreakdowns([candidate])
      expect(breakdowns.value['src-1:1']).toBeNull()

      await loadBreakdowns([candidate])
      expect(calls.filter(c => c.path === '/api/sources/{sourceId}/manga/{mangaId}/breakdown').length).toBe(1)
    })
  })

  it('loadSources() GETs /api/sources and maps it into the sources ref', async () => {
    const { sources, loadSources } = mountScanLibrary()
    calls = []

    await loadSources()

    expect(calls.filter(c => c.path === '/api/sources')).toHaveLength(1)
    expect(sources.value).toEqual([
      { id: 'src-1', name: 'MangaDex', lang: 'en' },
      { id: 'src-2', name: 'Asura Scans', lang: 'en' },
    ])
  })

  it('match(path, sourceIDs) CSV-joins the sources param when set', async () => {
    const { match } = mountScanLibrary()
    calls = []

    await match('/library/Manga/Foo', ['a'])

    const matchCall = calls.find(c => c.path === '/api/library/imports/match')
    expect(matchCall).toBeDefined()
    expect(matchCall!.query).toEqual({ path: '/library/Manga/Foo', sources: 'a' })
  })

  it('match(path, []) omits the sources param when the list is empty', async () => {
    const { match } = mountScanLibrary()
    calls = []

    await match('/library/Manga/Foo', [])

    const matchCall = calls.find(c => c.path === '/api/library/imports/match')
    expect(matchCall).toBeDefined()
    expect(matchCall!.query).toEqual({ path: '/library/Manga/Foo' })
  })

  it('match(path) GETs the match endpoint with that path and returns mapped SearchGroups', async () => {
    const { match } = mountScanLibrary()
    calls = []

    const groups = await match('/library/Manga/Foo', [])

    const matchCall = calls.find(c => c.path === '/api/library/imports/match')
    expect(matchCall).toBeDefined()
    expect(matchCall!.query).toEqual({ path: '/library/Manga/Foo' })

    expect(groups).toEqual([
      {
        title: 'Test Manga',
        candidates: [{
          source: 'src-1',
          sourceName: 'MangaDex',
          lang: 'en',
          mangaId: 42,
          title: 'Test Manga',
          thumbnailUrl: 'https://example.com/thumb.jpg',
        }],
      },
    ])
  })

  it('match() discards a stale response when an earlier (slower) request resolves after a later (faster) one', async () => {
    // Task-7 review fix: two overlapping match() calls for two DIFFERENT
    // staged entries (the owner clicks Match on series A, goes Back, then
    // clicks Match on series B). B is fast and resolves FIRST; A is slow and
    // resolves SECOND, after B has already landed. The composable's shared
    // matchGroups/matchError state must reflect B (the latest request) even
    // though A's promise settles later — a path-equality guard would not be
    // enough (this drives two DIFFERENT paths), which is why the fix uses a
    // monotonic generation counter.
    const { match, matchGroups, matchError } = mountScanLibrary()

    interface DeferredGetResult { data: unknown, error: unknown, response: Response }
    let resolveA!: (v: DeferredGetResult) => void
    let resolveB!: (v: DeferredGetResult) => void
    const responseA = new Promise<DeferredGetResult>((resolve) => { resolveA = resolve })
    const responseB = new Promise<DeferredGetResult>((resolve) => { resolveB = resolve })

    vi.mocked(apiClient.GET)
      .mockImplementationOnce(() => responseA)
      .mockImplementationOnce(() => responseB)

    const matchA = match('/library/Manga/Series-A', []) // slow, started first
    const matchB = match('/library/Manga/Series-B', []) // fast, started second

    // B (the LATER request) resolves FIRST.
    resolveB({
      data: [{ title: 'Series B', candidates: [] }],
      error: null,
      response: new Response(null, { status: 200 }),
    })
    await matchB

    expect(matchGroups.value).toEqual([{ title: 'Series B', candidates: [] }])

    // A (the EARLIER request) finally resolves AFTER B already landed.
    resolveA({
      data: [{ title: 'Series A', candidates: [] }],
      error: null,
      response: new Response(null, { status: 200 }),
    })
    await matchA

    // The stale A response must be discarded — shared state still reflects
    // B, the latest request, not whichever promise happened to settle last.
    expect(matchGroups.value).toEqual([{ title: 'Series B', candidates: [] }])
    expect(matchError.value).toBe('')
  })

  it('exposes entriesError (list-load failure) and error (per-row failure) as distinct, independently-working members', async () => {
    const { entriesError, error, skip, refresh } = mountScanLibrary()

    // Per-row failure: the next POST (skip's mutation) fails.
    vi.mocked(apiClient.POST).mockImplementationOnce((p: string) => {
      if (p === '/api/library/imports/skip') {
        return Promise.resolve({
          data: null,
          error: { message: 'skip failed' },
          response: new Response(null, { status: 500 }),
        })
      }
      return Promise.resolve({ data: null, error: null, response: new Response(null, { status: 200 }) })
    })
    await skip('/library/Manga/Foo')

    expect(error('/library/Manga/Foo')).toBe('skip failed')
    // The list-load error must be untouched by a per-row mutation failure.
    expect(entriesError.value).toBe('')

    // List-load failure: the next GET /api/library/imports call fails.
    vi.mocked(apiClient.GET).mockImplementationOnce((p: string) => {
      if (p === '/api/library/imports') {
        return Promise.resolve({
          data: null,
          error: { message: 'list load failed' },
          response: new Response(null, { status: 500 }),
        })
      }
      return Promise.resolve({ data: [], error: null, response: new Response(null, { status: 200 }) })
    })
    await refresh()

    expect(entriesError.value).toBe('list load failed')
    // The earlier per-row error must still be independently readable — the
    // two must never collide on a shared `error` key (the bug this test
    // guards against).
    expect(error('/library/Manga/Foo')).toBe('skip failed')
  })

  it('importAllDiskOnly() drains every pending page then imports all of them across chunked batches', async () => {
    const total = 550 // > 500 (the batch cap) so this must span ≥2 chunks
    pendingSeed = Array.from({ length: total }, (_, i) => ({
      path: `/library/Manga/Series-${i}`,
      title: `Series ${i}`,
      category: 'Manga',
      chapterCount: 1,
      providers: [],
      status: 'pending',
      alreadyInDb: false,
    }))

    const { importAllDiskOnly, batchResult, batchError } = mountScanLibrary()
    calls = []

    await importAllDiskOnly()

    expect(batchError.value).toBe('')

    // All GET /api/library/imports calls made during importAllDiskOnly():
    // the drain pages (status=pending, limit=DRAIN_PAGE=200) PLUS the one
    // trailing refetch `load(false)` fires after the batch completes
    // (status=undefined, limit=PAGE=50) — distinguished by `limit` so the
    // trailing refetch can't be silently folded into the drain count.
    const importsGetCalls = calls.filter(c => c.method === 'GET' && c.path === '/api/library/imports')
    const drainCalls = importsGetCalls.filter(c => (c.query as { limit?: number } | undefined)?.limit === 200)
    const trailingRefetchCalls = importsGetCalls.filter(c => (c.query as { limit?: number } | undefined)?.limit !== 200)

    // Exactly 3 drain pages cover 550 rows at 200/page: 200 + 200 + 150
    // (the 150-row page is short, terminating the drain loop).
    expect(drainCalls.length).toBe(3)
    // Exactly 1 trailing refetch — importAllDiskOnly's `await load(false)`
    // after the batch phase, not a drain page.
    expect(trailingRefetchCalls.length).toBe(1)
    expect(importsGetCalls.length).toBe(4)

    // Chunk: 550 paths / 500-cap = exactly 2 batch POSTs (500 + 50), and
    // every drained path is covered exactly once — none dropped, none
    // duplicated, no infinite loop.
    const batchCalls = calls.filter(c => c.method === 'POST' && c.path === '/api/library/import/batch')
    expect(batchCalls.length).toBe(2)
    const importedPaths = batchCalls.flatMap(c => (c.body as { paths: string[] }).paths)
    expect(importedPaths.length).toBe(total)
    expect(new Set(importedPaths).size).toBe(total)

    expect(batchResult.value?.imported).toBe(total)
    expect(batchResult.value?.failed).toEqual([])
  })

  it('importAllDiskOnly() with zero pending sets an explicit zero result instead of a silent no-op', async () => {
    // pendingSeed defaults to [] (reset in beforeEach) — the drain finds
    // nothing to import.
    const { importAllDiskOnly, batchResult, batchError } = mountScanLibrary()
    calls = []

    await importAllDiskOnly()

    expect(batchError.value).toBe('')
    // §16: batchResult must be an explicit zero outcome, never left
    // undefined/null — that's the silent no-op this test guards against.
    expect(batchResult.value).toEqual({ imported: 0, failed: [] })

    // No batch POST should have fired — there was nothing to chunk.
    expect(calls.some(c => c.method === 'POST' && c.path === '/api/library/import/batch')).toBe(false)
  })
})
