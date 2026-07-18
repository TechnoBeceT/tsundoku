/**
 * useNow — the shared ticking clock.
 *
 * Pins that the singleton `now` ref advances on its interval (so live "retry ~Nm"
 * labels recompute without a refetch) and that a single interval is shared across
 * consumers and torn down when the last scope disposes (no leak).
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { effectScope } from 'vue'
import { useNow, TICK_MS } from './useNow'

describe('useNow', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('advances now on each tick', () => {
    const scope = effectScope()
    const now = scope.run(() => useNow().now)!
    const before = now.value
    vi.advanceTimersByTime(TICK_MS)
    expect(now.value).toBeGreaterThan(before)
    scope.stop()
  })

  it('shares one interval and stops it when the last consumer disposes', () => {
    const setSpy = vi.spyOn(globalThis, 'setInterval')
    const clearSpy = vi.spyOn(globalThis, 'clearInterval')

    const a = effectScope()
    const b = effectScope()
    a.run(() => useNow())
    b.run(() => useNow())
    // Two consumers, but only ONE interval armed.
    expect(setSpy).toHaveBeenCalledTimes(1)

    a.stop()
    expect(clearSpy).not.toHaveBeenCalled() // b still holds it
    b.stop()
    expect(clearSpy).toHaveBeenCalledTimes(1) // last one out clears it
  })
})
