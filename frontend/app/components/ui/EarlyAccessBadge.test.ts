/**
 * EarlyAccessBadge — the "not published free yet" pill.
 *
 * Pins the two things that make it read as WAITING rather than FAILED: the wording
 * says early access (never "failed"/"error"), and the live "free ~Nd" countdown is
 * derived client-side from the backend's raw `until` timestamp.
 *
 * Non-vacuous: drop the `until` branch and the countdown assertion fails; swap the
 * label wording and the first assertion fails.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import EarlyAccessBadge from './EarlyAccessBadge.vue'

describe('EarlyAccessBadge', () => {
  it('reads as an early-access wait with a live countdown, never as a failure', () => {
    const until = new Date(Date.now() + 3 * 24 * 60 * 60_000).toISOString()
    const wrapper = mount(EarlyAccessBadge, { props: { until } })

    const text = wrapper.text()
    expect(text).toContain('Early access')
    expect(text).toContain('free ~3d')
    expect(text.toLowerCase()).not.toContain('fail')
    expect(text.toLowerCase()).not.toContain('error')
    wrapper.unmount()
  })

  it('drops the countdown when the backend gave no expiry', () => {
    const wrapper = mount(EarlyAccessBadge)
    expect(wrapper.text()).toContain('Early access')
    expect(wrapper.text()).not.toContain('free')
    wrapper.unmount()
  })

  it("surfaces the source's own message as the tooltip", () => {
    const wrapper = mount(EarlyAccessBadge, {
      props: { reason: 'Chapter locked, coins required' },
    })
    expect(wrapper.find('.early').attributes('title')).toBe('Chapter locked, coins required')
    wrapper.unmount()
  })
})
