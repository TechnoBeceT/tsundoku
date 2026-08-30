import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import SurfaceCard from './SurfaceCard.vue'

describe('SurfaceCard heading semantics', () => {
  it('keeps h2 as the backward-compatible default', () => {
    const wrapper = mount(SurfaceCard, { props: { title: 'Default card' } })

    expect(wrapper.get('h2').text()).toBe('Default card')
  })

  it('uses the configured heading level when nested below a section', () => {
    const wrapper = mount(SurfaceCard, { props: { title: 'Nested card', headingLevel: 3 } })

    expect(wrapper.find('h2').exists()).toBe(false)
    expect(wrapper.get('h3').text()).toBe('Nested card')
  })
})
