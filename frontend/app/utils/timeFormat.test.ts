/**
 * timeFormat — relative + absolute timestamps and the duration formatter.
 *
 * Pins the relative buckets against a fixed `now`, the absent-value handling, and
 * the duration thresholds. Non-vacuous: shift a threshold and its case fails.
 */
import { describe, it, expect } from 'vitest'
import { relativeTime, absoluteTime, formatDurationMs } from './timeFormat'

const NOW = Date.parse('2026-07-19T12:00:00Z')
const ago = (ms: number): string => new Date(NOW - ms).toISOString()

describe('relativeTime', () => {
  it('returns "never" for null/empty/unparseable', () => {
    expect(relativeTime(null, NOW)).toBe('never')
    expect(relativeTime('', NOW)).toBe('never')
    expect(relativeTime('not-a-date', NOW)).toBe('never')
  })

  it('buckets recent times', () => {
    expect(relativeTime(ago(10_000), NOW)).toBe('just now')
    expect(relativeTime(ago(5 * 60_000), NOW)).toBe('5m ago')
    expect(relativeTime(ago(3 * 3_600_000), NOW)).toBe('3h ago')
    expect(relativeTime(ago(2 * 86_400_000), NOW)).toBe('2d ago')
  })

  it('falls back to an absolute date beyond the 30-day window', () => {
    const out = relativeTime(ago(40 * 86_400_000), NOW)
    expect(out).not.toContain('ago')
    expect(out).not.toBe('never')
  })

  it('reads a slightly-future timestamp as "just now" (clock skew)', () => {
    expect(relativeTime(new Date(NOW + 5_000).toISOString(), NOW)).toBe('just now')
  })
})

describe('absoluteTime', () => {
  it('returns "—" for absent/unparseable values', () => {
    expect(absoluteTime(null)).toBe('—')
    expect(absoluteTime('')).toBe('—')
    expect(absoluteTime('nope')).toBe('—')
  })

  it('renders a real date to a non-empty string', () => {
    expect(absoluteTime('2026-07-19T12:00:00Z')).not.toBe('—')
  })
})

describe('formatDurationMs', () => {
  it('formats across the thresholds', () => {
    expect(formatDurationMs(0)).toBe('—')
    expect(formatDurationMs(-5)).toBe('—')
    expect(formatDurationMs(320)).toBe('320ms')
    expect(formatDurationMs(1500)).toBe('1.5s')
    expect(formatDurationMs(65_000)).toBe('1m 5s')
  })
})
