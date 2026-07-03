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
 *   6. importWithMatch(path, match) POSTs /api/library/import with
 *      {path, match} in the body.
 *
 * Uses the same FakeEventSource stub as useProgressStream.extensions.test.ts /
 * useExtensions.refetch.test.ts so the NAMED_EVENTS loop in useProgressStream
 * registers real addEventListener calls our stub can fire through — this
 * exercises the real subscription wiring, not a re-implemented mock of it.
 *
 * vi.mock is hoisted by Vitest's transform so the apiClient mock is in place
 * before useScanLibrary.ts is evaluated, regardless of import order here.
 */
import { describe, it, expect, vi, beforeAll, beforeEach } from 'vitest'
import { useScanLibrary } from './useScanLibrary'
import { useProgressStream } from './useProgressStream'

// ── Call tracking ─────────────────────────────────────────────────────────────

interface Call { method: string, path: string, body?: unknown }
let calls: Call[] = []

// Controls the next POST /api/library/scan response.
let nextScanStatus = 202

// ── Module mock ───────────────────────────────────────────────────────────────

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string) => {
      calls.push({ method: 'GET', path })
      if (path === '/api/library/imports') {
        return Promise.resolve({ data: [], error: null, response: new Response(null, { status: 200 }) })
      }
      if (path === '/api/library/imports/match') {
        return Promise.resolve({
          data: [{ title: 'Test Manga', candidates: [] }],
          error: null,
          response: new Response(null, { status: 200 }),
        })
      }
      return Promise.resolve({ data: null, error: null, response: new Response(null, { status: 200 }) })
    }),
    POST: vi.fn().mockImplementation((path: string, opts?: { body?: unknown }) => {
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
        return Promise.resolve({
          data: { imported: 0, failed: [] },
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
  })

  it('startScan() POSTs /api/library/scan and flips scanState to scanning', async () => {
    const { scanState, startScan } = useScanLibrary()
    expect(scanState.value.status).toBe('idle')

    await startScan()

    expect(calls).toContainEqual({ method: 'POST', path: '/api/library/scan', body: undefined })
    expect(scanState.value.status).toBe('scanning')
    expect(scanState.value.error).toBe('')
  })

  it('startScan() treats a 409 (already scanning) as scanning, not an error', async () => {
    nextScanStatus = 409
    const { scanState, startScan } = useScanLibrary()

    await startScan()

    expect(scanState.value.status).toBe('scanning')
    expect(scanState.value.error).toBe('')
  })

  it('scan.done is terminal: flips status to done, refetches entries, and ignores a late scan.progress', async () => {
    const { scanState, startScan } = useScanLibrary()
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
    const { scanState, startScan } = useScanLibrary()
    await startScan()
    await vi.waitFor(() => expect(stubSource).not.toBeNull())

    stubSource!.fire('scan.done', { error: 'scan timed out after 30m0s' })

    expect(scanState.value.status).toBe('done')
    expect(scanState.value.error).toBe('scan timed out after 30m0s')
  })

  it('skip(path) POSTs the skip endpoint then refetches entries', async () => {
    const { skip } = useScanLibrary()
    calls = []

    await skip('/library/Manga/Foo')

    const skipCall = calls.find(c => c.path === '/api/library/imports/skip')
    expect(skipCall).toBeDefined()
    expect(skipCall!.body).toEqual({ path: '/library/Manga/Foo' })

    expect(calls.some(c => c.method === 'GET' && c.path === '/api/library/imports')).toBe(true)
  })

  it('importWithMatch(path, match) POSTs /api/library/import with {path, match}', async () => {
    const { importWithMatch } = useScanLibrary()
    calls = []

    await importWithMatch('/library/Manga/Foo', { source: 'src-1', mangaId: 42, importance: 2 })

    const importCall = calls.find(c => c.path === '/api/library/import')
    expect(importCall).toBeDefined()
    expect(importCall!.body).toEqual({
      path: '/library/Manga/Foo',
      match: { source: 'src-1', mangaId: 42, importance: 2 },
    })
  })
})
