/**
 * SourcesPanel — the multi-select consolidation affordance (QCAT-295 Part B).
 * With ≥2 sources each row shows a checkbox; ticking one reveals the "Merge
 * into…" bar, which emits `startConsolidate` with the selected SeriesProvider ids.
 * The selection is pruned when the provider list changes (a completed merge drops
 * the folded-away rows).
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import SourcesPanel from './SourcesPanel.vue'
import { richSeries, singleProviderSeries } from '../../fixtures/seriesDetail'

function mountPanel(props: Record<string, unknown> = {}) {
  return mount(SourcesPanel, { props: { providers: richSeries.providers, ...props } })
}

describe('SourcesPanel — multi-select consolidation', () => {
  it('renders a select checkbox per row when there are ≥2 sources', () => {
    const wrapper = mountPanel()
    expect(wrapper.findAll('input[type="checkbox"]')).toHaveLength(richSeries.providers.length)
  })

  it('does NOT offer checkboxes for a single-source series (nothing to consolidate)', () => {
    const wrapper = mountPanel({ providers: singleProviderSeries.providers })
    expect(wrapper.findAll('input[type="checkbox"]')).toHaveLength(0)
  })

  it('reveals the merge bar once ≥1 source is ticked and emits startConsolidate with the ids', async () => {
    const wrapper = mountPanel()
    const first = richSeries.providers[0]!
    expect(wrapper.text()).not.toContain('selected')

    await wrapper.find('input[type="checkbox"]').setValue(true)
    expect(wrapper.text()).toContain('1 source selected')

    await wrapper.findAll('button').find(b => b.text().includes('Merge into'))!.trigger('click')
    expect(wrapper.emitted('startConsolidate')).toEqual([[[first.id]]])
  })

  it('prunes a selected id that disappears from the provider list (post-merge refetch)', async () => {
    const wrapper = mountPanel()
    await wrapper.find('input[type="checkbox"]').setValue(true)
    expect(wrapper.text()).toContain('1 source selected')

    // The folded-away provider drops out of the list — the merge bar clears.
    await wrapper.setProps({ providers: richSeries.providers.slice(1) })
    expect(wrapper.text()).not.toContain('selected')
  })
})
