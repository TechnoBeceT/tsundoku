/**
 * Fractionals screen — the presentational states + per-card action wiring.
 *
 * Pins:
 *   1. an empty list renders the all-clear EmptyState (never a bare list);
 *   2. a non-empty list renders one FractionalSeriesCard per series;
 *   3. a card's ignore toggle bubbles up as `toggle-ignore` with the series id;
 *   4. "Clean files" bubbles up as `clean-files` and is DISABLED when nothing is
 *      removable yet (no dead control);
 *   5. `busyIds` dims that one card's toggle.
 *
 * Non-vacuous: drop the empty-state branch and test 1 fails; forget to forward a
 * card emit and tests 3/4 fail. Mounts the REAL components (mirrors
 * LibraryList.test.ts).
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import Fractionals from './Fractionals.vue'
import FractionalSeriesCard from '../fractionals/FractionalSeriesCard.vue'
import Toggle from '../ui/Toggle.vue'
import { fractionalSeries, partlyRemovable, policyNotSet } from '../../fixtures/fractionals'

function mountScreen(props: Partial<InstanceType<typeof Fractionals>['$props']> = {}) {
  return mount(Fractionals, {
    props: { series: fractionalSeries, ...props },
  })
}

describe('Fractionals screen', () => {
  it('shows the all-clear empty state when there are no series', () => {
    const wrapper = mount(Fractionals, { props: { series: [] } })
    expect(wrapper.text()).toContain('No fractionals to manage')
    expect(wrapper.findAllComponents(FractionalSeriesCard)).toHaveLength(0)
  })

  it('renders one card per series', () => {
    const wrapper = mountScreen()
    expect(wrapper.findAllComponents(FractionalSeriesCard)).toHaveLength(fractionalSeries.length)
  })

  it('bubbles a card ignore toggle up as toggle-ignore with the series id', async () => {
    const wrapper = mountScreen({ series: [partlyRemovable] })
    wrapper.findComponent(Toggle).vm.$emit('update:modelValue', true)
    await wrapper.vm.$nextTick()
    const emitted = wrapper.emitted('toggle-ignore')
    expect(emitted).toBeTruthy()
    expect(emitted![0]).toEqual([{ seriesId: partlyRemovable.seriesId, ignore: true }])
  })

  it('disables "Clean files" when nothing is removable yet', () => {
    const wrapper = mountScreen({ series: [policyNotSet] })
    // The AppButton renders button.btn; with removableCount 0 it is disabled.
    const cleanBtn = wrapper.findComponent(FractionalSeriesCard).find('button.btn')
    expect(cleanBtn.exists()).toBe(true)
    expect(cleanBtn.attributes('disabled')).not.toBeUndefined()
  })

  it('bubbles "Clean files" up as clean-files with the series id', async () => {
    const wrapper = mountScreen({ series: [partlyRemovable] })
    // partlyRemovable has removableCount 5 → the button is enabled.
    const cleanBtn = wrapper.findComponent(FractionalSeriesCard).find('button.btn')
    await cleanBtn.trigger('click')
    const emitted = wrapper.emitted('clean-files')
    expect(emitted).toBeTruthy()
    expect(emitted![0]).toEqual([partlyRemovable.seriesId])
  })

  it('dims a card whose toggle is mid-write via busyIds', () => {
    const wrapper = mountScreen({ series: [partlyRemovable], busyIds: [partlyRemovable.seriesId] })
    expect(wrapper.findComponent(Toggle).props('disabled')).toBe(true)
  })
})
