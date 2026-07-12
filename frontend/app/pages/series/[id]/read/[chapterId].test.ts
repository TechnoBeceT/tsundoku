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
import { describe, it, expect, vi, beforeEach, type MockInstance } from 'vitest'
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

/**
 * spyOnRouterReplace — installs a resolved no-op spy on the REAL router's
 * `replace` method (reachable off any mounted component's `$router` global
 * property, the same singleton `useRouter()` returns inside the page). FIX 5
 * (goToChapter's `router.replace`) needs a router instance to call, but
 * mocking `useRouter()` wholesale breaks Nuxt's OWN internal plugins (they
 * also call `useRouter()` for their navigation hooks) — spying on just this
 * one method, on the real instance, keeps everything else working and stops
 * the spied call from performing a real navigation (which would re-run route
 * middleware and hit the network in this test environment).
 */
function spyOnRouterReplace(wrapper: Awaited<ReturnType<typeof mountReader>>): MockInstance<(path: string) => Promise<unknown>> {
  const router = (wrapper.vm as unknown as { $router: { replace: (path: string) => Promise<unknown> } }).$router
  return vi.spyOn(router, 'replace').mockResolvedValue(undefined)
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

  it('marks a chapter read with its DECLARED pageCount when unmeasured (no visible-pages emitted for it)', async () => {
    const wrapper = await mountReader()
    wrapper.findComponent(ReaderStrip).vm.$emit('chapter-finished', 'ch-a')
    expect(markRead).toHaveBeenCalledWith('ch-a', 12)
  })

  // FIX 3: a Kaizoku import can DECLARE more pages than the CBZ really has, so
  // once the strip has actually measured/trimmed a chapter's real page count,
  // chapter-finished must use THAT — not the (possibly inflated) declared
  // pageCount — matching the rule onSliderNext already used.
  it('marks a chapter read with its MEASURED/trimmed count when it has been scrolled through', async () => {
    const wrapper = await mountReader()
    const strip = wrapper.findComponent(ReaderStrip)
    strip.vm.$emit('visible-pages', { chapterId: 'ch-a', count: 9 }) // ch-a declares 12
    await wrapper.vm.$nextTick()
    strip.vm.$emit('chapter-finished', 'ch-a')
    expect(markRead).toHaveBeenCalledWith('ch-a', 9)
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
    const routerReplace = spyOnRouterReplace(wrapper)

    currentChapterId.value = 'ch-a'
    nextChapter.value = chapters.value[1]!
    strip.vm.$emit('visible-pages', { chapterId: 'ch-a', count: 9 })
    await wrapper.vm.$nextTick()

    chrome.vm.$emit('next')

    expect(markRead).toHaveBeenCalledWith('ch-a', 9)
    expect(jumpToChapter).toHaveBeenCalledWith('ch-b')
    expect(markRead.mock.invocationCallOrder[0]!).toBeLessThan(jumpToChapter.mock.invocationCallOrder[0]!)
    // FIX 5: the URL is synced via router.REPLACE (never push — a chapter flip
    // must not grow browser history).
    expect(routerReplace).toHaveBeenCalledWith('/series/series-1/read/ch-b')
  })

  it('does nothing when there is no next chapter', async () => {
    const wrapper = await mountReader()
    const chrome = wrapper.findComponent(ReaderChrome)
    const routerReplace = spyOnRouterReplace(wrapper)
    currentChapterId.value = 'ch-b'
    nextChapter.value = null
    await wrapper.vm.$nextTick()

    chrome.vm.$emit('next')

    expect(markRead).not.toHaveBeenCalled()
    expect(jumpToChapter).not.toHaveBeenCalled()
    // Scoped to a chapter-navigation replace specifically: this Nuxt test
    // harness's own auth middleware independently fires an UNRELATED
    // `router.replace('/')` during app init (a pre-existing test-environment
    // quirk — checkSession's fetch always fails under happy-dom), which a
    // bare `not.toHaveBeenCalled()` would false-positive on.
    expect(routerReplace).not.toHaveBeenCalledWith(expect.stringContaining('/read/'))
  })

  it('prev marks nothing — going back is a correction, not a completion — and still syncs the URL', async () => {
    const wrapper = await mountReader()
    const chrome = wrapper.findComponent(ReaderChrome)
    const routerReplace = spyOnRouterReplace(wrapper)
    prevChapter.value = chapters.value[0]!
    await wrapper.vm.$nextTick()

    chrome.vm.$emit('prev')

    expect(markRead).not.toHaveBeenCalled()
    expect(jumpToChapter).toHaveBeenCalledWith('ch-a')
    expect(routerReplace).toHaveBeenCalledWith('/series/series-1/read/ch-a')
  })

  // Regression: `visiblePages` used to be a single route-level ref updated by
  // whichever chapter's `visible-pages` last fired — a value that is inherently
  // PER-CHAPTER stored as if it were global. After a chapter jump it still held
  // the PREVIOUS chapter's count, so a second `next` tapped before the new
  // chapter ever scrolled persisted a `lastReadPage` with no relation to that
  // chapter's real length. Fixed by keying the count by chapter id
  // (`visiblePagesByChapter`).
  it('reproduces the reviewer\'s exact sequence: measure A, next (lands on B), next AGAIN before B ever scrolls — B is marked with its OWN count/fallback, never A\'s', async () => {
    const wrapper = await mountReader()
    const chrome = wrapper.findComponent(ReaderChrome)
    const strip = wrapper.findComponent(ReaderStrip)
    const chapterC = { id: 'ch-c', number: 3, name: 'Three', pageCount: 5, read: false, lastReadPage: 0 }

    // 1. Measure chapter A (reviewer's reproduction: 9 pages).
    currentChapterId.value = 'ch-a'
    nextChapter.value = chapters.value[1]! // ch-b
    strip.vm.$emit('visible-pages', { chapterId: 'ch-a', count: 9 })
    await wrapper.vm.$nextTick()

    // 2. Tap next: marks A with A's own measured count (9 — correct), lands on B.
    chrome.vm.$emit('next')
    expect(markRead).toHaveBeenNthCalledWith(1, 'ch-a', 9)

    // Simulate the landing on B that `jumpToChapter` would have driven — B has
    // NOT scrolled, so no `visible-pages` has ever fired for it.
    currentChapterId.value = 'ch-b'
    nextChapter.value = chapterC

    // 3. Tap next AGAIN before any scroll on B (`visible-pages` for ch-b never
    //    emitted). The bug: the old single shared `visiblePages` ref still held
    //    A's 9 here, so `markRead('ch-b', 9)` fired — a number belonging to a
    //    different chapter. The fix: B has no entry in the per-chapter map, so
    //    it falls back to B's own declared pageCount (20), never A's 9.
    chrome.vm.$emit('next')

    expect(markRead).toHaveBeenNthCalledWith(2, 'ch-b', 20)
    expect(markRead).not.toHaveBeenCalledWith('ch-b', 9)
    expect(jumpToChapter).toHaveBeenCalledWith('ch-c')
  })

  it('next on an UNMEASURED chapter (no visible-pages emitted for it yet) falls back to its declared pageCount, never a sibling chapter\'s count', async () => {
    const wrapper = await mountReader()
    const chrome = wrapper.findComponent(ReaderChrome)
    const strip = wrapper.findComponent(ReaderStrip)

    // Chapter A gets measured at 9 (a different chapter's count that must
    // never leak onto B).
    currentChapterId.value = 'ch-a'
    strip.vm.$emit('visible-pages', { chapterId: 'ch-a', count: 9 })
    await wrapper.vm.$nextTick()

    // Now the reader is centred on B, which has NEVER emitted visible-pages
    // (e.g. it fit on-screen with no scroll). ch-b's fixture pageCount is 20.
    currentChapterId.value = 'ch-b'
    nextChapter.value = { id: 'ch-c', number: 3, name: 'Three', pageCount: 5, read: false, lastReadPage: 0 }
    await wrapper.vm.$nextTick()

    chrome.vm.$emit('next')

    expect(markRead).toHaveBeenCalledWith('ch-b', 20) // ch-b's declared pageCount, NOT ch-a's 9
    expect(jumpToChapter).toHaveBeenCalledWith('ch-c')
  })

  it('the slider denominator resets to the new chapter\'s own count after a jump — not the previous chapter\'s', async () => {
    const wrapper = await mountReader()
    const chrome = wrapper.findComponent(ReaderChrome)
    const strip = wrapper.findComponent(ReaderStrip)

    currentChapterId.value = 'ch-a'
    strip.vm.$emit('visible-pages', { chapterId: 'ch-a', count: 9 })
    await wrapper.vm.$nextTick()
    expect(chrome.props('visiblePages')).toBe(9)

    // Jump to B — B has not been measured yet, so the denominator must be 0
    // (safe default), NEVER A's leftover 9.
    currentChapterId.value = 'ch-b'
    await wrapper.vm.$nextTick()
    expect(chrome.props('visiblePages')).toBe(0)

    // Once B is actually measured, the denominator reflects B's real count.
    strip.vm.$emit('visible-pages', { chapterId: 'ch-b', count: 20 })
    await wrapper.vm.$nextTick()
    expect(chrome.props('visiblePages')).toBe(20)
  })
})
