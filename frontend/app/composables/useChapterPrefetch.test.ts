/**
 * useChapterPrefetch — the whole-chapter background image-cache warmer.
 *
 * Pins:
 *   1. A `currentChapter` change enqueues ALL its pages (not a small radius).
 *   2. The fetch order is nearest-to-`currentPage` first.
 *   3. In-flight requests never exceed the bounded concurrency cap.
 *   4. The `nextChapter` is only enqueued once the current chapter's queue drains.
 *   5. A LATER `currentChapter` change abandons whichever queue was running
 *      (current OR next), including a next-chapter queue already in flight.
 *   6. Every requested URL carries `pageUrl`'s `?v=` cache buster verbatim.
 *   7. A page load failure (`onerror` — the tail-404 case) resolves like a
 *      success: it neither stalls nor kills the queue, and is never retried.
 *   8. A URL already requested by this instance is never re-fired (re-entering
 *      an already-warmed chapter is a no-op).
 *   9. `dispose()` stops a running queue from enqueuing any further page.
 *
 * `globalThis.Image` is stubbed with a controllable fake (mirrors
 * ReaderStrip.test.ts's IntersectionObserver stub idiom): setting `.src`
 * records the request and PARKS it — nothing resolves until the test fires
 * `resolveOldest()`/`failOldest()` — so concurrency + ordering are asserted
 * deterministically instead of racing real timers.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { ref } from 'vue'
import { useChapterPrefetch, type PrefetchChapter } from './useChapterPrefetch'

// ---- Image() stub -----------------------------------------------------------

class FakeImage {
  onload: (() => void) | null = null
  onerror: (() => void) | null = null
  private _src = ''
  get src(): string { return this._src }
  set src(v: string) {
    this._src = v
    requestedUrls.push(v)
    pending.push(this)
  }
}

/** Every URL ever assigned to an Image's `src`, in request order. */
let requestedUrls: string[] = []
/** FakeImage instances still awaiting `resolveOldest`/`failOldest`, oldest first. */
let pending: FakeImage[] = []

/** Resolves the oldest still-pending image request as a successful load. */
function resolveOldest(): void {
  pending.shift()?.onload?.()
}

/** Resolves the oldest still-pending image request as a failed load (404). */
function failOldest(): void {
  pending.shift()?.onerror?.()
}

beforeEach(() => {
  requestedUrls = []
  pending = []
  vi.stubGlobal('Image', FakeImage)
})

/** A same-origin-shaped versioned URL, mirroring useReader.pageUrl's `?v=` contract. */
function pageUrl(chapterId: string, n: number): string {
  return `/api/series/s1/chapters/${chapterId}/pages/${n}?v=ver-${chapterId}`
}

const PAGE_INDEX_RE = /pages\/(\d+)/

/** Extracts the page index from a request URL built by `pageUrl` above. */
function pageIndexOf(url: string): string | undefined {
  return PAGE_INDEX_RE.exec(url)?.[1]
}

const chA: PrefetchChapter = { id: 'ch-a', pageCount: 12 }
const chB: PrefetchChapter = { id: 'ch-b', pageCount: 4 }

describe('useChapterPrefetch — current chapter', () => {
  it('requests every page of the current chapter, not just a radius around the centre', async () => {
    const current = ref<PrefetchChapter | null>(chA)
    const next = ref<PrefetchChapter | null>(null)
    const currentPage = ref(0)
    useChapterPrefetch(current, next, currentPage, pageUrl)
    await flushPromises()

    // Drain the whole queue (12 pages, 5 at a time).
    for (let i = 0; i < 12; i++) {
      resolveOldest()
      await flushPromises()
    }

    const requestedPages = new Set(requestedUrls.map((u) => pageIndexOf(u)))
    expect(requestedPages.size).toBe(12)
    for (let n = 0; n < 12; n++) expect(requestedPages.has(String(n))).toBe(true)
  })

  it('orders the fetch nearest-to-currentPage first', async () => {
    const current = ref<PrefetchChapter | null>(chA)
    const next = ref<PrefetchChapter | null>(null)
    const currentPage = ref(6)
    useChapterPrefetch(current, next, currentPage, pageUrl)
    await flushPromises()

    // The first PREFETCH_CONCURRENCY (5) requests fired synchronously are the
    // pages nearest page 6: 6, 7, 5, 8, 4 (alternating outward).
    const firstFive = requestedUrls.map((u) => pageIndexOf(u))
    expect(firstFive).toEqual(['6', '7', '5', '8', '4'])
  })
})

describe('useChapterPrefetch — bounded concurrency', () => {
  it('never has more than PREFETCH_CONCURRENCY (5) requests in flight at once', async () => {
    const current = ref<PrefetchChapter | null>(chA) // 12 pages
    const next = ref<PrefetchChapter | null>(null)
    const currentPage = ref(0)
    useChapterPrefetch(current, next, currentPage, pageUrl)
    await flushPromises()

    expect(pending.length).toBe(5)

    // Resolving one in-flight request admits exactly one more — the pool
    // never bursts past the cap.
    resolveOldest()
    await flushPromises()
    expect(pending.length).toBe(5)

    resolveOldest()
    await flushPromises()
    expect(pending.length).toBe(5)
  })
})

