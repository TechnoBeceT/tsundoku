import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import SelectField from './SelectField.vue'

describe('SelectField', () => {
  it('renders disabled options while preserving the selected value and other available choices', async () => {
    const wrapper = mount(SelectField, {
      props: {
        modelValue: 'required',
        options: [
          { value: 'auto', label: 'Auto' },
          { value: 'required', label: 'Required', disabled: true },
          { value: 'disabled', label: 'Disabled' },
        ],
      },
    })

    expect(wrapper.get('option[value="required"]').attributes('disabled')).toBeDefined()
    expect((wrapper.get('select').element as HTMLSelectElement).value).toBe('required')
    expect(wrapper.get('select').attributes('disabled')).toBeUndefined()
    expect(wrapper.get('option[value="auto"]').attributes('disabled')).toBeUndefined()
    await wrapper.get('select').setValue('auto')
    expect(wrapper.emitted('update:modelValue')).toEqual([['auto']])
  })
})
