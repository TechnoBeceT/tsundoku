/**
 * SourceFilterChips — the shared source-filter chip row extracted from the
 * Adopt wizard (`screens/Import.vue`) and reused by the Series-Detail
 * "Add a source" dialog. Pins the toggle contract: clicking a chip emits the
 * NEW `selected` array (the component holds no state of its own).
 *
 * Non-vacuous: the toggle must ADD an unselected id and REMOVE an already-
 * selected one — both directions asserted. A degraded (cooling-down) source is
 * marked (⚠ + `.imp-chip--degraded`) but stays SELECTABLE; a healthy source
 * shows no marker.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import SourceFilterChips from './SourceFilterChips.vue'

const sources = [
  { id: 'a', name: 'MangaDex' },
  { id: 'b', name: 'Asura Scans' },
]

describe('SourceFilterChips', () => {
  it('emits the id added to the selection when an unselected chip is clicked', async () => {
    const wrapper = mount(SourceFilterChips, { props: { sources, selected: [] } })

    await wrapper.findAll('button.imp-chip')[0]!.trigger('click')

    expect(wrapper.emitted('update:selected')).toEqual([[['a']]])
  })

  it('emits the id removed from the selection when an already-selected chip is clicked', async () => {
    const wrapper = mount(SourceFilterChips, { props: { sources, selected: ['a'] } })

    await wrapper.findAll('button.imp-chip')[0]!.trigger('click')

    expect(wrapper.emitted('update:selected')).toEqual([[[]]])
  })

  it('marks a degraded source (⚠ + reason tooltip) while leaving a healthy one unmarked', () => {
    const wrapper = mount(SourceFilterChips, {
      props: {
        sources: [
          { id: 'a', name: 'MangaDex' },
          { id: 'b', name: 'Asura Scans', degraded: true, degradedReason: 'Temporarily unavailable — 4 consecutive failures' },
        ],
        selected: [],
      },
    })

    const chips = wrapper.findAll('button.imp-chip')
    // Healthy chip: no degraded class, no warning marker.
    expect(chips[0]!.classes()).not.toContain('imp-chip--degraded')
    expect(chips[0]!.find('.imp-chip__warn').exists()).toBe(false)
    // Degraded chip: dimmed class + ⚠ marker + reason on the title.
    expect(chips[1]!.classes()).toContain('imp-chip--degraded')
    expect(chips[1]!.find('.imp-chip__warn').exists()).toBe(true)
    expect(chips[1]!.attributes('title')).toBe('Temporarily unavailable — 4 consecutive failures')
  })

  it('still emits the toggle when a degraded chip is clicked (a hint, not a hard block)', async () => {
    const wrapper = mount(SourceFilterChips, {
      props: { sources: [{ id: 'b', name: 'Asura Scans', degraded: true }], selected: [] },
    })

    await wrapper.find('button.imp-chip').trigger('click')

    expect(wrapper.emitted('update:selected')).toEqual([[['b']]])
  })
})
