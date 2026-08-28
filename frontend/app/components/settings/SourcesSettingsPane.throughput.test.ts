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
    const wrapper = mount(SourcesSettingsPane, { props: { ...baseProps, throughputError: 'Policy service unavailable' } })
    expect(wrapper.findComponent(SourceThroughputControl).exists()).toBe(false)
    await wrapper.get('[data-testid="retry-throughput"]').trigger('click')
    expect(wrapper.emitted('reloadThroughput')).toBeTruthy()
  })

  it('renders a successful authoritative policy set', () => {
    const wrapper = mount(SourcesSettingsPane, { props: baseProps })
    expect(wrapper.findComponent(SourceThroughputControl).exists()).toBe(true)
  })
})
