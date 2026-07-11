/**
 * Reader route wiring — the thin glue between useReader, useReadingProgress, and
 * ReaderStrip. The data/windowing (useReader), the debounce/resume math
 * (useReadingProgress), and the scroll mechanics (ReaderStrip) are each unit-
 * tested in isolation; this pins only that the route CONNECTS them:
 *   1. the strip's `centered` event drives `record`;
 *   2. `chapter-finished` drives `markRead` with the chapter's pageCount;
 *   3. the computed resume target is passed to ReaderStrip as `initialScrollTo`;
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

vi.mock('~/composables/useReader', () => ({
  useReader: () => ({
    chapters,
    mountedChapters: ref([chapters.value[0]]),
    pageUrl: (id: string, n: number) => `x/${id}/${n}`,
    onNearTail: vi.fn(),
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
  it('passes the computed resume target to ReaderStrip as initialScrollTo', async () => {
    const wrapper = await mountReader()
    expect(wrapper.findComponent(ReaderStrip).props('initialScrollTo')).toEqual({ chapterId: 'ch-a', page: 3 })
  })

  it('records the position when the strip emits centered', async () => {
    const wrapper = await mountReader()
    wrapper.findComponent(ReaderStrip).vm.$emit('centered', { chapterId: 'ch-a', page: 6 })
    expect(record).toHaveBeenCalledWith('ch-a', 6)
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
