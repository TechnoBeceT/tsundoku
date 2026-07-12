/**
 * Reader route — `?page=` resume override (FIX B).
 *
 * The series page's "Continue" FAB threads its OWN already-decided resume
 * page via `?page=` (see `onResume`'s doc comment in `[id]/index.vue`) because
 * re-deriving it here via `resumeTarget(chapters.value)` always hits that
 * function's "started" branch (the deep-linked `chapterId` is always in the
 * loaded list) — which opens at the chapter's own saved `lastReadPage`, NOT
 * whatever page the FAB actually decided. This mattered for the all-read
 * case: `resumeTarget` opens the last chapter at page 0 (start it over), but
 * without the override the reader landed on that chapter's FINAL page
 * instead. This test pins BOTH sides: an explicit `?page=` wins over the
 * mocked `resumeTarget`'s own page, and an ABSENT `?page=` (a plain
 * chapter-row deep link) still resolves via `resumeTarget` as before.
 */
import { describe, it, expect, vi } from 'vitest'
import { ref } from 'vue'
import { mountSuspended, mockNuxtImport } from '@nuxt/test-utils/runtime'
import ReaderStrip from '~/components/reader/ReaderStrip.vue'
import type { ReaderChapter } from '~/composables/useReader'
import ReadPage from './[chapterId].vue'

const chapters = ref<ReaderChapter[]>([
  { id: 'ch-a', number: 1, name: 'One', pageCount: 12, read: true, lastReadPage: 9 },
])

// Simulates resumeTarget's "started" branch: the deep-linked chapter's own
// saved lastReadPage (9) — what the bug used regardless of the FAB's intent.
const resumeTarget = vi.fn(() => ({ chapterId: 'ch-a', page: 9 }))

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
    onNearHead: vi.fn(),
    hasPrev: ref(false),
    hasNext: ref(false),
    setCurrentChapter: vi.fn(),
    currentChapterId: ref(null),
    prevChapter: ref(null),
    nextChapter: ref(null),
    jumpToChapter: vi.fn(),
    loading: ref(false),
    error: ref(null),
    startChapterId: 'ch-a',
    refresh: vi.fn(),
    scrollRequest,
    requestScroll,
  }),
}))

vi.mock('~/composables/useReadingProgress', () => ({
  useReadingProgress: () => ({ record: vi.fn(), markRead: vi.fn(), resumeTarget, flush: vi.fn() }),
}))

// A MUTABLE query object, read fresh by useRoute() on every mount — this
// mirrors [chapterId].test.ts's pattern of mutating shared refs rather than
// re-registering the mock per test (mockNuxtImport is a hoisted macro; it is
// not designed to be re-invoked inside individual `it()` blocks).
let routeQuery: Record<string, string> = {}
mockNuxtImport('useRoute', () => () => ({ params: { id: 'series-1', chapterId: 'ch-a' }, query: routeQuery }))

// happy-dom has no IntersectionObserver — ReaderStrip's onMounted needs it.
class IOStub {
  observe(): void { /* no-op stub */ }
  disconnect(): void { /* no-op stub */ }
}
vi.stubGlobal('IntersectionObserver', IOStub)

async function mountReader() {
  return mountSuspended(ReadPage, { global: { stubs: { Icon: true } } })
}

describe('reader route — resume page override', () => {
  it('an explicit ?page= wins over resumeTarget\'s own page (the all-read-resume fix)', async () => {
    routeQuery = { page: '0' }
    mockTokenCounter = 0
    scrollRequest.value = null

    const wrapper = await mountReader()

    // resumeTarget itself says page 9 (this chapter's own saved progress),
    // but the FAB's ?page=0 must be what the strip actually scrolls to.
    expect(wrapper.findComponent(ReaderStrip).props('scrollRequest')).toEqual({ chapterId: 'ch-a', page: 0, token: 1 })
  })

  it('with no ?page=, resolves via resumeTarget as before (a plain chapter-row deep link)', async () => {
    routeQuery = {}
    mockTokenCounter = 0
    scrollRequest.value = null

    const wrapper = await mountReader()

    expect(wrapper.findComponent(ReaderStrip).props('scrollRequest')).toEqual({ chapterId: 'ch-a', page: 9, token: 1 })
  })

  it('an invalid ?page= (non-numeric/negative) is ignored, falling back to resumeTarget', async () => {
    routeQuery = { page: 'nope' }
    mockTokenCounter = 0
    scrollRequest.value = null

    const wrapper = await mountReader()

    expect(wrapper.findComponent(ReaderStrip).props('scrollRequest')).toEqual({ chapterId: 'ch-a', page: 9, token: 1 })
  })
})
