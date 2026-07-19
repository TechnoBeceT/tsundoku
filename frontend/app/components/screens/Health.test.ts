/**
 * Health console shell — the 2-tab screen (Library + Sources).
 *
 * Pins the tab shell wiring the composition root (pages/health.vue) drives:
 *   1. it defaults to the Library tab (LibraryHealth shown, SourceHealth not);
 *   2. a controlled activeTab='sources' shows SourceHealth (this is what the
 *      `?tab=sources`/`?tab=metrics` deep-link resolves to — the page passes the
 *      resolved tab in); the Library tab is then hidden;
 *   3. clicking the Sources tab button emits `set-tab` with 'sources';
 *   4. child actions forward up (open-series from Library, warm-now/reset-breaker
 *      from Sources).
 *
 * Non-vacuous: swap the v-if branches and tests 1/2 fail; drop a forwarded emit
 * and tests 3/4 fail. Mounts the REAL child screens (mirrors Fractionals.test.ts).
 *
 * The deep-link/sessionStorage RESOLUTION precedence itself is unit-tested on the
 * pure resolver in utils/healthTabs.test.ts (default · stored · query · alias).
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import Health from './Health.vue'
import LibraryHealth from './LibraryHealth.vue'
import SourceHealth from './SourceHealth.vue'
import SourceMetricRow from '../health/SourceMetricRow.vue'
import { sickSeries } from '../../fixtures/libraryHealth'
import { sourceMetrics } from '../../fixtures/settings'

function mountShell(props: Partial<InstanceType<typeof Health>['$props']> = {}) {
  return mount(Health, {
    props: { series: sickSeries, metrics: sourceMetrics, ...props },
  })
}

/** The SegmentedTabs button whose visible label matches `label`. */
function tabButton(wrapper: ReturnType<typeof mountShell>, label: string) {
  return wrapper.findAll('button[role="tab"]').find(b => b.text().includes(label))
}

describe('Health console shell', () => {
  it('defaults to the Library tab', () => {
    const wrapper = mountShell()
    expect(wrapper.findComponent(LibraryHealth).exists()).toBe(true)
    expect(wrapper.findComponent(SourceHealth).exists()).toBe(false)
  })

  it('shows the Sources tab when activeTab=sources (the deep-link target)', () => {
    const wrapper = mountShell({ activeTab: 'sources' })
    expect(wrapper.findComponent(SourceHealth).exists()).toBe(true)
    expect(wrapper.findComponent(LibraryHealth).exists()).toBe(false)
  })

  it('emits set-tab with the picked key when a tab is clicked', async () => {
    const wrapper = mountShell()
    await tabButton(wrapper, 'Sources')!.trigger('click')
    expect(wrapper.emitted('set-tab')).toEqual([['sources']])
  })

  it('forwards a Library-tab open-series up', () => {
    const wrapper = mountShell()
    wrapper.findComponent(LibraryHealth).vm.$emit('open-series', 'series-42')
    expect(wrapper.emitted('open-series')).toEqual([['series-42']])
  })

  it('forwards Sources-tab warm-now + reset-breaker up', () => {
    const wrapper = mountShell({ activeTab: 'sources' })
    const source = wrapper.findComponent(SourceHealth)
    source.vm.$emit('warm-now')
    source.vm.$emit('reset-breaker', 'src-2')
    expect(wrapper.emitted('warm-now')).toBeTruthy()
    expect(wrapper.emitted('reset-breaker')).toEqual([['src-2']])
    // And the relocated metric rows actually render under the Sources tab.
    expect(wrapper.findAllComponents(SourceMetricRow).length).toBe(sourceMetrics.length)
  })
})
