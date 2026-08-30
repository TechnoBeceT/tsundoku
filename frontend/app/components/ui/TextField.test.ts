import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import TextField from './TextField.vue'

describe('TextField', () => {
  it('puts a non-visual accessible label on the native input', () => {
    const wrapper = mount(TextField, {
      props: {
        modelValue: '5',
        type: 'number',
        compact: true,
        accessibleLabel: 'Chapter max retries',
      },
    })

    expect(wrapper.get('input').attributes('aria-label')).toBe('Chapter max retries')
  })
})
