import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import Downloads from './Downloads.vue'
import type { DownloadItem } from './downloads.types'

const item = (seriesId: string, chapterId: string): DownloadItem => ({ chapterId, seriesId, seriesTitle: `Series ${seriesId}`, seriesCategory: 'Manga', coverUrl: '', number: 1, name: 'Chapter 1', state: 'wanted', provider: '101', providerName: 'Source' })

describe('Downloads fixed-series priority', () => {
  it('groups queue by series and emits one explicit priority action per series', async () => {
    const wrapper = mount(Downloads, { props: { items: [item('a', 'a1'), item('a', 'a2'), item('b', 'b1')], activeTab: 'queued' }, global: { stubs: { CoverImage: true, Chip: true } } })
    const buttons = wrapper.findAll('button').filter(b => b.text().includes('Prioritize queued chapters'))
    expect(buttons).toHaveLength(2)
    await buttons[0]!.trigger('click')
    expect(wrapper.emitted('set-priority')).toEqual([['a', true]])
  })
  it('keeps dormant snapshots cancellable and disables in-flight controls', async () => {
    const wrapper = mount(Downloads, {
      props: { items: [item('a', 'a1')], activeTab: 'queued', prioritySnapshots: [{ seriesId: 'missing', seriesTitle: 'Waiting series', remaining: 3 }], priorityBusyIds: ['a'], priorityError: 'Save failed' },
      global: { stubs: { CoverImage: true, Chip: true } },
    })
    expect(wrapper.text()).toContain('Save failed')
    expect(wrapper.text()).toContain('3 remaining')
    const enable = wrapper.findAll('button').find(button => button.text().includes('Prioritize queued chapters'))!
    expect(enable.attributes('disabled')).toBeDefined()
    const cancel = wrapper.findAll('button').find(button => button.text().includes('Cancel priority'))!
    await cancel.trigger('click')
    expect(wrapper.emitted('set-priority')).toEqual([['missing', false]])
  })

})
