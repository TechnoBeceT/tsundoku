/**
 * CycleBanner — the pill states the engine's real state, and is HONEST about a
 * deferred queue.
 *
 * Pins the branches that carry a claim: a deferred queue shows "N waiting on a
 * source · retry ~Nm" instead of a bare countdown; a running cycle always wins over
 * the deferral summary; an OVERRUNNING cycle says the next run is already due
 * (back-to-back cycles) rather than showing 0:00; "not scheduled" and "unavailable"
 * are worded as the different claims they are.
 *
 * Non-vacuous: drop the `deferralSummary` branch and the first assertion falls back
 * to the schedule line; collapse `overrunning` into `running` and the fourth fails.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import CycleBanner from './CycleBanner.vue'

const stubs = { Spinner: true }

describe('CycleBanner', () => {
  it('shows the honest waiting summary when the queue is deferred', () => {
    const wrapper = mount(CycleBanner, {
      props: {
        cycle: { state: 'waiting', remainingMs: 600_000 },
        deferralSummary: { count: 7, soonestIso: new Date(Date.now() + 18 * 60_000).toISOString() },
      },
      global: { stubs },
    })
    const text = wrapper.text()
    expect(text).toContain('7 waiting on a source')
    expect(text).toContain('retry ~18m')
    expect(text).not.toContain('Next download cycle')
  })

  it('counts down to the next cycle when nothing is deferred', () => {
    const wrapper = mount(CycleBanner, {
      props: { cycle: { state: 'waiting', remainingMs: 83_000 }, deferralSummary: null },
      global: { stubs },
    })
    expect(wrapper.text()).toContain('Next download cycle 1:23')
  })

  it('shows the in-progress label while a cycle runs, even with a deferred queue', () => {
    const wrapper = mount(CycleBanner, {
      props: {
        cycle: { state: 'running', remainingMs: null },
        deferralSummary: { count: 3, soonestIso: new Date(Date.now() + 60_000).toISOString() },
      },
      global: { stubs },
    })
    expect(wrapper.text()).toContain('Download cycle in progress')
    expect(wrapper.text()).not.toContain('waiting on a source')
  })

  it('says the next run is already due when the cycle is overrunning', () => {
    const wrapper = mount(CycleBanner, {
      props: { cycle: { state: 'overrunning', remainingMs: null } },
      global: { stubs },
    })
    expect(wrapper.text()).toContain('Download cycle in progress · next due now')
    expect(wrapper.text()).not.toContain('0:00')
  })

  it('distinguishes an unscheduled loop from an unreadable schedule', () => {
    const unscheduled = mount(CycleBanner, {
      props: { cycle: { state: 'unscheduled', remainingMs: null } },
      global: { stubs },
    })
    expect(unscheduled.text()).toContain('Download cycle not scheduled')

    const unavailable = mount(CycleBanner, { props: { cycle: null }, global: { stubs } })
    expect(unavailable.text()).toContain('Cycle schedule unavailable')
  })
})
