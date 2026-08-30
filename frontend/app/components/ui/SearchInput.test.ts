/** SearchInput accessible-name contract. */
import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import SearchInput from './SearchInput.vue'

describe('SearchInput', () => {
  it('puts the supplied accessible name on the native search field', () => {
    const wrapper = mount(SearchInput, {
      props: { modelValue: '', label: 'Search installed sources' },
    })

    expect(wrapper.get('input[type="search"]').attributes('aria-label')).toBe('Search installed sources')
  })
})
