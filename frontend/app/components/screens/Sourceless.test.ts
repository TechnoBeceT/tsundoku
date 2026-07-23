/**
 * Sourceless screen — the presentational states + per-card action wiring.
 *
 * Pins:
 *   1. an empty list renders the all-clear EmptyState (never a bare list);
 *   2. a non-empty list renders one SourcelessSeriesCard per series;
 *   3. a card's "Review" bubbles up as `review` with the series id;
 *   4. `loading` renders skeleton cards instead of the list.
 *
 * Non-vacuous: drop the empty-state branch and test 1 fails; forget to forward
 * a card emit and test 3 fails. Mounts the REAL components (mirrors
 * Fractionals.test.ts).
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import Sourceless from './Sourceless.vue'
import SourcelessSeriesCard from '../sourceless/SourcelessSeriesCard.vue'
import Skeleton from '../ui/Skeleton.vue'
import { sampleSourcelessSeries } from '../../fixtures/sourceless'

function mountScreen(props: Partial<InstanceType<typeof Sourceless>['$props']> = {}) {
  return mount(Sourceless, {
    props: { series: sampleSourcelessSeries, ...props },
  })
}

describe('Sourceless screen', () => {
  it('shows the all-clear empty state when there are no series', () => {
    const wrapper = mount(Sourceless, { props: { series: [] } })
    expect(wrapper.text()).toContain('Nothing sourceless')
    expect(wrapper.findAllComponents(SourcelessSeriesCard)).toHaveLength(0)
  })

  it('renders one card per series', () => {
    const wrapper = mountScreen()
    expect(wrapper.findAllComponents(SourcelessSeriesCard)).toHaveLength(sampleSourcelessSeries.length)
  })

  it('bubbles a card "Review" up as review with the series id', async () => {
    const wrapper = mountScreen()
    const firstCard = wrapper.findComponent(SourcelessSeriesCard)
    await firstCard.find('button.btn').trigger('click')
    const emitted = wrapper.emitted('review')
    expect(emitted).toBeTruthy()
    expect(emitted![0]).toEqual([sampleSourcelessSeries[0]!.seriesId])
  })

  it('renders skeleton cards while loading, not the series list', () => {
    const wrapper = mount(Sourceless, { props: { series: [], loading: true } })
    expect(wrapper.findAllComponents(Skeleton).length).toBeGreaterThan(0)
    expect(wrapper.findAllComponents(SourcelessSeriesCard)).toHaveLength(0)
  })
})
