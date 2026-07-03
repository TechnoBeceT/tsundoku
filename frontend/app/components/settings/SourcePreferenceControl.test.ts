/**
 * SourcePreferenceControl — each variant commits the right typed value.
 *
 * Pins the change payload per control (the value the parent PATCHes):
 *   - Switch  → flipping emits the negated boolean.
 *   - List    → selecting emits the chosen entryValue (a string).
 *   - Multi   → checking an unselected option emits the extended array.
 *   - EditText→ typing + Enter emits the buffered string (not per keystroke).
 *
 * Non-vacuous: if a control stopped forwarding its edit, or emitted the wrong
 * value/position, the matching assertion fails.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import SourcePreferenceControl from './SourcePreferenceControl.vue'
import { editPref, listPref, multiPref, switchPref } from '../../fixtures/preferences'

interface Payload { sourceId: string, position: number, value: unknown }

function lastChange(wrapper: ReturnType<typeof mount>): Payload {
  const emitted = wrapper.emitted('change')
  expect(emitted).toBeTruthy()
  return emitted![emitted!.length - 1]![0] as Payload
}

describe('SourcePreferenceControl', () => {
  it('Switch: flipping commits the negated boolean at its position', async () => {
    const wrapper = mount(SourcePreferenceControl, { props: { preference: switchPref, sourceId: 'src-en' } })
    await wrapper.find('[role="switch"]').trigger('click')
    expect(lastChange(wrapper)).toEqual({ sourceId: 'src-en', position: 0, value: false })
  })

  it('List: selecting commits the chosen entryValue', async () => {
    const wrapper = mount(SourcePreferenceControl, { props: { preference: listPref, sourceId: 'src-en' } })
    await wrapper.find('select').setValue('.256.jpg')
    expect(lastChange(wrapper)).toEqual({ sourceId: 'src-en', position: 1, value: '.256.jpg' })
  })

  it('MultiSelect: checking an option commits the extended array', async () => {
    const wrapper = mount(SourcePreferenceControl, { props: { preference: multiPref, sourceId: 'src-en' } })
    // multiPref starts with ['safe','suggestive']; check 'erotica' (the 3rd box).
    const boxes = wrapper.findAll('input[type="checkbox"]')
    await boxes[2]!.setValue(true)
    expect(lastChange(wrapper)).toEqual({ sourceId: 'src-en', position: 2, value: ['safe', 'suggestive', 'erotica'] })
  })

  it('EditText: typing then Enter commits the buffered string once', async () => {
    const wrapper = mount(SourcePreferenceControl, { props: { preference: editPref, sourceId: 'src-en' } })
    const input = wrapper.find('input[type="text"]')
    await input.setValue('group-a, group-b')
    // No commit yet — the field buffers until Enter/blur.
    expect(wrapper.emitted('change')).toBeFalsy()
    await input.trigger('keydown.enter')
    expect(lastChange(wrapper)).toEqual({ sourceId: 'src-en', position: 3, value: 'group-a, group-b' })
  })
})
