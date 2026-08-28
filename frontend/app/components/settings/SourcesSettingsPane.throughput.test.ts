import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import SourcesSettingsPane from './SourcesSettingsPane.vue'
import SourceThroughputControl from './SourceThroughputControl.vue'
import { inheritedThroughputPolicy, sourcesSettings } from '../../fixtures/settings'

vi.mock('~/utils/api/client', () => ({
  apiClient: { GET: vi.fn().mockResolvedValue({ data: { authenticated: true, ownerId: 'owner' } }) },
  setUnauthorizedHandler: vi.fn(),
}))

const baseProps = { sources: sourcesSettings, throughputPolicies: [inheritedThroughputPolicy] }

describe('SourcesSettingsPane throughput authority', () => {
  it('does not render controls before the first authoritative load starts', () => {
    const wrapper = mount(SourcesSettingsPane, { props: { ...baseProps, throughputReady: false } })
    expect(wrapper.findComponent(SourceThroughputControl).exists()).toBe(false)
  })

  it('does not render editable policies while the authoritative load is pending', () => {
    const wrapper = mount(SourcesSettingsPane, { props: { ...baseProps, throughputLoading: true } })
    expect(wrapper.findComponent(SourceThroughputControl).exists()).toBe(false)
    expect(wrapper.text()).toContain('Loading source policies')
  })

  it('does not render false inherited controls after load failure and offers retry', async () => {
    const wrapper = mount(SourcesSettingsPane, { props: { ...baseProps, throughputLoadError: 'Policy service unavailable' } })
    expect(wrapper.findComponent(SourceThroughputControl).exists()).toBe(false)
    await wrapper.get('[data-testid="retry-throughput"]').trigger('click')
    expect(wrapper.emitted('reloadThroughput')).toBeTruthy()
  })

  it.each([
    'Download concurrency must be between 1 and 32.',
    'Source policy could not be saved',
  ])('keeps authoritative controls editable for mutation failure: %s', (message) => {
    const wrapper = mount(SourcesSettingsPane, { props: { ...baseProps, throughputError: message } })
    expect(wrapper.findComponent(SourceThroughputControl).exists()).toBe(true)
    expect(wrapper.get('[role="alert"]').text()).toContain(message)
    expect(wrapper.find('[data-testid="retry-throughput"]').exists()).toBe(false)
    expect(wrapper.get('input[type="number"]').attributes('disabled')).toBeUndefined()
  })

  it('renders a successful authoritative policy set', () => {
    const wrapper = mount(SourcesSettingsPane, { props: baseProps })
    expect(wrapper.findComponent(SourceThroughputControl).exists()).toBe(true)
  })
})
