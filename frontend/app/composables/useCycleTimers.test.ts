/**
 * useCycleTimers — the header's two cycle states, read from the SERVER's schedule.
 *
 * Pins the behaviour the client used to get wrong (GAP-115):
 *   1. A tab that loads MID-CYCLE reports the cycle as running — the bug that put
 *      "Idle" beside a visibly downloading source strip.
 *   2. Countdowns come from the endpoint's nextRunAt, not from an interval the
 *      client re-derives, and are measured against the SERVER clock: a browser
 *      clock minutes out still shows the right remaining time.
 *   3. Every contract state maps to its own UI state: overrunning (running with an
 *      already-due next run) · waiting · unscheduled (nextRunAt null) · unavailable
 *      (endpoint unreachable).
 *   4. SSE is the liveness signal: a cycle.start / cycle.done edge flips `running`
 *      immediately and triggers a re-read, and a response that pre-dates the edge
 *      can never resurrect the state the edge just superseded.
 *   5. The 1s ticker decrements the countdown between reads.
 *
 * Non-vacuous: drop the mount read and (1) reports 'unavailable'; ignore serverTime
 * and (2) is off by the injected skew; let an edge-triggered response write
 * `running` and (4) flips back to running.
 *
 * Uses the same FakeEventSource stub as the other stream tests, and fake timers for
 * both the countdown clock and the clock-skew arithmetic.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { effectScope, nextTick } from 'vue'
import { useCycleTimers } from './useCycleTimers'
import { useProgressStream } from './useProgressStream'
import type { components } from '~/utils/api/schema.d.ts'

type EngineScheduleDTO = components['schemas']['EngineSchedule']
type CycleScheduleDTO = components['schemas']['CycleSchedule']

// The browser clock every test starts from (vi.setSystemTime).
const CLIENT_NOW = Date.parse('2026-07-25T12:00:00Z')

/** Build a schedule response. `serverNow` defaults to the browser clock (no skew). */
function schedule(
  download: CycleScheduleDTO,
  refresh: CycleScheduleDTO = idle(),
  serverNow: number = CLIENT_NOW,
): EngineScheduleDTO {
  return { download, refresh, serverTime: new Date(serverNow).toISOString() }
}

/** A loop waiting `ms` for its next run, measured from the given server instant. */
function waiting(ms: number, serverNow: number = CLIENT_NOW): CycleScheduleDTO {
  return { running: false, nextRunAt: new Date(serverNow + ms).toISOString(), overdue: false }
}

/** A loop with a next run one hour out — the "nothing to see here" filler. */
const idle = (): CycleScheduleDTO => waiting(3_600_000)

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

// ── apiClient mock (the schedule endpoint) ─────────────────────────────────────
// Each test sets `nextResponse` before the read it wants to shape; `getCalls`
// pins that the SSE edges actually trigger a re-read.
let nextResponse: { data: EngineScheduleDTO | null, error: unknown } = { data: null, error: null }
let getCalls = 0

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string) => {
      if (path !== '/api/engine/schedule') return Promise.resolve({ data: null, error: null })
      getCalls += 1
      return Promise.resolve(nextResponse)
    }),
    POST: vi.fn(), PATCH: vi.fn(), DELETE: vi.fn(), PUT: vi.fn(), use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

/** Let the in-flight fetch chain settle (mocked promises resolve in microtasks). */
const flush = async (): Promise<void> => {
  await Promise.resolve()
  await Promise.resolve()
  await nextTick()
}

/** Mount the composable in its own scope and settle the mount read. */
async function mountTimers() {
  const scope = effectScope()
  const timers = scope.run(() => useCycleTimers())!
  await flush()
  return { timers, stop: () => scope.stop() }
}

