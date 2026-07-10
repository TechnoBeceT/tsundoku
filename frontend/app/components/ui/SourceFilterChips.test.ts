/**
 * SourceFilterChips — the shared source-filter chip row extracted from the
 * Adopt wizard (`screens/Import.vue`) and reused by the Series-Detail
 * "Add a source" dialog. Pins the toggle contract: clicking a chip emits the
 * NEW `selected` array (the component holds no state of its own).
 *
 * Non-vacuous: the toggle must ADD an unselected id and REMOVE an already-
 * selected one — both directions asserted.
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
})
