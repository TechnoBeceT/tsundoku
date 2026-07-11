/**
 * Reader route wiring — the thin glue between useReader, useReadingProgress,
 * ReaderStrip, and ReaderChrome's page slider. The data/windowing (useReader),
 * the debounce/resume math (useReadingProgress), and the scroll mechanics
 * (ReaderStrip) are each unit-tested in isolation; this pins only that the
 * route CONNECTS them:
 *   1. the strip's `centered` event drives `record` AND `setCurrentChapter`;
 *   2. `chapter-finished` drives `markRead` with the chapter's pageCount;
 *   3. the computed resume target is published as the strip's `scrollRequest`;
 *   4. leaving the route flushes the pending debounced write;
 *   5. the slider's `seek` optimistically moves the page AND suppresses the
 *      strip's own resulting `centered` echo, so the slider never fights a drag
 *      (the feedback-loop guard — see the route's doc comment);
 *   6. the slider's `next` marks the current chapter read before navigating,
 *      `prev` marks nothing.
 *
 * The two composables are mocked to spies; useRoute is mocked for the params.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ref } from 'vue'
import { mountSuspended, mockNuxtImport } from '@nuxt/test-utils/runtime'
import ReaderStrip from '~/components/reader/ReaderStrip.vue'
import ReaderChrome from '~/components/reader/ReaderChrome.vue'
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

// Chapter-navigation state — real `useReader` derives these from `chapters`
// via `currentChapterId`; the mock owns them directly so tests can drive
// prev/next/currentChapterId independently of the (also mocked) centered flow.
const currentChapterId = ref<string | null>(null)
const prevChapter = ref<ReaderChapter | null>(null)
const nextChapter = ref<ReaderChapter | null>(null)
const hasNext = ref(true)
const jumpToChapter = vi.fn()

// Fix 4: `scrollRequest`/`requestScroll` mirror the real `useReader` contract —
// the route publishes its resume-anchor scroll through `requestScroll` (never
// a hardcoded `{ token: 1 }` literal), which mints tokens itself. This mock
// reproduces that so `publishes the computed resume target...` below still
// pins the exact shape the route ends up requesting.
const scrollRequest = ref<{ chapterId: string, page: number, token: number } | null>(null)
let mockTokenCounter = 0
const requestScroll = vi.fn((chapterId: string, page: number) => {
  mockTokenCounter += 1
  scrollRequest.value = { chapterId, page, token: mockTokenCounter }
})

vi.mock('~/composables/useReader', () => ({
  useReader: () => ({
    chapters,
    mountedChapters: ref([chapters.value[0]]),
    pageUrl: (id: string, n: number) => `x/${id}/${n}`,
    onNearTail: vi.fn(),
    onNearHead,
    hasPrev: ref(false),
    hasNext,
    setCurrentChapter,
    currentChapterId,
    prevChapter,
    nextChapter,
    jumpToChapter,
    loading: ref(false),
    error: ref(null),
    startChapterId: 'ch-a',
    refresh: vi.fn(),
    scrollRequest,
    requestScroll,
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
  scrollRequest.value = null
  mockTokenCounter = 0
  currentChapterId.value = null
  prevChapter.value = null
  nextChapter.value = null
  hasNext.value = true
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

describe('slider seek vs. the strip\'s own echo (feedback-loop guard)', () => {
  it('sets the slider page optimistically on seek, drops the echoed centered inside the suppression window, and still honours a later genuine centered', async () => {
    vi.useFakeTimers()
    try {
      const wrapper = await mountReader()
      const chrome = wrapper.findComponent(ReaderChrome)
      const strip = wrapper.findComponent(ReaderStrip)

      strip.vm.$emit('centered', { chapterId: 'ch-a', page: 2 })
      await wrapper.vm.$nextTick()
      expect(chrome.props('page')).toBe(2)

      // A drag/click seek moves the slider immediately — it does not wait for
      // the strip to scroll and report back.
      chrome.vm.$emit('seek', 5)
      await wrapper.vm.$nextTick()
      expect(chrome.props('page')).toBe(5)

      // The strip's own resulting scroll reports a DIFFERENT page (the
      // viewport-midpoint vs. seek-target-top anchor mismatch) inside the
      // suppression window — an unguarded route would let this overwrite the
      // optimistic value and visibly fight the drag.
      strip.vm.$emit('centered', { chapterId: 'ch-a', page: 4 })
      await wrapper.vm.$nextTick()
      expect(chrome.props('page')).toBe(5)

      // Once the suppression window elapses, a genuine centered event (real
      // scrolling, not the seek's own echo) moves the slider again.
      vi.advanceTimersByTime(300)
      strip.vm.$emit('centered', { chapterId: 'ch-a', page: 7 })
      await wrapper.vm.$nextTick()
      expect(chrome.props('page')).toBe(7)
    }
    finally {
      vi.useRealTimers()
    }
  })

  it('still records + tracks the chapter for a centered event inside the suppression window (only the page assignment is dropped)', async () => {
    const wrapper = await mountReader()
    const chrome = wrapper.findComponent(ReaderChrome)
    const strip = wrapper.findComponent(ReaderStrip)

    chrome.vm.$emit('seek', 5)
    strip.vm.$emit('centered', { chapterId: 'ch-a', page: 4 })

    expect(record).toHaveBeenCalledWith('ch-a', 4)
    expect(setCurrentChapter).toHaveBeenCalledWith('ch-a')
  })
})

describe('slider prev/next chapter navigation', () => {
  it('next marks the current chapter read with the trimmed visiblePages count, THEN jumps forward', async () => {
    const wrapper = await mountReader()
    const chrome = wrapper.findComponent(ReaderChrome)
    const strip = wrapper.findComponent(ReaderStrip)

    currentChapterId.value = 'ch-a'
    nextChapter.value = chapters.value[1]!
    strip.vm.$emit('visible-pages', { chapterId: 'ch-a', count: 9 })
    await wrapper.vm.$nextTick()

    chrome.vm.$emit('next')

    expect(markRead).toHaveBeenCalledWith('ch-a', 9)
    expect(jumpToChapter).toHaveBeenCalledWith('ch-b')
    expect(markRead.mock.invocationCallOrder[0]!).toBeLessThan(jumpToChapter.mock.invocationCallOrder[0]!)
  })

  it('does nothing when there is no next chapter', async () => {
    const wrapper = await mountReader()
    const chrome = wrapper.findComponent(ReaderChrome)
    currentChapterId.value = 'ch-b'
    nextChapter.value = null
    await wrapper.vm.$nextTick()

    chrome.vm.$emit('next')

    expect(markRead).not.toHaveBeenCalled()
    expect(jumpToChapter).not.toHaveBeenCalled()
  })

  it('prev marks nothing — going back is a correction, not a completion', async () => {
    const wrapper = await mountReader()
    const chrome = wrapper.findComponent(ReaderChrome)
    prevChapter.value = chapters.value[0]!
    await wrapper.vm.$nextTick()

    chrome.vm.$emit('prev')

    expect(markRead).not.toHaveBeenCalled()
    expect(jumpToChapter).toHaveBeenCalledWith('ch-a')
  })
})
