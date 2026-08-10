/**
 * SourceSeriesPanel — the per-source impact list's presentational states.
 *
 * Pins:
 *   1. a series with no alternative renders a "Goes dark" badge (Tag) and NOT a
 *      take-over line — the load-bearing distinction the owner acts on;
 *   2. a series that keeps a provider renders the take-over provider and NO
 *      "Goes dark" badge;
 *   3. the summary line counts total series and how many go dark;
 *   4. the four §16 states are distinct: loading / error / empty / list.
 *
 * Non-vacuous: swap the goesDark branch and test 1 fails; drop the summary and
 * test 3 fails. Mounts the REAL component + its Tag atom.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import SourceSeriesPanel from './SourceSeriesPanel.vue'
import Tag from '../ui/Tag.vue'
import type { SourceSeriesRow } from './sourceSeries.types'

const darkRow: SourceSeriesRow = {
  seriesId: 's-dark', title: 'Only Here', alternativeCount: 0, goesDark: true, topAlternative: '',
}
const keptRow: SourceSeriesRow = {
  seriesId: 's-kept', title: 'Solo Leveling', alternativeCount: 2, goesDark: false, topAlternative: 'Flame Comics',
}

describe('SourceSeriesPanel', () => {
  it('renders a "Goes dark" badge for a series with no alternative, and no take-over line', () => {
    const wrapper = mount(SourceSeriesPanel, { props: { rows: [darkRow] } })
    const tags = wrapper.findAllComponents(Tag)
    expect(tags).toHaveLength(1)
    expect(tags[0]!.text()).toContain('Goes dark')
    expect(wrapper.text()).not.toContain('Taken over by')
    wrapper.unmount()
  })

  it('renders the take-over provider for a series that keeps one, and no "Goes dark" badge', () => {
    const wrapper = mount(SourceSeriesPanel, { props: { rows: [keptRow] } })
    expect(wrapper.findAllComponents(Tag)).toHaveLength(0)
    expect(wrapper.text()).toContain('Taken over by')
    expect(wrapper.text()).toContain('Flame Comics')
    wrapper.unmount()
  })

  it('summarises total series and the go-dark count', () => {
    const wrapper = mount(SourceSeriesPanel, { props: { rows: [darkRow, keptRow] } })
    expect(wrapper.text()).toContain('2 series · 1 go dark')
    wrapper.unmount()
  })

  it('reads "none go dark" when every series keeps a provider', () => {
    const wrapper = mount(SourceSeriesPanel, { props: { rows: [keptRow] } })
    expect(wrapper.text()).toContain('1 series · none go dark')
    wrapper.unmount()
  })

  it('shows the loading state and no rows while pending (§16)', () => {
    const wrapper = mount(SourceSeriesPanel, { props: { rows: [], pending: true } })
    expect(wrapper.text()).toContain('Checking affected series')
    expect(wrapper.findAllComponents(Tag)).toHaveLength(0)
    wrapper.unmount()
  })

  it('surfaces a load error (§16, never swallowed)', () => {
    const wrapper = mount(SourceSeriesPanel, { props: { rows: [], error: 'boom' } })
    expect(wrapper.text()).toContain('boom')
    wrapper.unmount()
  })

  it('shows the empty state when nothing carries the source', () => {
    const wrapper = mount(SourceSeriesPanel, { props: { rows: [] } })
    expect(wrapper.text()).toContain('Nothing depends on this source')
    wrapper.unmount()
  })
})
