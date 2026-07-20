/**
 * useDownloadSummary — the persistent nav-counter data layer.
 *
 * Pins:
 *   1. The initial fetch reads GET /api/downloads/summary and exposes the three
 *      counts (downloading / queued / failed).
 *   2. A live SSE download.fail refetches (leading edge) so the counts move at once.
 *
 * Non-vacuous: drop the fetchSummary call and test 1 sees 0/0/0; drop the SSE
 * subscription and test 2 never picks up the changed counts.
 */
import { describe, it, expect, vi, beforeAll } from 'vitest'
import { useDownloadSummary } from './useDownloadSummary'
import { useProgressStream } from './useProgressStream'

// Mutable server payload so a refetch can observe a change.
let summary = { downloading: 2, queued: 5, failed: 1 }

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string) => {
      if (path !== '/api/downloads/summary') return Promise.resolve({ data: null, error: null })
      return Promise.resolve({ data: { ...summary }, error: null })
    }),
    POST: vi.fn(),
    PATCH: vi.fn(),
    DELETE: vi.fn(),
    use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

// ── EventSource stub (carries a JSON payload) ─────────────────────────────────
interface StubSource { fire: (name: string, payload: unknown) => void }
let stubSource: StubSource | null = null

class FakeEventSource {
  onopen: ((ev: Event) => void) | null = null
  onerror: ((ev: Event) => void) | null = null
  private _handlers = new Map<string, ((ev: Event) => void)[]>()
  constructor(_url: string) {
    const handlers = this._handlers
    const onOpenRef = (): void => { this.onopen?.(new Event('open')) }
    stubSource = {
      fire(name: string, payload: unknown) {
        const ev = { data: JSON.stringify(payload) } as MessageEvent
        ;(handlers.get(name) ?? []).forEach((h) => h(ev))
      },
    }
    queueMicrotask(onOpenRef)
  }

  addEventListener(name: string, handler: (ev: Event) => void): void {
    if (!this._handlers.has(name)) this._handlers.set(name, [])
    this._handlers.get(name)!.push(handler)
  }

  removeEventListener(): void { void 0 }
  close(): void { stubSource = null }
}

describe('useDownloadSummary', () => {
  beforeAll(() => {
    vi.stubGlobal('EventSource', FakeEventSource)
    useProgressStream().connect()
  })

  it('fetches the three summary counts on first use', async () => {
    const { downloading, queued, failed } = useDownloadSummary()
    await vi.waitFor(() => expect(downloading.value).toBe(2))
    expect(queued.value).toBe(5)
    expect(failed.value).toBe(1)
  })

  it('refetches on a live download.fail SSE event', async () => {
    const { downloading, queued, failed } = useDownloadSummary()
    await vi.waitFor(() => expect(stubSource).not.toBeNull())

    // Server now reports different counts; a download.fail must pull them.
    summary = { downloading: 0, queued: 4, failed: 3 }
    stubSource!.fire('download.fail', { chapter_id: 'x', state: 'failed', error: 'boom' })

    await vi.waitFor(() => expect(failed.value).toBe(3))
    expect(downloading.value).toBe(0)
    expect(queued.value).toBe(4)
  })
})
