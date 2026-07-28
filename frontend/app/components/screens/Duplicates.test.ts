/**
 * Duplicates screen — the presentational states + per-card action wiring.
 *
 * Pins:
 *   1. an empty list renders the all-clear EmptyState (never a bare list);
 *   2. a non-empty list renders one DuplicateSeriesCard per series;
 *   3. a card's "Open series" bubbles up as `open-series` with the series id —
 *      the tab is DISCOVERY ONLY, so navigating is the only action a row has;
 *   4. `loading` renders skeleton cards instead of the list;
 *   5. the header shows the server totals, formatted.
 *
 * Non-vacuous: drop the empty-state branch and test 1 fails; forget to forward a
 * card emit and test 3 fails; render the raw byte count and test 5 fails. Mounts
 * the REAL components (mirrors Sourceless.test.ts).
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import Duplicates from './Duplicates.vue'
import DuplicateSeriesCard from '../duplicates/DuplicateSeriesCard.vue'
import Skeleton from '../ui/Skeleton.vue'
import { sampleDuplicateSeries } from '../../fixtures/duplicates'

/** The server totals the fixture list adds up to — the screen renders them verbatim. */
const TOTALS = { totalFiles: 258, totalBytes: 1_311_810_518 }

function mountScreen(props: Partial<InstanceType<typeof Duplicates>['$props']> = {}) {
  return mount(Duplicates, {
    props: { series: sampleDuplicateSeries, ...TOTALS, ...props },
  })
}

describe('Duplicates screen', () => {
  it('shows the all-clear empty state when there are no series', () => {
    const wrapper = mount(Duplicates, { props: { series: [], totalFiles: 0, totalBytes: 0 } })
    expect(wrapper.text()).toContain('No duplicate files')
    expect(wrapper.findAllComponents(DuplicateSeriesCard)).toHaveLength(0)
  })

  it('renders one card per series', () => {
    const wrapper = mountScreen()
    expect(wrapper.findAllComponents(DuplicateSeriesCard)).toHaveLength(sampleDuplicateSeries.length)
  })

  it('bubbles a card "Open series" up as open-series with the series id', async () => {
    const wrapper = mountScreen()
    const firstCard = wrapper.findComponent(DuplicateSeriesCard)
    await firstCard.find('button.btn').trigger('click')
    const emitted = wrapper.emitted('open-series')
    expect(emitted).toBeTruthy()
    expect(emitted![0]).toEqual([sampleDuplicateSeries[0]!.seriesId])
  })

  it('renders skeleton cards while loading, not the series list', () => {
    const wrapper = mount(Duplicates, { props: { series: [], totalFiles: 0, totalBytes: 0, loading: true } })
    expect(wrapper.findAllComponents(Skeleton).length).toBeGreaterThan(0)
    expect(wrapper.findAllComponents(DuplicateSeriesCard)).toHaveLength(0)
  })

  it('shows the server totals in the header, formatted as a size', () => {
    const wrapper = mountScreen({ totalFiles: 258, totalBytes: 1_073_741_824 })
    expect(wrapper.text()).toContain('3 series · 258 files · 1.0 GB')
  })
})
