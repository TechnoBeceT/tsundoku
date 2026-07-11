/**
 * Reader route wiring — the thin glue between useReader, useReadingProgress, and
 * ReaderStrip. The data/windowing (useReader), the debounce/resume math
 * (useReadingProgress), and the scroll mechanics (ReaderStrip) are each unit-
 * tested in isolation; this pins only that the route CONNECTS them:
 *   1. the strip's `centered` event drives `record` AND `setCurrentChapter`;
 *   2. `chapter-finished` drives `markRead` with the chapter's pageCount;
 *   3. the computed resume target is published as the strip's `scrollRequest`;
 *   4. leaving the route flushes the pending debounced write.
 *
 * The two composables are mocked to spies; useRoute is mocked for the params.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ref } from 'vue'
import { mountSuspended, mockNuxtImport } from '@nuxt/test-utils/runtime'
import ReaderStrip from '~/components/reader/ReaderStrip.vue'
import type { ReaderChapter } from '~/composables/useReader'
import ReadPage from './[chapterId].vue'

const chapters = ref<ReaderChapter[]>([
  { id: 'ch-a', number: 1, name: 'One', pageCount: 12, read: false, lastReadPage: 3 },
  { id: 'ch-b', number: 2, name: 'Two', pageCount: 20, read: false, lastReadPage: 0 },
])

const record = vi.fn()
const markRead = vi.fn()
const flush = vi.fn()
const resumeTarget = vi.fn(() => ({ chapterId: 'ch-a', page: 3 }))
const onNearHead = vi.fn()
const setCurrentChapter = vi.fn()

vi.mock('~/composables/useReader', () => ({
  useReader: () => ({
    chapters,
    mountedChapters: ref([chapters.value[0]]),
    pageUrl: (id: string, n: number) => `x/${id}/${n}`,
    onNearTail: vi.fn(),
    onNearHead,
    hasPrev: ref(false),
    setCurrentChapter,
    loading: ref(false),
    error: ref(null),
    startChapterId: 'ch-a',
    refresh: vi.fn(),
  }),
}))

vi.mock('~/composables/useReadingProgress', () => ({
  useReadingProgress: () => ({ record, markRead, resumeTarget, flush }),
}))

mockNuxtImport('useRoute', () => () => ({ params: { id: 'series-1', chapterId: 'ch-a' } }))

// happy-dom has no IntersectionObserver — ReaderStrip's onMounted needs it.
class IOStub {
  observe(): void { /* no-op stub */ }
  disconnect(): void { /* no-op stub */ }
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.stubGlobal('IntersectionObserver', IOStub)
})

async function mountReader() {
  return mountSuspended(ReadPage, { global: { stubs: { Icon: true } } })
}

describe('reader route wiring', () => {
  it('publishes the computed resume target as the strip scrollRequest', async () => {
    const wrapper = await mountReader()
    expect(wrapper.findComponent(ReaderStrip).props('scrollRequest')).toEqual({ chapterId: 'ch-a', page: 3, token: 1 })
  })

  it('wires near-head to onNearHead', async () => {
    const wrapper = await mountReader()
    wrapper.findComponent(ReaderStrip).vm.$emit('near-head')
    expect(onNearHead).toHaveBeenCalled()
  })

  it('records the position AND tracks the centred chapter when the strip emits centered', async () => {
    const wrapper = await mountReader()
    wrapper.findComponent(ReaderStrip).vm.$emit('centered', { chapterId: 'ch-a', page: 6 })
    expect(record).toHaveBeenCalledWith('ch-a', 6)
    expect(setCurrentChapter).toHaveBeenCalledWith('ch-a')
  })

  it('marks a chapter read with its pageCount when the strip emits chapter-finished', async () => {
    const wrapper = await mountReader()
    wrapper.findComponent(ReaderStrip).vm.$emit('chapter-finished', 'ch-a')
    expect(markRead).toHaveBeenCalledWith('ch-a', 12)
  })

  it('flushes the pending write when the route unmounts', async () => {
    const wrapper = await mountReader()
    wrapper.unmount()
    expect(flush).toHaveBeenCalled()
  })
})
