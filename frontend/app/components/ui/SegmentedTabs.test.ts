import { defineComponent, nextTick, ref } from 'vue'
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import SegmentedTabs from './SegmentedTabs.vue'
import type { TabItem } from './nav.types'

const linkedTabs = [
  { key: 'scheduling', label: 'Scheduling', id: 'scheduling-tab', panelId: 'scheduling-panel' },
  { key: 'protection', label: 'Protection', id: 'protection-tab', panelId: 'protection-panel' },
  { key: 'routing', label: 'Routing', id: 'routing-tab', panelId: 'routing-panel' },
] satisfies TabItem[]

function mountInteractiveTabs() {
  const Host = defineComponent({
    components: { SegmentedTabs },
    setup() {
      return { active: ref('scheduling'), tabs: linkedTabs }
    },
    template: '<SegmentedTabs v-model="active" :tabs="tabs" accessible-label="Download engine sections" />',
  })

  return mount(Host, { attachTo: document.body })
}

describe('SegmentedTabs', () => {
  it('preserves the key-label-count caller contract and existing update event', async () => {
    const wrapper = mount(SegmentedTabs, {
      props: {
        modelValue: 'active',
        tabs: [
          { key: 'active', label: 'Active', count: 3 },
          { key: 'failed', label: 'Failed' },
        ],
      },
    })

    await wrapper.findAll('[role="tab"]')[1]!.trigger('click')

    expect(wrapper.emitted('update:modelValue')).toEqual([['failed']])
    expect(wrapper.get('[role="tablist"]').attributes('aria-label')).toBeUndefined()
  })

  it('links tabs to panels and exposes a tablist label when metadata is provided', () => {
    const wrapper = mountInteractiveTabs()
    const tabs = wrapper.findAll('[role="tab"]')

    expect(wrapper.get('[role="tablist"]').attributes('aria-label')).toBe('Download engine sections')
    expect(tabs.map(tab => tab.attributes('id'))).toEqual([
      'scheduling-tab',
      'protection-tab',
      'routing-tab',
    ])
    expect(tabs.map(tab => tab.attributes('aria-controls'))).toEqual([
      'scheduling-panel',
      'protection-panel',
      'routing-panel',
    ])
    wrapper.unmount()
  })

  it('uses a roving tabindex and moves activation and focus with wrapping arrow keys', async () => {
    const wrapper = mountInteractiveTabs()
    let tabs = wrapper.findAll<HTMLButtonElement>('[role="tab"]')

    expect(tabs.map(tab => tab.attributes('tabindex'))).toEqual(['0', '-1', '-1'])
    await tabs[0]!.trigger('keydown', { key: 'ArrowLeft' })
    await nextTick()
    tabs = wrapper.findAll<HTMLButtonElement>('[role="tab"]')
    expect(tabs.map(tab => tab.attributes('tabindex'))).toEqual(['-1', '-1', '0'])
    expect(tabs[2]!.element).toBe(document.activeElement)

    await tabs[2]!.trigger('keydown', { key: 'ArrowRight' })
    await nextTick()
    tabs = wrapper.findAll<HTMLButtonElement>('[role="tab"]')
    expect(tabs.map(tab => tab.attributes('tabindex'))).toEqual(['0', '-1', '-1'])
    expect(tabs[0]!.element).toBe(document.activeElement)
    wrapper.unmount()
  })

  it('moves activation and focus to the first and last tabs with Home and End', async () => {
    const wrapper = mountInteractiveTabs()
    let tabs = wrapper.findAll<HTMLButtonElement>('[role="tab"]')

    await tabs[0]!.trigger('keydown', { key: 'End' })
    await nextTick()
    tabs = wrapper.findAll<HTMLButtonElement>('[role="tab"]')
    expect(tabs[2]!.attributes('aria-selected')).toBe('true')
    expect(tabs[2]!.element).toBe(document.activeElement)

    await tabs[2]!.trigger('keydown', { key: 'Home' })
    await nextTick()
    tabs = wrapper.findAll<HTMLButtonElement>('[role="tab"]')
    expect(tabs[0]!.attributes('aria-selected')).toBe('true')
    expect(tabs[0]!.element).toBe(document.activeElement)
    wrapper.unmount()
  })
})
