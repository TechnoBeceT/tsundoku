/**
 * useEngineStatus — source-strip mapping + the polite poll lifecycle.
 *
 * Pins:
 *   1. The initial fetch maps the SourceStatus DTO → screen SourceStatus (1:1).
 *   2. It re-polls on the interval while visible.
 *   3. Hiding the tab (visibilitychange → hidden) PAUSES polling; showing it again
 *      resumes with an immediate freshening fetch.
 *   4. Scope dispose (component unmount / effectScope stop) stops polling — no
 *      further fetches, no leaked interval.
 *   5. A failed fetch surfaces in `error` without blanking the last-known list.
 *
 * Non-vacuous: if the interval never armed, assertion 2's count would stay at 1; if
 * dispose didn't clear the interval, assertion 4 would keep counting up.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { effectScope } from 'vue'
import { useEngineStatus } from './useEngineStatus'

const RAW = [
  { sourceKey: 'Asura Scans', state: 'downloading', activeCount: 5, cap: 5, cooldownRemainingSeconds: 0, reason: '', consecutiveFailures: 0, lastError: '' },
  { sourceKey: 'Comix', state: 'cooling', activeCount: 0, cap: 5, cooldownRemainingSeconds: 720, reason: 'rate_limit', consecutiveFailures: 6, lastError: '429 rate limit exceeded' },
]

let getCount = 0
let getError = false

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string) => {
      if (path === '/api/engine/sources') {
        getCount++
        if (getError) return Promise.resolve({ data: null, error: { message: 'boom' } })
        return Promise.resolve({ data: RAW, error: null })
      }
      return Promise.resolve({ data: null, error: null })
    }),
    POST: vi.fn(), PATCH: vi.fn(), DELETE: vi.fn(), PUT: vi.fn(), use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

const flush = async (): Promise<void> => { await Promise.resolve(); await Promise.resolve() }

// Drive document.visibilityState + dispatch the visibilitychange event.
function setVisibility(state: 'visible' | 'hidden'): void {
  Object.defineProperty(document, 'visibilityState', { value: state, configurable: true })
  document.dispatchEvent(new Event('visibilitychange'))
}

describe('useEngineStatus', () => {
  beforeEach(() => {
    getCount = 0
    getError = false
    setVisibility('visible')
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('fetches immediately + maps the DTO, then re-polls on the interval', async () => {
    const scope = effectScope()
    const s = scope.run(() => useEngineStatus({ intervalMs: 4000 }))!
    await flush()

    expect(getCount).toBe(1)
    expect(s.sources.value).toHaveLength(2)
    expect(s.sources.value[0]).toEqual(RAW[0]) // 1:1 mapping, no dropped field
    expect(s.error.value).toBeNull()

    vi.advanceTimersByTime(4000)
    expect(getCount).toBe(2)
    vi.advanceTimersByTime(4000)
    expect(getCount).toBe(3)

    scope.stop()
  })

  it('pauses polling while hidden and resumes (with a fetch) when shown', async () => {
    const scope = effectScope()
    scope.run(() => useEngineStatus({ intervalMs: 4000 }))!
    await flush()
    expect(getCount).toBe(1)

    // Hide → the interval is torn down; no more polls.
    setVisibility('hidden')
    vi.advanceTimersByTime(12_000)
    expect(getCount).toBe(1)

    // Show → an immediate freshening fetch, then polling resumes.
    setVisibility('visible')
    expect(getCount).toBe(2)
    vi.advanceTimersByTime(4000)
    expect(getCount).toBe(3)

    scope.stop()
  })

  it('stops polling on scope dispose (no leaked interval)', async () => {
    const scope = effectScope()
    scope.run(() => useEngineStatus({ intervalMs: 4000 }))!
    await flush()
    expect(getCount).toBe(1)

    scope.stop()
    vi.advanceTimersByTime(20_000)
    expect(getCount).toBe(1) // disposed → the interval no longer fires
  })

  it('surfaces a failed fetch in error without blanking the list', async () => {
    const scope = effectScope()
    const s = scope.run(() => useEngineStatus({ intervalMs: 4000 }))!
    await flush()
    expect(s.sources.value).toHaveLength(2)

    getError = true
    vi.advanceTimersByTime(4000)
    await flush()
    expect(s.error.value).toBe('Failed to load engine sources')
    expect(s.sources.value).toHaveLength(2) // last-known list kept

    scope.stop()
  })
})
