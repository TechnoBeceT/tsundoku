/**
 * errorCategory — the category → {label, tone} display map.
 *
 * Pins the tone assignment (danger/warn/neutral) and the null/unmapped fallback.
 * Non-vacuous: flip a tone and its case fails.
 */
import { describe, it, expect } from 'vitest'
import { categoryMeta } from './errorCategory'

describe('categoryMeta', () => {
  it('assigns the right tone per category', () => {
    expect(categoryMeta('captcha')).toEqual({ label: 'Anti-bot', tone: 'danger' })
    expect(categoryMeta('rate_limit').tone).toBe('warn')
    expect(categoryMeta('timeout').tone).toBe('warn')
    expect(categoryMeta('server_error').tone).toBe('danger')
    expect(categoryMeta('network').tone).toBe('danger')
    expect(categoryMeta('parse').tone).toBe('danger')
    expect(categoryMeta('not_found').tone).toBe('neutral')
    expect(categoryMeta('no_pages').tone).toBe('neutral')
  })

  it('falls back to the neutral Unknown treatment for null/unmapped', () => {
    expect(categoryMeta(null)).toEqual({ label: 'Unknown', tone: 'neutral' })
    expect(categoryMeta(undefined)).toEqual({ label: 'Unknown', tone: 'neutral' })
    expect(categoryMeta('made_up')).toEqual({ label: 'Unknown', tone: 'neutral' })
  })
})
