/**
 * DisclosurePanel — pins the accessible open/close contract the app's long lists
 * rely on (QCAT-265 treatment #2).
 *
 * Non-vacuous: the trigger must be a REAL button carrying `aria-expanded` +
 * `aria-controls` that addresses the body, clicking must flip both the state
 * and the emit, and a non-collapsible panel must render no trigger at all.
 */
import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import DisclosurePanel from './DisclosurePanel.vue'

const body = { default: '<p class="body-probe">rows</p>' }

describe('DisclosurePanel', () => {
  it('renders no trigger when it is not collapsible', () => {
    const wrapper = mount(DisclosurePanel, { props: { title: 'Sources' }, slots: body })

    expect(wrapper.find('button.dp__trigger').exists()).toBe(false)
    expect(wrapper.find('.body-probe').exists()).toBe(true)
  })

  it('exposes an aria-expanded trigger that controls the body region', () => {
    const wrapper = mount(DisclosurePanel, { props: { title: 'Chapters', collapsible: true }, slots: body })

    const trigger = wrapper.get('button.dp__trigger')
    expect(trigger.attributes('aria-expanded')).toBe('true')
    expect(trigger.attributes('aria-controls')).toBe(wrapper.get('.dp__content').attributes('id'))
  })

  it('hides the body and flips aria-expanded when the trigger is clicked', async () => {
    const wrapper = mount(DisclosurePanel, { props: { title: 'Chapters', collapsible: true }, slots: body })

    await wrapper.get('button.dp__trigger').trigger('click')

    expect(wrapper.get('button.dp__trigger').attributes('aria-expanded')).toBe('false')
    // v-show keeps the body mounted (state survives a collapse) but hidden.
    expect(wrapper.get('.dp__content').attributes('style')).toContain('display: none')
    expect(wrapper.emitted('update:open')).toEqual([[false]])
  })

  it('starts collapsed when defaultOpen is false, and opens on click', async () => {
    const wrapper = mount(DisclosurePanel, {
      props: { title: 'Chapters', collapsible: true, defaultOpen: false },
      slots: body,
    })

    expect(wrapper.get('button.dp__trigger').attributes('aria-expanded')).toBe('false')

    await wrapper.get('button.dp__trigger').trigger('click')

    expect(wrapper.get('button.dp__trigger').attributes('aria-expanded')).toBe('true')
    expect(wrapper.emitted('update:open')).toEqual([[true]])
  })

  it('renders the count badge for a zero count and hides it when absent', () => {
    const zero = mount(DisclosurePanel, { props: { title: 'Chapters', count: 0 }, slots: body })
    expect(zero.get('.dp__count').text()).toBe('0')

    const none = mount(DisclosurePanel, { props: { title: 'Chapters' }, slots: body })
    expect(none.find('.dp__count').exists()).toBe(false)
  })

  it('keeps a controlled panel on the host value and ignores its own clicks', async () => {
    const wrapper = mount(DisclosurePanel, {
      props: { title: 'Chapters', collapsible: true, open: false },
      slots: body,
    })

    await wrapper.get('button.dp__trigger').trigger('click')

    // The host owns the state: until it responds to the emit, nothing moves.
    expect(wrapper.get('button.dp__trigger').attributes('aria-expanded')).toBe('false')
    expect(wrapper.emitted('update:open')).toEqual([[true]])
  })
})
