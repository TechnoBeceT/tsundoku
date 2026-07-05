/**
 * SearchGroupCard — pins the `trayEnabled` gate on the cross-search adopt-tray
 * Add/Added toggle. The card is reused by two SINGLE-SELECT match surfaces
 * (`scanLibrary/MatchPanel`, `seriesDetail/MatchSourceDialog`) that have no
 * tray and must NOT show the toggle — they leave `trayEnabled` at its default
 * `false`. The Adopt wizard (`screens/Import.vue`) passes `trayEnabled` true.
 *
 * Non-vacuous: dropping the `v-if="trayEnabled"` guard makes the "absent by
 * default" assertion fail (the toggle would leak into the match surfaces —
 * the exact regression this test guards).
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import SearchGroupCard from './SearchGroupCard.vue'
import { searchResults } from '../../fixtures/import'

const group = searchResults[0]!

describe('SearchGroupCard — trayEnabled toggle gate', () => {
  it('omits the Add/Added toggle by default (single-select match surfaces)', () => {
    const wrapper = mount(SearchGroupCard, { props: { group } })

    expect(wrapper.find('.group__toggle').exists()).toBe(false)
    // The classic pick still works — the whole body emits `pick`.
    expect(wrapper.emitted('add')).toBeUndefined()
  })

  it('renders the "+ Add" toggle when trayEnabled, emitting `add` on click', async () => {
    const wrapper = mount(SearchGroupCard, { props: { group, trayEnabled: true } })

    const toggle = wrapper.find('.group__toggle')
    expect(toggle.exists()).toBe(true)
    expect(toggle.text()).toContain('+ Add')

    await toggle.trigger('click')
    expect(wrapper.emitted('add')).toEqual([[group]])
    // The toggle's @click.stop keeps the card-body pick from also firing.
    expect(wrapper.emitted('pick')).toBeUndefined()
  })

  it('shows "✓ Added" and emits `remove` when the group is already in the tray', async () => {
    const wrapper = mount(SearchGroupCard, { props: { group, trayEnabled: true, added: true } })

    const toggle = wrapper.find('.group__toggle')
    expect(toggle.text()).toContain('✓ Added')

    await toggle.trigger('click')
    expect(wrapper.emitted('remove')).toEqual([[group]])
  })

  it('keeps the card body keyboard-operable (role/tabindex + Enter picks) while the tray is empty', async () => {
    const wrapper = mount(SearchGroupCard, { props: { group, trayEnabled: true } })
    const body = wrapper.find('.group')

    expect(body.attributes('role')).toBe('button')
    expect(body.attributes('tabindex')).toBe('0')

    await body.trigger('keydown.enter')
    expect(wrapper.emitted('pick')).toEqual([[group]])
  })

  it('drops the body role/tabindex + stops picking once the tray is active', async () => {
    const wrapper = mount(SearchGroupCard, { props: { group, trayEnabled: true, trayActive: true } })
    const body = wrapper.find('.group')

    expect(body.attributes('role')).toBeUndefined()
    expect(body.attributes('tabindex')).toBeUndefined()

    await body.trigger('click')
    expect(wrapper.emitted('pick')).toBeUndefined()
  })
})
