/**
 * countdown helpers — formatCountdown (m:ss / h:mm:ss) + formatCompactDuration.
 * Pins the header timer format from the mockup ("0:43", "1:52:08") and the strip's
 * compact cooling label ("12m", "45s"), plus the negative-clamp guard so an
 * over-run timer never renders a wrong-looking negative.
 */
import { describe, it, expect } from 'vitest'
import { formatCountdown, formatCompactDuration } from './countdown'

describe('formatCountdown', () => {
  it('formats sub-hour durations as M:SS', () => {
    expect(formatCountdown(43_000)).toBe('0:43')
    expect(formatCountdown(63_000)).toBe('1:03')
    expect(formatCountdown(600_000)).toBe('10:00')
  })

  it('formats hour-plus durations as H:MM:SS', () => {
    expect(formatCountdown(6_728_000)).toBe('1:52:08')
    expect(formatCountdown(3_600_000)).toBe('1:00:00')
  })

  it('clamps a negative (over-run) duration to 0:00', () => {
    expect(formatCountdown(-5_000)).toBe('0:00')
  })
})

describe('formatCompactDuration', () => {
  it('picks the largest whole unit', () => {
    expect(formatCompactDuration(720)).toBe('12m')
    expect(formatCompactDuration(240)).toBe('4m')
    expect(formatCompactDuration(45)).toBe('45s')
    expect(formatCompactDuration(7200)).toBe('2h')
  })

  it('clamps negatives to 0s', () => {
    expect(formatCompactDuration(-10)).toBe('0s')
  })
})
