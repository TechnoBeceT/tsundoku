/**
 * readerChrome.logic — the centre-tap decision for the chrome toggle.
 *
 * Pins:
 *   1. A tap in the middle band toggles (true).
 *   2. A tap in the top / bottom edge band does NOT (false) — that's where the bars are.
 *   3. An unmeasured (0 / negative) viewport is never a centre tap.
 *   4. A custom edgeFraction moves the band.
 */
import { describe, it, expect } from 'vitest'
import { isCenterTap } from './readerChrome.logic'

describe('isCenterTap', () => {
  it('returns true for a tap in the centre band', () => {
    expect(isCenterTap(500, 1000)).toBe(true) // dead centre
    expect(isCenterTap(300, 1000)).toBe(true) // inside the 22%..78% band
  })

  it('returns false for a tap in the top edge band', () => {
    expect(isCenterTap(50, 1000)).toBe(false) // above 220px
  })

  it('returns false for a tap in the bottom edge band', () => {
    expect(isCenterTap(950, 1000)).toBe(false) // below 780px
  })

  it('treats the exact band boundaries as centre (inclusive)', () => {
    expect(isCenterTap(220, 1000)).toBe(true)
    expect(isCenterTap(780, 1000)).toBe(true)
  })

  it('is never a centre tap for a non-positive viewport height', () => {
    expect(isCenterTap(0, 0)).toBe(false)
    expect(isCenterTap(100, -10)).toBe(false)
  })

  it('honours a custom edge fraction', () => {
    // 40% edges → centre band is 400..600; 300 now sits in the edge.
    expect(isCenterTap(300, 1000, 0.4)).toBe(false)
    expect(isCenterTap(500, 1000, 0.4)).toBe(true)
  })
})
