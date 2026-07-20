/**
 * useCycleTimers — the two derived header countdowns.
 *
 * Pins:
 *   1. After GET /api/settings resolves, both countdowns SEED from their interval
 *      (so a plausible countdown shows on first mount, before any SSE boundary).
 *   2. The local 1s ticker decrements the remaining time.
 *   3. A `cycle.done` SSE event RE-SEEDS the download countdown to now + interval
 *      (self-correcting after an early/manual cycle); `refresh.done` re-seeds refresh.
 *   4. `downloadRunning` / `refreshRunning` follow the cycle.start / refresh.start
 *      SSE flags.
 *
 * Non-vacuous: if the interval parse dropped, the seed (assertion 1) would stay
 * null; if the cycle.done listener were missing, assertion 3's reseed would not
 * bounce back up.
 *
 * Uses the same FakeEventSource stub as useProgressStream.sources.test.ts to drive
 * SSE, and fake timers for the 1s countdown clock.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { effectScope } from 'vue'
import { useCycleTimers } from './useCycleTimers'
import { useProgressStream } from './useProgressStream'

// ── EventSource stub (drives useProgressStream) ────────────────────────────────
interface StubSource { fire: (name: string, data: unknown) => void }
let stubSource: StubSource | null = null

class FakeEventSource {
  onopen: ((ev: Event) => void) | null = null
  onerror: ((ev: Event) => void) | null = null
  private _handlers = new Map<string, ((ev: Event) => void)[]>()
  constructor(_url: string) {
    const handlers = this._handlers
    stubSource = {
      fire(name, data) {
        const ev = { data: JSON.stringify(data) } as MessageEvent
        ;(handlers.get(name) ?? []).forEach(h => h(ev))
      },
    }
  }
  addEventListener(name: string, handler: (ev: Event) => void): void {
    if (!this._handlers.has(name)) this._handlers.set(name, [])
    this._handlers.get(name)!.push(handler)
  }
  removeEventListener(): void { void 0 }
  close(): void { stubSource = null }
}

// ── apiClient mock (settings intervals) ────────────────────────────────────────
vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string) => {
      if (path === '/api/settings') {
        return Promise.resolve({
          data: [
            { key: 'jobs.download_interval', value: '1m0s' },
            { key: 'jobs.refresh_interval', value: '2h0m0s' },
            { key: 'jobs.max_retries', value: '5' },
          ],
          error: null,
        })
      }
      return Promise.resolve({ data: null, error: null })
    }),
    POST: vi.fn(), PATCH: vi.fn(), DELETE: vi.fn(), PUT: vi.fn(), use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

const flush = async (): Promise<void> => { await Promise.resolve(); await Promise.resolve() }

describe('useCycleTimers', () => {
  beforeEach(() => {
    vi.stubGlobal('EventSource', FakeEventSource)
    vi.useFakeTimers()
    stubSource = null
  })
  afterEach(() => {
    // Reset the useProgressStream module singleton so the next test's connect()
    // creates a fresh FakeEventSource (connect is idempotent while a source is open).
    useProgressStream().disconnect()
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('seeds both countdowns from their interval, ticks down, and re-seeds on *.done', async () => {
    const scope = effectScope()
    const t = scope.run(() => useCycleTimers())!
    await flush()

    // 1. Seeded from the intervals (download 60s, refresh 2h).
    expect(t.downloadRemainingMs.value).toBe(60_000)
    expect(t.refreshRemainingMs.value).toBe(7_200_000)

    // 2. The 1s ticker decrements.
    vi.advanceTimersByTime(3_000)
    expect(t.downloadRemainingMs.value).toBe(57_000)
    expect(t.refreshRemainingMs.value).toBe(7_197_000)

    // 3. cycle.done re-seeds the download countdown to a full interval again.
    stubSource!.fire('cycle.done', {})
    expect(t.downloadRemainingMs.value).toBe(60_000)
    // refresh unaffected until refresh.done.
    expect(t.refreshRemainingMs.value).toBe(7_197_000)
    stubSource!.fire('refresh.done', {})
    expect(t.refreshRemainingMs.value).toBe(7_200_000)

    scope.stop()
  })

  it('follows the cycle.start / refresh.start running flags', async () => {
    const scope = effectScope()
    const t = scope.run(() => useCycleTimers())!
    await flush()

    expect(t.downloadRunning.value).toBe(false)
    expect(t.refreshRunning.value).toBe(false)

    stubSource!.fire('cycle.start', {})
    expect(t.downloadRunning.value).toBe(true)
    stubSource!.fire('cycle.done', {})
    expect(t.downloadRunning.value).toBe(false)

    stubSource!.fire('refresh.start', {})
    expect(t.refreshRunning.value).toBe(true)
    stubSource!.fire('refresh.done', {})
    expect(t.refreshRunning.value).toBe(false)

    scope.stop()
  })
})
