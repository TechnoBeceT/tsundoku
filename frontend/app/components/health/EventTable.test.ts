/**
 * EventTable — row rendering, the select emit, and the empty/loading states.
 *
 * Pins that a row click emits `select` with its event, that a failed row carries
 * the category badge (a success does not), that the source column hides when
 * `showSource=false`, and that empty + pending render their own states.
 *
 * Non-vacuous: drop the `@click="emit('select', e)"` and the emit assertion
 * fails; leak the category badge onto a success row and the count assertion fails.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import EventTable from './EventTable.vue'
import type { SourceEventRecord } from './sourceReport.types'

function evt(overrides: Partial<SourceEventRecord> = {}): SourceEventRecord {
  return {
    id: 'e1', sourceKey: 'ComicK', sourceId: '1', sourceName: 'ComicK', language: 'en',
    eventType: 'download', status: 'failed', durationMs: 60000, errorMessage: 'timeout',
    errorCategory: 'timeout', itemsCount: null, metadata: {}, createdAt: '2026-07-19T11:00:00Z',
    ...overrides,
  }
}

describe('EventTable', () => {
  it('emits select with the clicked event', async () => {
    const e = evt()
    const wrapper = mount(EventTable, { props: { events: [e] } })
    await wrapper.find('.evt__row').trigger('click')
    expect(wrapper.emitted('select')?.[0]).toEqual([e])
    wrapper.unmount()
  })

  it('shows a category badge on a failure but not on a success', () => {
    const wrapper = mount(EventTable, {
      props: { events: [evt({ id: 'a', status: 'failed' }), evt({ id: 'b', status: 'success', errorCategory: null, errorMessage: null })] },
    })
    // One category badge total (the failed row only).
    expect(wrapper.findAll('.cat')).toHaveLength(1)
    wrapper.unmount()
  })

  it('hides the source column when showSource=false', () => {
    const withSource = mount(EventTable, { props: { events: [evt()], showSource: true } })
    expect(withSource.find('.evt__source').exists()).toBe(true)
    withSource.unmount()

    const without = mount(EventTable, { props: { events: [evt()], showSource: false } })
    expect(without.find('.evt__source').exists()).toBe(false)
    without.unmount()
  })

  it('renders the empty state and skeletons', () => {
    const empty = mount(EventTable, { props: { events: [], emptyLabel: 'Nothing here' } })
    expect(empty.text()).toContain('Nothing here')
    empty.unmount()

    const loading = mount(EventTable, { props: { events: [], pending: true } })
    expect(loading.findAll('.evt__skeleton').length).toBeGreaterThan(0)
    loading.unmount()
  })
})
