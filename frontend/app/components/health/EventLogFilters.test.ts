/**
 * EventLogFilters — the pager + filter emits.
 *
 * Pins that Prev is disabled on the first page and Next on the last, that clicking
 * them emits `prev`/`next`, and that changing a filter select emits the new value.
 * Non-vacuous: drop the `atStart`/`atEnd` guards and the disabled assertions fail.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import EventLogFilters from './EventLogFilters.vue'

function findBtn(wrapper: ReturnType<typeof mount>, label: string) {
  return wrapper.findAll('button').find(b => b.text().includes(label))!
}

describe('EventLogFilters', () => {
  it('disables Prev on the first page and emits next', async () => {
    const wrapper = mount(EventLogFilters, {
      props: { status: '', eventType: '', page: 0, pageCount: 4, total: 200 },
    })
    expect(findBtn(wrapper, 'Prev').attributes('disabled')).toBeDefined()
    await findBtn(wrapper, 'Next').trigger('click')
    expect(wrapper.emitted('next')).toBeTruthy()
    wrapper.unmount()
  })

  it('disables Next on the last page and emits prev', async () => {
    const wrapper = mount(EventLogFilters, {
      props: { status: '', eventType: '', page: 3, pageCount: 4, total: 200 },
    })
    expect(findBtn(wrapper, 'Next').attributes('disabled')).toBeDefined()
    await findBtn(wrapper, 'Prev').trigger('click')
    expect(wrapper.emitted('prev')).toBeTruthy()
    wrapper.unmount()
  })

  it('emits the new filter value when a select changes', async () => {
    const wrapper = mount(EventLogFilters, {
      props: { status: '', eventType: '', page: 0, pageCount: 4, total: 200 },
    })
    const selects = wrapper.findAll('select')
    await selects[0]!.setValue('failed')
    expect(wrapper.emitted('update:status')?.[0]).toEqual(['failed'])
    await selects[1]!.setValue('download')
    expect(wrapper.emitted('update:eventType')?.[0]).toEqual(['download'])
    wrapper.unmount()
  })

  it('disables the pager while pending', () => {
    const wrapper = mount(EventLogFilters, {
      props: { status: '', eventType: '', page: 1, pageCount: 4, total: 200, pending: true },
    })
    expect(findBtn(wrapper, 'Prev').attributes('disabled')).toBeDefined()
    expect(findBtn(wrapper, 'Next').attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })
})
