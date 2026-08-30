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
  it('never renders the retired all-source throughput form, even with the old dataset supplied', () => {
    const wrapper = mount(SourcesSettingsPane, { props: baseProps })
    expect(wrapper.findComponent(SourceThroughputControl).exists()).toBe(false)
    expect(wrapper.text()).not.toContain('Per-source download pace')
  })
})
