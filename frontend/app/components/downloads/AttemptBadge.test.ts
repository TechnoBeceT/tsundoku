/**
 * AttemptBadge — the per-source retry-budget pill.
 *
 * Pins: (1) it renders "‹provider› · N/max"; (2) it tints by budget state
 * (fresh / trying / exhausted).
 *
 * Non-vacuous: drop the count interpolation and the text assertion fails; hardcode
 * one tone and the tone assertions fail.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import AttemptBadge from './AttemptBadge.vue'

describe('AttemptBadge', () => {
  it('renders the source and the N/max budget', () => {
    const wrapper = mount(AttemptBadge, { props: { provider: 'MangaDex', attempts: 1, max: 5 } })
    expect(wrapper.text()).toContain('MangaDex')
    expect(wrapper.text()).toContain('1/5')
  })

  it('tints fresh / trying / exhausted by budget state', () => {
    expect(mount(AttemptBadge, { props: { provider: 'S', attempts: 0, max: 3 } }).classes()).toContain('attempts--fresh')
    expect(mount(AttemptBadge, { props: { provider: 'S', attempts: 1, max: 3 } }).classes()).toContain('attempts--trying')
    expect(mount(AttemptBadge, { props: { provider: 'S', attempts: 3, max: 3 } }).classes()).toContain('attempts--exhausted')
  })
})