describe('useChapterPrefetch — next chapter', () => {
  it('is only enqueued once the current chapter drains', async () => {
    const current = ref<PrefetchChapter | null>(chB) // 4 pages
    const next = ref<PrefetchChapter | null>(chA)
    const currentPage = ref(0)
    useChapterPrefetch(current, next, currentPage, pageUrl)
    await flushPromises()

    // Only ch-b's 4 pages should have fired — none of ch-a's yet.
    expect(requestedUrls.every((u) => u.includes('/chapters/ch-b/'))).toBe(true)
    expect(requestedUrls.length).toBe(4)

    // Drain ch-b.
    for (let i = 0; i < 4; i++) {
      resolveOldest()
      await flushPromises()
    }

    // ch-a (the next chapter) now starts.
    expect(requestedUrls.some((u) => u.includes('/chapters/ch-a/'))).toBe(true)
  })
})

describe('useChapterPrefetch — abandonment on chapter change', () => {
  it('abandons a still-running next-chapter queue when the current chapter changes again', async () => {
    const current = ref<PrefetchChapter | null>(chB) // 4 pages
    const next = ref<PrefetchChapter | null>(chA) // 12 pages
    const currentPage = ref(0)
    useChapterPrefetch(current, next, currentPage, pageUrl)
    await flushPromises()

    // Drain ch-b so the next-chapter (ch-a) queue starts.
    for (let i = 0; i < 4; i++) {
      resolveOldest()
      await flushPromises()
    }
    expect(pending.length).toBe(5) // ch-a's first batch in flight
    expect(pending.every((_, idx) => requestedUrls[requestedUrls.length - 5 + idx]?.includes('/chapters/ch-a/'))).toBe(true)

    const countBeforeJump = requestedUrls.length

    // The reader jumps to a brand new chapter mid-way through ch-a's prefetch.
    const chC: PrefetchChapter = { id: 'ch-c', pageCount: 3 }
    current.value = chC
    next.value = null
    await flushPromises()

    // Resolve everything still parked (the abandoned ch-a in-flight requests +
    // ch-c's own). No FURTHER ch-a pages should be requested beyond what was
    // already in flight at the moment of the jump.
    for (let i = 0; i < 10 && pending.length > 0; i++) {
      resolveOldest()
      await flushPromises()
    }

    const chAAfterJump = requestedUrls.slice(countBeforeJump).filter((u) => u.includes('/chapters/ch-a/'))
    expect(chAAfterJump).toEqual([])
    // ch-c's pages did get requested — the new current chapter is not abandoned.
    expect(requestedUrls.some((u) => u.includes('/chapters/ch-c/'))).toBe(true)
  })
})

describe('useChapterPrefetch — versioned URLs', () => {
  it('every requested URL carries the ?v=<pageVersion> cache buster from pageUrl', async () => {
    const current = ref<PrefetchChapter | null>(chB)
    const next = ref<PrefetchChapter | null>(null)
    const currentPage = ref(0)
    useChapterPrefetch(current, next, currentPage, pageUrl)
    await flushPromises()

    expect(requestedUrls.length).toBeGreaterThan(0)
    for (const url of requestedUrls) expect(url).toContain('?v=ver-ch-b')
  })
})

describe('useChapterPrefetch — tail-404 tolerance', () => {
  it('a failed page load does not stall or kill the queue, and is never retried', async () => {
    const current = ref<PrefetchChapter | null>(chB) // 4 pages
    const next = ref<PrefetchChapter | null>(null)
    const currentPage = ref(0)
    useChapterPrefetch(current, next, currentPage, pageUrl)
    await flushPromises()
    expect(pending.length).toBe(4)

    // Fail two, succeed two — the queue must still fully drain (4 unique URLs,
    // no more, no re-fires of the failed ones).
    failOldest()
    await flushPromises()
    resolveOldest()
    await flushPromises()
    failOldest()
    await flushPromises()
    resolveOldest()
    await flushPromises()

    expect(pending.length).toBe(0)
    expect(new Set(requestedUrls).size).toBe(4)
  })
})

describe('useChapterPrefetch — dedupe', () => {
  it('does not re-request a URL already requested by this instance', async () => {
    const current = ref<PrefetchChapter | null>(chB) // 4 pages
    const next = ref<PrefetchChapter | null>(null)
    const currentPage = ref(0)
    useChapterPrefetch(current, next, currentPage, pageUrl)
    await flushPromises()

    // Drain ch-b fully.
    for (let i = 0; i < 4; i++) {
      resolveOldest()
      await flushPromises()
    }
    const countAfterFirstDrain = requestedUrls.length
    expect(countAfterFirstDrain).toBe(4)

    // Re-entering the SAME chapter (e.g. scrolling back into it) must not
    // re-fire any of its already-warmed page URLs.
    current.value = { id: 'ch-b', pageCount: 4 }
    await flushPromises()

    expect(requestedUrls.length).toBe(countAfterFirstDrain)
    expect(pending.length).toBe(0)
  })
})

describe('useChapterPrefetch — dispose', () => {
  it('stops enqueuing further pages once disposed', async () => {
    const current = ref<PrefetchChapter | null>(chA) // 12 pages
    const next = ref<PrefetchChapter | null>(null)
    const currentPage = ref(0)
    const { dispose } = useChapterPrefetch(current, next, currentPage, pageUrl)
    await flushPromises()
    expect(pending.length).toBe(5)

    dispose()

    // Resolving the in-flight requests must not admit any new ones.
    for (let i = 0; i < 5; i++) {
      resolveOldest()
      await flushPromises()
    }
    expect(requestedUrls.length).toBe(5)
  })
})
