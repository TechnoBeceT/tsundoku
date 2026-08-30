import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import DurationInput from './DurationInput.vue'

describe('DurationInput', () => {
  it('names its value and unit controls from the supplied setting context', () => {
    const wrapper = mount(DurationInput, {
      props: {
        modelValue: { value: 15, unit: 'm' },
        accessibleLabel: 'Refresh interval',
      },
    })

    expect(wrapper.get('input[type="number"]').attributes('aria-label')).toBe('Refresh interval value')
    expect(wrapper.get('select').attributes('aria-label')).toBe('Refresh interval unit')
  })
})
