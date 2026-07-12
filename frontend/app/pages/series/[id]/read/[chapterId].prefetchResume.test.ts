/**
 * Reader route — prefetch seeds from the RESUME TARGET, not page 0 (reviewer
 * fix, reader-page-prefetch branch).
 *
 * BUG: `useChapterPrefetch` orders its "nearest first" fetch off `currentPage`,
 * which the route only ever wrote later, via `onCentered` (fired by the
 * strip's FIRST real scroll-settle). On a RESUME open — e.g. the owner left
 * off at page 80 — `currentPage` was still its initial `0` the moment the
 * prefetcher's `immediate: true` watcher first ran, so it centred on page 0
 * and burned its first 5 concurrent requests warming pages nobody was about
 * to see, while the actually-visible pages (near 80) competed for the same
 * handful of connections. That is precisely the "makes opening a chapter
 * worse" case the whole prefetch feature exists to avoid.
 *
 * This test drives the REAL `useChapterPrefetch` (not mocked) through the
 * mounted route, with `chapters` populated ASYNCHRONOUSLY after mount — the
 * same timing as the real `useReader.refresh()` network load — so it also
 * proves the seeding survives that timing, not just a synchronous-mock
 * coincidence.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { ref, nextTick } from 'vue'
import { flushPromises } from '@vue/test-utils'
import { mountSuspended, mockNuxtImport } from '@nuxt/test-utils/runtime'
import type { ReaderChapter } from '~/composables/useReader'
import ReadPage from './[chapterId].vue'

// Starts EMPTY — populated mid-test to mirror useReader's real async load.
const chapters = ref<ReaderChapter[]>([])

const scrollRequest = ref<{ chapterId: string, page: number, token: number } | null>(null)

vi.mock('~/composables/useReader', () => ({
  useReader: () => ({
    chapters,
    mountedChapters: ref([]),
    pageUrl: (id: string, n: number) => `/api/series/s1/chapters/${id}/pages/${n}`,
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
    requestScroll: vi.fn(),
  }),
}))

// The owner already read this chapter up to page 80 — a genuine resume, not a
// fresh open.
vi.mock('~/composables/useReadingProgress', () => ({
  useReadingProgress: () => ({
    record: vi.fn(),
    markRead: vi.fn(),
    resumeTarget: vi.fn(() => ({ chapterId: 'ch-a', page: 80 })),
    flush: vi.fn(),
  }),
}))

mockNuxtImport('useRoute', () => () => ({ params: { id: 'series-1', chapterId: 'ch-a' }, query: {} }))

// happy-dom has no IntersectionObserver — ReaderStrip's onMounted needs it.
class IOStub {
  observe(): void { /* no-op stub */ }
  disconnect(): void { /* no-op stub */ }
}

/** A parked Image stub (mirrors useChapterPrefetch.test.ts): records request
 *  order via `src` but never resolves, so only the first
 *  PREFETCH_CONCURRENCY (5) requests ever fire — exactly what "nearest-first
 *  ordering" needs to observe. */
class FakeImage {
  onload: (() => void) | null = null
  onerror: (() => void) | null = null
  private _src = ''
  get src(): string { return this._src }
  set src(v: string) {
    this._src = v
    requestedUrls.push(v)
  }
}
let requestedUrls: string[] = []

beforeEach(() => {
  requestedUrls = []
  chapters.value = []
  vi.stubGlobal('IntersectionObserver', IOStub)
  vi.stubGlobal('Image', FakeImage)
})

const PAGE_INDEX_RE = /pages\/(\d+)/
function pageIndexOf(url: string): number {
  return Number(PAGE_INDEX_RE.exec(url)?.[1])
}

describe('reader route — prefetch seeds from the resume target', () => {
  it('opening at page 80 requests pages near 80 first, NOT near 0', async () => {
    await mountSuspended(ReadPage, { global: { stubs: { Icon: true } } })

    // Chapters resolve AFTER mount — mirrors useReader.refresh()'s real
    // network-load timing, the exact window the bug lived in.
    chapters.value = [
      { id: 'ch-a', number: 1, name: 'One', pageCount: 200, read: false, lastReadPage: 80 },
    ]
    await nextTick()
    await flushPromises()

    const firstFive = requestedUrls.slice(0, 5).map(pageIndexOf)
    // Nearest-to-80-first (alternating outward), matching useChapterPrefetch's
    // own `pageOrder`. Without the fix this is [0, 1, 2, 3, 4].
    expect(firstFive).toEqual([80, 81, 79, 82, 78])
  })
})
