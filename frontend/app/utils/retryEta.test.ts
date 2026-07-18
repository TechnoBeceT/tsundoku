/**
 * formatRetryEta — the client-side "time until retry" label for a deferred source.
 *
 * Pins the four branches (elapsed / seconds / minutes / hours) and the "~" estimate
 * marker, all against a FIXED now so the assertions are deterministic (the real UI
 * passes the live ticking clock).
 */
import { describe, it, expect } from 'vitest'
import { formatRetryEta } from './retryEta'

const NOW = new Date('2026-07-18T12:00:00.000Z').getTime()
const at = (ms: number): string => new Date(NOW + ms).toISOString()

describe('formatRetryEta', () => {
  it('reads "now" once the cooldown has elapsed (past or exactly now)', () => {
    expect(formatRetryEta(at(-5_000), NOW)).toBe('now')
    expect(formatRetryEta(at(0), NOW)).toBe('now')
  })

  it('reads seconds under a minute', () => {
    expect(formatRetryEta(at(40_000), NOW)).toBe('~40s')
  })

  it('reads minutes under an hour', () => {
    expect(formatRetryEta(at(12 * 60_000), NOW)).toBe('~12m')
  })

  it('reads hours at an hour or more', () => {
    expect(formatRetryEta(at(2 * 3_600_000), NOW)).toBe('~2h')
  })
})
