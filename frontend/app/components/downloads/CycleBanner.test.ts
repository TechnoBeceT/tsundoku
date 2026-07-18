/**
 * CycleBanner — the pill is HONEST about a deferred queue.
 *
 * Pins the three idle branches: a deferred-queue summary shows "N waiting on a
 * source · retry ~Nm" (never the misleading "Idle" line), a known interval shows
 * the countdown, and neither shows the plain idle text. A running cycle always wins.
 *
 * Non-vacuous: drop the `deferralSummary` branch from the label computed and the
 * first assertion falls back to "Idle — waiting for next cycle" and fails.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import CycleBanner from './CycleBanner.vue'

const stubs = { Spinner: true }

describe('CycleBanner', () => {
  it('shows the honest waiting summary when the queue is deferred', () => {
    const wrapper = mount(CycleBanner, {
      props: {
        cycleActive: false,
        deferralSummary: { count: 7, soonestIso: new Date(Date.now() + 18 * 60_000).toISOString() },
      },
      global: { stubs },
    })
    const text = wrapper.text()
    expect(text).toContain('7 waiting on a source')
    expect(text).toContain('retry ~18m')
    expect(text).not.toContain('Idle')
  })

  it('falls back to the plain idle line when nothing is deferred and no interval is known', () => {
    const wrapper = mount(CycleBanner, {
      props: { cycleActive: false, deferralSummary: null, nextCycleMinutes: null },
      global: { stubs },
    })
    expect(wrapper.text()).toContain('Idle — waiting for next cycle')
  })

  it('shows the in-progress label while a cycle runs, even with a deferred queue', () => {
    const wrapper = mount(CycleBanner, {
      props: {
        cycleActive: true,
        deferralSummary: { count: 3, soonestIso: new Date(Date.now() + 60_000).toISOString() },
      },
      global: { stubs },
    })
    expect(wrapper.text()).toContain('Download cycle in progress')
  })
})
