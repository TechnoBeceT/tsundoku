/**
 * DownloadCounter — the persistent nav trio renders all three counts and links out.
 *
 * Pins: (1) the three counts (downloading / queued / failed) all render even at 0;
 * (2) clicking the counter emits `navigate` (the parent routes to Downloads).
 *
 * Non-vacuous: drop a count row and the first assertion (three rows) fails; drop the
 * @click and the emit assertion fails.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import DownloadCounter from './DownloadCounter.vue'

describe('DownloadCounter', () => {
  it('renders all three counts, including zeros', () => {
    const wrapper = mount(DownloadCounter, { props: { downloading: 3, queued: 0, failed: 2 } })
    const rows = wrapper.findAll('.counter__row')
    expect(rows).toHaveLength(3)
    expect(wrapper.text()).toContain('3')
    expect(wrapper.text()).toContain('0')
    expect(wrapper.text()).toContain('2')
    // The zero (queued) row is present but marked muted, not removed.
    expect(wrapper.find('.counter__row--queued').classes()).toContain('counter__row--zero')
  })

  it('emits navigate when clicked', async () => {
    const wrapper = mount(DownloadCounter, { props: { downloading: 1, queued: 1, failed: 1 } })
    await wrapper.get('.counter').trigger('click')
    expect(wrapper.emitted('navigate')).toHaveLength(1)
  })
})