describe('useCycleTimers', () => {
  beforeEach(() => {
    vi.stubGlobal('EventSource', FakeEventSource)
    vi.useFakeTimers()
    vi.setSystemTime(CLIENT_NOW)
    stubSource = null
    getCalls = 0
    nextResponse = { data: schedule(waiting(43_000)), error: null }
  })
  afterEach(() => {
    // Reset the useProgressStream module singleton so the next test's connect()
    // creates a fresh FakeEventSource (connect is idempotent while one is open).
    useProgressStream().disconnect()
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('seeds the countdown from the endpoint on mount', async () => {
    const { timers, stop } = await mountTimers()
    expect(timers.download.value).toEqual({ state: 'waiting', remainingMs: 43_000 })
    stop()
  })

  it('reports a cycle that was ALREADY running when the page loaded', async () => {
    // The mid-cycle reload: no SSE cycle.start will ever arrive for this cycle.
    nextResponse = {
      data: schedule({ running: true, nextRunAt: new Date(CLIENT_NOW + 30_000).toISOString(), overdue: false }),
      error: null,
    }
    const { timers, stop } = await mountTimers()
    expect(timers.download.value.state).toBe('running')
    stop()
  })

  it('reports an overrunning cycle (back-to-back) without a countdown', async () => {
    nextResponse = {
      data: schedule({ running: true, nextRunAt: new Date(CLIENT_NOW - 23_000).toISOString(), overdue: true }),
      error: null,
    }
    const { timers, stop } = await mountTimers()
    expect(timers.download.value).toEqual({ state: 'overrunning', remainingMs: null })
    stop()
  })

  it('reports an unscheduled loop', async () => {
    nextResponse = {
      data: schedule({ running: false, nextRunAt: null, overdue: false }),
      error: null,
    }
    const { timers, stop } = await mountTimers()
    expect(timers.download.value).toEqual({ state: 'unscheduled', remainingMs: null })
    stop()
  })

  it('reports "unavailable" when the endpoint cannot be read — never a guess', async () => {
    nextResponse = { data: null, error: { message: 'boom' } }
    const { timers, stop } = await mountTimers()
    expect(timers.download.value).toEqual({ state: 'unavailable', remainingMs: null })
    expect(timers.refresh.value).toEqual({ state: 'unavailable', remainingMs: null })
    stop()
  })

  it('corrects for a skewed browser clock', async () => {
    // The browser runs 10 MINUTES ahead of the server. nextRunAt is 60s after the
    // SERVER's clock, so an uncorrected client would compute a long-past instant.
    const serverNow = CLIENT_NOW - 600_000
    nextResponse = { data: schedule(waiting(60_000, serverNow), idle(), serverNow), error: null }
    const { timers, stop } = await mountTimers()
    expect(timers.download.value).toEqual({ state: 'waiting', remainingMs: 60_000 })

    // …and it keeps ticking correctly against the corrected clock.
    vi.advanceTimersByTime(5_000)
    expect(timers.download.value.remainingMs).toBe(55_000)
    stop()
  })

  it('ticks the countdown down every second between reads', async () => {
    const { timers, stop } = await mountTimers()
    vi.advanceTimersByTime(3_000)
    expect(timers.download.value.remainingMs).toBe(40_000)
    stop()
  })

  it('flips to running on a cycle.start edge and re-reads the schedule', async () => {
    const { timers, stop } = await mountTimers()
    expect(timers.download.value.state).toBe('waiting')

    const before = getCalls
    // The server re-anchors nextRunAt on the cycle START, so the re-read is what
    // keeps an on-time cycle from looking overrun.
    nextResponse = {
      data: schedule({ running: true, nextRunAt: new Date(CLIENT_NOW + 90_000).toISOString(), overdue: false }),
      error: null,
    }
    stubSource!.fire('cycle.start', {})
    expect(timers.download.value.state).toBe('running') // immediate, before the fetch
    await flush()
    expect(getCalls).toBe(before + 1)
    expect(timers.download.value.state).toBe('running')
    stop()
  })

  it('re-reads on cycle.done and shows the server\'s new next-run instant', async () => {
    const { timers, stop } = await mountTimers()
    nextResponse = { data: schedule(waiting(90_000)), error: null }
    stubSource!.fire('cycle.done', {})
    await flush()
    expect(timers.download.value).toEqual({ state: 'waiting', remainingMs: 90_000 })
    stop()
  })

  it('keeps cycles back-to-back honest: done → immediately running again', async () => {
    const { timers, stop } = await mountTimers()

    // The cycle ends; the server's next-run instant is already in the past.
    nextResponse = {
      data: schedule({ running: false, nextRunAt: new Date(CLIENT_NOW - 23_000).toISOString(), overdue: true }),
      error: null,
    }
    stubSource!.fire('cycle.done', {})
    await flush()
    // "Due, between cycles" — NOT a 0:00 countdown.
    expect(timers.download.value).toEqual({ state: 'starting', remainingMs: null })

    // The next cycle starts immediately (zero idle) and is already due again.
    nextResponse = {
      data: schedule({ running: true, nextRunAt: new Date(CLIENT_NOW - 23_000).toISOString(), overdue: true }),
      error: null,
    }
    stubSource!.fire('cycle.start', {})
    await flush()
    expect(timers.download.value).toEqual({ state: 'overrunning', remainingMs: null })
    stop()
  })

  it('never lets an edge-triggered response resurrect the state the edge superseded', async () => {
    nextResponse = {
      data: schedule({ running: true, nextRunAt: new Date(CLIENT_NOW + 30_000).toISOString(), overdue: false }),
      error: null,
    }
    const { timers, stop } = await mountTimers()
    expect(timers.download.value.state).toBe('running')

    // cycle.done fires, but the server snapshot still reports the cycle as running
    // (the loop publishes its "not running" mark just after the event is emitted).
    stubSource!.fire('cycle.done', {})
    expect(timers.download.value.state).not.toBe('running')
    await flush()
    expect(timers.download.value.state).not.toBe('running')
    stop()
  })

  it('tracks the refresh loop independently of the download loop', async () => {
    nextResponse = { data: schedule(waiting(43_000), waiting(6_728_000)), error: null }
    const { timers, stop } = await mountTimers()
    expect(timers.refresh.value).toEqual({ state: 'waiting', remainingMs: 6_728_000 })

    stubSource!.fire('refresh.start', {})
    expect(timers.refresh.value.state).toBe('running')
    expect(timers.download.value.state).toBe('waiting')

    nextResponse = { data: schedule(waiting(43_000), waiting(7_200_000)), error: null }
    stubSource!.fire('refresh.done', {})
    await flush()
    expect(timers.refresh.value).toEqual({ state: 'waiting', remainingMs: 7_200_000 })
    stop()
  })
})
