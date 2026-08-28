import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import SourceThroughputControl from './SourceThroughputControl.vue'

vi.mock('~/utils/api/client', () => ({
  apiClient: { GET: vi.fn().mockResolvedValue({ data: { authenticated: true, ownerId: 'owner' } }) },
  setUnauthorizedHandler: vi.fn(),
}))

const inherited = {
  sourceId: '101',
  downloadConcurrency: { override: null, effective: 5 },
  imageRequestDelay: { override: null, effective: '500ms' },
}

describe('SourceThroughputControl', () => {
  it('shows inherited and effective values', () => {
    const wrapper = mount(SourceThroughputControl, { props: { policy: inherited, sourceName: 'ComicK Fanmade' } })
    expect(wrapper.text()).toContain('Uses global')
    expect(wrapper.text()).toContain('Global: 5')
    expect(wrapper.text()).toContain('Global: 500ms')
    expect(wrapper.text()).toContain('Effective: 5')
    expect(wrapper.text()).toContain('Effective: 500ms')
    expect(wrapper.get('input[type="number"]').element.closest('label')?.textContent).toContain('ComicK Fanmade chapter concurrency override')
    expect(wrapper.get('[data-testid="image-delay"]').element.closest('label')?.textContent).toContain('ComicK Fanmade image request delay override')
  })

  it('saves an explicit zero delay instead of clearing to inherit', async () => {
    const wrapper = mount(SourceThroughputControl, { props: { policy: inherited } })
    const delay = wrapper.get('[data-testid="image-delay"]')
    await delay.setValue('0s')
    await delay.trigger('keydown.enter')
    expect(wrapper.emitted('save-image-delay')?.[0]).toEqual(['101', '0s'])
  })

  it('emits separate inherit actions without touching the other field', async () => {
    const policy = {
      ...inherited,
      downloadConcurrency: { override: 2, effective: 2 },
      imageRequestDelay: { override: '750ms', effective: '750ms' },
    }
    const wrapper = mount(SourceThroughputControl, { props: { policy } })
    await wrapper.get('[data-testid="inherit-concurrency"]').trigger('click')
    await wrapper.get('[data-testid="inherit-delay"]').trigger('click')
    expect(wrapper.emitted('inherit-concurrency')?.[0]).toEqual(['101'])
    expect(wrapper.emitted('inherit-image-delay')?.[0]).toEqual(['101'])
  })

  it('disables controls while saving', () => {
    const wrapper = mount(SourceThroughputControl, { props: { policy: inherited, saving: true } })
    expect(wrapper.findAll('button').every(button => button.attributes('disabled') !== undefined)).toBe(true)
    expect(wrapper.findAll('input').every(input => input.attributes('disabled') !== undefined)).toBe(true)
  })

  it('keeps a save error visible for correction', () => {
    const wrapper = mount(SourceThroughputControl, { props: { policy: inherited, error: 'Image delay must use whole milliseconds.' } })
    expect(wrapper.get('[role="alert"]').text()).toContain('whole milliseconds')
  })
})
