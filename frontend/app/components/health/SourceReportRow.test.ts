/**
 * SourceReportRow — accordion toggle, the preserved Tsundoku superset, and the
 * expanded report sections.
 *
 * Pins that the chevron emits `toggle` with the source key; that a matched metric
 * with a tripped breaker renders the reused SourceMetricRow's cooling-down banner
 * + Reset (the superset MUST NOT regress) and forwards its `reset`; that the body
 * is hidden when collapsed and reveals the timeline + breakdown + recent events
 * when expanded; and that a null metric falls back to the minimal header.
 *
 * Non-vacuous: drop the toggle `@click` and the emit fails; drop the metric reuse
 * and the cooling-banner assertion fails.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import SourceReportRow from './SourceReportRow.vue'
import { sourceReports, timelineBuckets, sourceEvents } from '../../fixtures/sourceReport'
import { sourceMetrics } from '../../fixtures/settings'

const report = sourceReports[0]! // ComicK (failing)
const coolingMetric = sourceMetrics[1]! // ComicK metric with a tripped breaker

describe('SourceReportRow', () => {
  it('emits toggle with the source key when the chevron is clicked', async () => {
    const wrapper = mount(SourceReportRow, { props: { report, metric: coolingMetric } })
    await wrapper.find('.rr__toggle').trigger('click')
    expect(wrapper.emitted('toggle')?.[0]).toEqual([report.sourceKey])
    wrapper.unmount()
  })

  it('preserves the Tsundoku superset — reuses SourceMetricRow (cooling banner + Reset)', async () => {
    const wrapper = mount(SourceReportRow, { props: { report, metric: coolingMetric } })
    // The reused metric row is present with its cooling-down breaker banner.
    expect(wrapper.find('.metric').exists()).toBe(true)
    expect(wrapper.find('.metric__cooldown').exists()).toBe(true)
    expect(wrapper.text()).toContain('cooling down')

    // Its Reset button forwards through as `reset` carrying the metric id.
    const reset = wrapper.findAll('button').find(b => b.text().includes('Reset'))!
    await reset.trigger('click')
    expect(wrapper.emitted('reset')?.[0]).toEqual([coolingMetric.id])
    wrapper.unmount()
  })

  it('hides the body when collapsed and reveals the report sections when expanded', () => {
    const collapsed = mount(SourceReportRow, { props: { report, metric: coolingMetric, expanded: false } })
    // v-show keeps the body mounted but display:none when collapsed.
    expect(collapsed.find('.rr__body').attributes('style') ?? '').toContain('display: none')
    collapsed.unmount()

    const expanded = mount(SourceReportRow, {
      props: {
        report,
        metric: coolingMetric,
        expanded: true,
        timeline: timelineBuckets,
        recentEvents: sourceEvents.slice(0, 4),
      },
    })
    expect(expanded.find('.rr__body').attributes('style') ?? '').not.toContain('display: none')
    expect(expanded.find('.tl').exists()).toBe(true) // TimelineHistogram
    expect(expanded.find('.etb').exists()).toBe(true) // EventTypeBreakdown
    expect(expanded.find('.evt').exists()).toBe(true) // EventTable (recent events)
    expanded.unmount()
  })

  it('forwards a recent-event click as select-event', async () => {
    const wrapper = mount(SourceReportRow, {
      props: { report, metric: coolingMetric, expanded: true, timeline: timelineBuckets, recentEvents: sourceEvents.slice(0, 4) },
    })
    await wrapper.find('.evt__row').trigger('click')
    expect(wrapper.emitted('select-event')).toBeTruthy()
    wrapper.unmount()
  })

  it('falls back to a minimal header when no metric is matched', () => {
    const wrapper = mount(SourceReportRow, { props: { report, metric: null } })
    expect(wrapper.find('.metric').exists()).toBe(false)
    expect(wrapper.find('.rr__fallback').exists()).toBe(true)
    expect(wrapper.text()).toContain(report.sourceName)
    wrapper.unmount()
  })
})
