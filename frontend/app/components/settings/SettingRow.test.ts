import { h } from 'vue'
import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import SettingRow from './SettingRow.vue'

describe('SettingRow', () => {
  it('offers its setting name to the trailing control as accessible-label context', () => {
    const wrapper = mount(SettingRow, {
      props: { name: 'Refresh interval' },
      slots: {
        default: ({ accessibleLabel }: { accessibleLabel?: string }) => (
          h('input', { 'aria-label': accessibleLabel })
        ),
      },
    })

    expect(wrapper.get('input').attributes('aria-label')).toBe('Refresh interval')
  })
})
