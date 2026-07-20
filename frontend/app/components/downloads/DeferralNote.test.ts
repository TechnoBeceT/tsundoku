/**
 * DeferralNote — the waiting pill states WHO is being waited on and WHEN it retries.
 *
 * Pins that the note reads "waiting on <source> · retry ~Nm" (ETA derived
 * client-side from deferredUntil) and that the reason rides in the title tooltip.
 *
 * Non-vacuous: drop the `source` interpolation and the first assertion fails; drop
 * the `:title="reason"` bind and the tooltip assertion fails.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import DeferralNote from './DeferralNote.vue'

describe('DeferralNote', () => {
  it('names the waited-on source with a live retry ETA and a reason tooltip', () => {
    const deferredUntil = new Date(Date.now() + 12 * 60_000).toISOString()
    const wrapper = mount(DeferralNote, {
      props: { deferredUntil, source: 'Asura Scans', reason: 'Cloudflare challenge failed (403)' },
    })

    const text = wrapper.text()
    expect(text).toContain('waiting on')
    expect(text).toContain('Asura Scans')
    expect(text).toContain('retry ~12m')
    expect(wrapper.find('.defer').attributes('title')).toBe('Cloudflare challenge failed (403)')
    wrapper.unmount()
  })

  it('omits the tooltip when there is no reason', () => {
    const deferredUntil = new Date(Date.now() + 60 * 60_000).toISOString()
    const wrapper = mount(DeferralNote, {
      props: { deferredUntil, source: 'MangaDex' },
    })
    expect(wrapper.find('.defer').attributes('title')).toBeUndefined()
    wrapper.unmount()
  })

  it('reads "cooling down" wording for a tripped breaker', () => {
    const deferredUntil = new Date(Date.now() + 15 * 60_000).toISOString()
    const wrapper = mount(DeferralNote, {
      props: { deferredUntil, source: 'Asura Scans', reason: 'rate limited', reasonKind: 'cooling_down' },
    })
    const text = wrapper.text()
    expect(text).toContain('waiting on')
    expect(text).toContain('Asura Scans')
    expect(text).toContain('cooling down')
    expect(text).toContain('retry ~15m')
    expect(wrapper.find('.defer').classes()).toContain('defer--cooling')
    wrapper.unmount()
  })

  it('reads "retrying" wording for a per-chapter backoff, with the source in the tooltip', () => {
    const deferredUntil = new Date(Date.now() + 4 * 60_000).toISOString()
    const wrapper = mount(DeferralNote, {
      props: { deferredUntil, source: 'MangaDex', reason: 'connection reset', reasonKind: 'backoff' },
    })
    const text = wrapper.text()
    expect(text).toContain('retrying ~4m')
    // Source is not in the visible text for a backoff — it rides in the tooltip.
    expect(text).not.toContain('waiting on')
    expect(wrapper.find('.defer').attributes('title')).toContain('MangaDex')
    wrapper.unmount()
  })
})
