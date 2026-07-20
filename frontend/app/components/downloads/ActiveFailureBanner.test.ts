/**
 * ActiveFailureBanner — the Active-tab "waiting, not up to date" banner.
 *
 * Pins: (1) each half renders its count and pluralizes; (2) each half hides at 0;
 * (3) the two links emit distinct intents.
 *
 * Non-vacuous: drop a v-if and a hidden-at-0 assertion fails; drop a @click and its
 * emit assertion fails.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import ActiveFailureBanner from './ActiveFailureBanner.vue'

describe('ActiveFailureBanner', () => {
  it('renders both halves with counts and links', async () => {
    const wrapper = mount(ActiveFailureBanner, { props: { failing: 7, coolingDown: 2 } })
    expect(wrapper.text()).toContain('7 chapters failing')
    expect(wrapper.text()).toContain('2 sources cooling down')

    await wrapper.get('.banner__link--failed').trigger('click')
    await wrapper.get('.banner__link--cooling').trigger('click')
    expect(wrapper.emitted('view-failed')).toHaveLength(1)
    expect(wrapper.emitted('view-sources')).toHaveLength(1)
  })

  it('hides the cooling-down half at 0 and singularizes', () => {
    const wrapper = mount(ActiveFailureBanner, { props: { failing: 1, coolingDown: 0 } })
    expect(wrapper.text()).toContain('1 chapter failing')
    expect(wrapper.find('.banner__link--cooling').exists()).toBe(false)
  })

  it('hides the failing half at 0', () => {
    const wrapper = mount(ActiveFailureBanner, { props: { failing: 0, coolingDown: 3 } })
    expect(wrapper.find('.banner__link--failed').exists()).toBe(false)
    expect(wrapper.text()).toContain('3 sources cooling down')
  })
})
