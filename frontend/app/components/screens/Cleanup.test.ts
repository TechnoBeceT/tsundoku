/**
 * Cleanup console shell — the 3-tab screen (Fractionals + Sourceless + Duplicates).
 *
 * Pins the tab shell wiring the composition root (pages/cleanup.vue) drives:
 *   1. it defaults to the Fractionals tab (only that body is mounted);
 *   2. a controlled activeTab shows that tab's body and ONLY that one — this is
 *      what the `?tab=` deep-link (and the two redirected legacy routes) resolve
 *      to, and mounting one body at a time is what keeps the other tabs' scans
 *      from running;
 *   3. clicking a tab button emits `set-tab` with its key;
 *   4. child actions forward up (open-series, refresh, toggle-ignore, clean-files,
 *      review);
 *   5. a tab's load error renders as a banner above its body (§16).
 *
 * Non-vacuous: swap the v-if branches and tests 1/2 fail; drop a forwarded emit
 * and test 4 fails; drop the ErrorBanner and test 5 fails. Mounts the REAL child
 * screens (mirrors Health.test.ts).
 *
 * The deep-link/sessionStorage RESOLUTION precedence itself is unit-tested on the
 * pure resolver in utils/cleanupTabs.test.ts.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import Cleanup from './Cleanup.vue'
import Fractionals from './Fractionals.vue'
import Sourceless from './Sourceless.vue'
import Duplicates from './Duplicates.vue'
import ErrorBanner from '../ui/ErrorBanner.vue'
import { fractionalSeries } from '../../fixtures/fractionals'
import { sampleSourcelessSeries } from '../../fixtures/sourceless'
import { sampleDuplicateSeries } from '../../fixtures/duplicates'
import type {
  CleanupFractionalsPane,
  CleanupSourcelessPane,
  CleanupDuplicatesPane,
} from './cleanup.types'

const fractionals: CleanupFractionalsPane = {
  series: fractionalSeries,
  loading: false,
  refreshing: false,
  error: null,
  busyIds: [],
  toggleError: null,
}

const sourceless: CleanupSourcelessPane = {
  series: sampleSourcelessSeries,
  loading: false,
  refreshing: false,
  error: null,
}

const duplicates: CleanupDuplicatesPane = {
  series: sampleDuplicateSeries,
  totalFiles: 258,
  totalBytes: 1_311_810_518,
  loading: false,
  refreshing: false,
  error: null,
}

function mountShell(props: Partial<InstanceType<typeof Cleanup>['$props']> = {}) {
  return mount(Cleanup, {
    props: { fractionals, sourceless, duplicates, ...props },
  })
}

/** The SegmentedTabs button whose visible label matches `label`. */
function tabButton(wrapper: ReturnType<typeof mountShell>, label: string) {
  return wrapper.findAll('button[role="tab"]').find(b => b.text().includes(label))
}

describe('Cleanup console shell', () => {
  it('defaults to the Fractionals tab and mounts only that body', () => {
    const wrapper = mountShell()
    expect(wrapper.findComponent(Fractionals).exists()).toBe(true)
    expect(wrapper.findComponent(Sourceless).exists()).toBe(false)
    expect(wrapper.findComponent(Duplicates).exists()).toBe(false)
  })

  it('shows the Sourceless tab alone when activeTab=sourceless', () => {
    const wrapper = mountShell({ activeTab: 'sourceless' })
    expect(wrapper.findComponent(Sourceless).exists()).toBe(true)
    expect(wrapper.findComponent(Fractionals).exists()).toBe(false)
    expect(wrapper.findComponent(Duplicates).exists()).toBe(false)
  })

  it('shows the Duplicates tab alone when activeTab=duplicates', () => {
    const wrapper = mountShell({ activeTab: 'duplicates' })
    expect(wrapper.findComponent(Duplicates).exists()).toBe(true)
    expect(wrapper.findComponent(Fractionals).exists()).toBe(false)
    expect(wrapper.findComponent(Sourceless).exists()).toBe(false)
  })

  it('emits set-tab with the picked key when a tab is clicked', async () => {
    const wrapper = mountShell()
    await tabButton(wrapper, 'Duplicates')!.trigger('click')
    expect(wrapper.emitted('set-tab')).toEqual([['duplicates']])
  })

  it('forwards Fractionals-tab actions up', () => {
    const wrapper = mountShell()
    const pane = wrapper.findComponent(Fractionals)
    pane.vm.$emit('open-series', 'series-42')
    pane.vm.$emit('toggle-ignore', { seriesId: 'series-42', ignore: true })
    pane.vm.$emit('clean-files', 'series-42')
    pane.vm.$emit('refresh')
    expect(wrapper.emitted('open-series')).toEqual([['series-42']])
    expect(wrapper.emitted('toggle-ignore')).toEqual([[{ seriesId: 'series-42', ignore: true }]])
    expect(wrapper.emitted('clean-files')).toEqual([['series-42']])
    expect(wrapper.emitted('refresh')).toEqual([['fractionals']])
  })

  it('forwards a Sourceless-tab review up, tagging its own refresh', () => {
    const wrapper = mountShell({ activeTab: 'sourceless' })
    const pane = wrapper.findComponent(Sourceless)
    pane.vm.$emit('review', 'series-7')
    pane.vm.$emit('refresh')
    expect(wrapper.emitted('review-sourceless')).toEqual([['series-7']])
    expect(wrapper.emitted('refresh')).toEqual([['sourceless']])
  })

  it('forwards a Duplicates-tab open-series up, tagging its own refresh', () => {
    const wrapper = mountShell({ activeTab: 'duplicates' })
    const pane = wrapper.findComponent(Duplicates)
    pane.vm.$emit('open-series', 'series-9')
    pane.vm.$emit('refresh')
    expect(wrapper.emitted('open-series')).toEqual([['series-9']])
    expect(wrapper.emitted('refresh')).toEqual([['duplicates']])
  })

  it('renders a tab load failure as a banner above its body (§16)', () => {
    const wrapper = mountShell({
      activeTab: 'duplicates',
      duplicates: { ...duplicates, error: 'Failed to load duplicate files' },
    })
    expect(wrapper.findComponent(ErrorBanner).exists()).toBe(true)
    expect(wrapper.text()).toContain('Failed to load duplicate files')
  })
})
