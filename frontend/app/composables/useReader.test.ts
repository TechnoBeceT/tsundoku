/**
 * useReader — the reader's data + windowing layer.
 *
 * Pins:
 *   1. The chapter list is DOWNLOADED-only, number-ascending (null numbers last).
 *   2. The mounted window opens at `startChapterId` (a single chapter), and falls
 *      back to the first chapter when the start id is absent.
 *   3. `onNearTail` appends the next chapter and unmounts far-above chapters so
 *      the window never exceeds MAX_MOUNTED (3).
 *   4. `pageUrl` builds the same-origin page-bytes string.
 *   5. loading flips during the load; a failed load surfaces `error` (§16).
 *
 * vi.mock is hoisted so the apiClient mock is in place before useReader.ts loads.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { useReader } from './useReader'
import { MAX_MOUNTED } from '~/components/reader/ReaderStrip.logic'

let nextOk = true
let nextEmpty = false

// A series that loads successfully but has NO downloaded chapters (all wanted) —
// the reader's empty branch: an empty window, no error (the route's EmptyState).
const emptyDetail = {
  id: 'series-1',
  chapters: [
    { id: 'ch-w1', chapterKey: 'w1', number: 1, name: 'One', state: 'wanted', filename: '', pageCount: null, read: false, lastReadPage: 0 },
    { id: 'ch-w2', chapterKey: 'w2', number: 2, name: 'Two', state: 'failed', filename: '', pageCount: null, read: false, lastReadPage: 0 },
  ],
}

// A series whose chapters exercise the filter (a wanted + a failed chapter that
// must be excluded), the sort (out-of-order numbers + a null-number chapter that
// sorts last), and enough downloaded chapters to drive the window.
const detail = {
  id: 'series-1',
  chapters: [
    { id: 'ch-c', chapterKey: 'k3', number: 3, name: 'Three', state: 'downloaded', filename: 'c.cbz', pageCount: 20, read: false, lastReadPage: 0 },
    { id: 'ch-a', chapterKey: 'k1', number: 1, name: 'One', state: 'downloaded', filename: 'a.cbz', pageCount: 10, read: true, lastReadPage: 9 },
    { id: 'ch-wanted', chapterKey: 'k5', number: 5, name: 'Five', state: 'wanted', filename: '', pageCount: null, read: false, lastReadPage: 0 },
    { id: 'ch-b', chapterKey: 'k2', number: 2, name: 'Two', state: 'downloaded', filename: 'b.cbz', pageCount: 15, read: false, lastReadPage: 3 },
    { id: 'ch-null', chapterKey: 'kx', number: null, name: 'Extra', state: 'downloaded', filename: 'x.cbz', pageCount: 8, read: false, lastReadPage: 0 },
    { id: 'ch-failed', chapterKey: 'k6', number: 6, name: 'Six', state: 'failed', filename: '', pageCount: null, read: false, lastReadPage: 0 },
    { id: 'ch-d', chapterKey: 'k4', number: 4, name: 'Four', state: 'downloaded', filename: 'd.cbz', pageCount: 30, read: false, lastReadPage: 0 },
  ],
}

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string) => {
      if (path === '/api/series/{id}') {
        if (!nextOk) return Promise.resolve({ data: null, error: { message: 'boom' }, response: new Response(null, { status: 500 }) })
        if (nextEmpty) return Promise.resolve({ data: emptyDetail, error: null, response: new Response() })
        return Promise.resolve({ data: detail, error: null, response: new Response() })
      }
      return Promise.resolve({ data: null, error: null, response: new Response() })
    }),
    use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

describe('useReader — chapter list', () => {
  beforeEach(() => { nextOk = true })

  it('keeps only downloaded chapters, sorted number-ascending with nulls last', async () => {
    const { chapters, refresh } = useReader('series-1', 'ch-a')
    await refresh()

    // downloaded ids: a(1) b(2) c(3) d(4) null(last); wanted + failed excluded.
    expect(chapters.value.map((c) => c.id)).toEqual(['ch-a', 'ch-b', 'ch-c', 'ch-d', 'ch-null'])
    expect(chapters.value.map((c) => c.number)).toEqual([1, 2, 3, 4, null])
  })

  it('maps progress + pageCount fields onto each chapter', async () => {
    const { chapters, refresh } = useReader('series-1', 'ch-a')
    await refresh()
    const one = chapters.value.find((c) => c.id === 'ch-a')!
    expect(one).toMatchObject({ pageCount: 10, read: true, lastReadPage: 9, name: 'One' })
  })
})

describe('useReader — mounted window', () => {
  beforeEach(() => { nextOk = true })

  it('opens the window at startChapterId (a single chapter)', async () => {
    const { mountedChapters, refresh } = useReader('series-1', 'ch-c')
    await refresh()
    expect(mountedChapters.value.map((c) => c.id)).toEqual(['ch-c'])
  })

  it('falls back to the first chapter when startChapterId is absent', async () => {
    const { mountedChapters, refresh } = useReader('series-1', 'does-not-exist')
    await refresh()
    expect(mountedChapters.value.map((c) => c.id)).toEqual(['ch-a'])
  })

  it('onNearTail appends the next chapter', async () => {
    const { mountedChapters, onNearTail, refresh } = useReader('series-1', 'ch-a')
    await refresh()
    onNearTail()
    expect(mountedChapters.value.map((c) => c.id)).toEqual(['ch-a', 'ch-b'])
  })

  it('onNearTail keeps the window bounded to MAX_MOUNTED, dropping far-above chapters', async () => {
    const { mountedChapters, onNearTail, refresh } = useReader('series-1', 'ch-a')
    await refresh()
    onNearTail() // a,b
    onNearTail() // a,b,c  (== MAX_MOUNTED)
    onNearTail() // append d, drop a -> b,c,d
    expect(mountedChapters.value.length).toBe(MAX_MOUNTED)
    expect(mountedChapters.value.map((c) => c.id)).toEqual(['ch-b', 'ch-c', 'ch-d'])
  })

  it('onNearTail is a no-op once the last chapter is mounted', async () => {
    const { mountedChapters, onNearTail, refresh } = useReader('series-1', 'ch-null')
    await refresh()
    onNearTail()
    onNearTail()
    expect(mountedChapters.value.map((c) => c.id)).toEqual(['ch-null'])
  })
})

describe('useReader — pageUrl + states', () => {
  beforeEach(() => { nextOk = true; nextEmpty = false })

  it('builds the same-origin page-bytes URL', async () => {
    const { pageUrl, refresh } = useReader('series-1', 'ch-a')
    await refresh()
    expect(pageUrl('ch-a', 0)).toBe('/api/series/series-1/chapters/ch-a/pages/0')
    expect(pageUrl('ch-b', 7)).toBe('/api/series/series-1/chapters/ch-b/pages/7')
  })

  it('surfaces an error and leaves the list empty when the load fails', async () => {
    nextOk = false
    const { chapters, mountedChapters, error, refresh } = useReader('series-1', 'ch-a')
    await refresh()
    expect(error.value).toBeTruthy()
    expect(chapters.value).toEqual([])
    expect(mountedChapters.value).toEqual([])
  })

  it('a successful load with zero downloaded chapters is empty + error-free (the EmptyState branch)', async () => {
    nextEmpty = true
    const { chapters, mountedChapters, error, loading, refresh } = useReader('series-1', 'ch-a')
    await refresh()
    expect(error.value).toBeNull()
    expect(loading.value).toBe(false)
    expect(chapters.value).toEqual([])
    expect(mountedChapters.value).toEqual([])
  })
})

/**
 * Slice 4: bidirectional window + current chapter + prev/next + jump.
 *
 * Reuses the `detail` fixture above (5 downloaded chapters, sorted
 * ch-a(1) ch-b(2) ch-c(3) ch-d(4) ch-null(last)) — plenty of room for a full
 * MAX_MOUNTED (3) window plus a prepend/append on either side.
 */
describe('useReader — prev/next neighbours', () => {
  beforeEach(() => { nextOk = true; nextEmpty = false })

  it('exposes prev/next neighbours in number order', async () => {
    const { refresh, setCurrentChapter, prevChapter, nextChapter } = useReader('series-1', 'ch-a')
    await refresh()

    setCurrentChapter('ch-b')

    expect(prevChapter.value?.id).toBe('ch-a')
    expect(nextChapter.value?.id).toBe('ch-c')
  })

  it('has no prev at the first chapter and no next at the last', async () => {
    const { refresh, setCurrentChapter, hasPrev, hasNext } = useReader('series-1', 'ch-a')
    await refresh()

    setCurrentChapter('ch-a')
    expect(hasPrev.value).toBe(false)
    expect(hasNext.value).toBe(true)

    setCurrentChapter('ch-null')
    expect(hasPrev.value).toBe(true)
    expect(hasNext.value).toBe(false)
  })
})

describe('useReader — bidirectional window (onNearHead)', () => {
  beforeEach(() => { nextOk = true; nextEmpty = false })

  it('onNearHead prepends the previous chapter', async () => {
    const { refresh, mountedChapters, onNearHead } = useReader('series-1', 'ch-c')
    await refresh()
    expect(mountedChapters.value.map((c) => c.id)).toEqual(['ch-c'])

    onNearHead()

    expect(mountedChapters.value.map((c) => c.id)).toEqual(['ch-b', 'ch-c'])
  })

  it('onNearHead is a no-op at the head of the list', async () => {
    const { refresh, mountedChapters, onNearHead } = useReader('series-1', 'ch-a')
    await refresh()

    onNearHead()

    expect(mountedChapters.value.map((c) => c.id)).toEqual(['ch-a'])
  })

  it('onNearHead never unmounts the chapter it just prepended', async () => {
    const { refresh, mountedChapters, onNearHead, onNearTail } = useReader('series-1', 'ch-c')
    await refresh()

    // Grow the window forward to a full MAX_MOUNTED (3) window: c, d, null.
    onNearTail()
    onNearTail()
    expect(mountedChapters.value.map((c) => c.id)).toEqual(['ch-c', 'ch-d', 'ch-null'])

    // Prepend b: the window would be 4-wide, so one chapter must drop — the
    // BOTTOM (ch-null), never the just-prepended ch-b.
    onNearHead()

    const ids = mountedChapters.value.map((c) => c.id)
    expect(ids).toEqual(['ch-b', 'ch-c', 'ch-d'])
    expect(ids).toContain('ch-b')
    expect(ids).not.toContain('ch-null')
  })

  it('onNearTail still unmounts from the top (regression guard on the forward path)', async () => {
    const { refresh, mountedChapters, onNearTail } = useReader('series-1', 'ch-a')
    await refresh()

    onNearTail() // a,b
    onNearTail() // a,b,c
    onNearTail() // append d (4-wide) -> drop the top (a) -> b,c,d

    const ids = mountedChapters.value.map((c) => c.id)
    expect(ids).toEqual(['ch-b', 'ch-c', 'ch-d'])
    expect(ids).not.toContain('ch-a')
  })
})

describe('useReader — jumpToChapter', () => {
  beforeEach(() => { nextOk = true; nextEmpty = false })

  it('jumpToChapter reseeds the window and emits a scroll request with a fresh token', async () => {
    const { refresh, mountedChapters, currentChapterId, scrollRequest, jumpToChapter } = useReader('series-1', 'ch-a')
    await refresh()

    jumpToChapter('ch-c')

    expect(mountedChapters.value.map((c) => c.id)).toEqual(['ch-c'])
    expect(currentChapterId.value).toBe('ch-c')
    expect(scrollRequest.value).toMatchObject({ chapterId: 'ch-c', page: 0 })
    const firstToken = scrollRequest.value!.token

    jumpToChapter('ch-a')

    expect(mountedChapters.value.map((c) => c.id)).toEqual(['ch-a'])
    expect(currentChapterId.value).toBe('ch-a')
    expect(scrollRequest.value!.token).toBeGreaterThan(firstToken)
  })

  it('jumpToChapter is a no-op for an unknown chapter id', async () => {
    const { refresh, mountedChapters, jumpToChapter } = useReader('series-1', 'ch-a')
    await refresh()

    jumpToChapter('does-not-exist')

    expect(mountedChapters.value.map((c) => c.id)).toEqual(['ch-a'])
  })
})

/**
 * Fix 4: the route used to hardcode `token: 1` for its resume-anchor scroll
 * request, which collided with `jumpToChapter`'s own counter — both started
 * counting from 1 — silently swallowing whichever request the strip saw
 * second. `requestScroll` is now the ONE place a `scrollRequest` is minted;
 * both the route's resume scroll and `jumpToChapter` must go through it and
 * therefore share one strictly-increasing token space.
 */
describe('useReader — requestScroll: ONE token space shared by the resume scroll and every jump (Fix 4)', () => {
  beforeEach(() => { nextOk = true; nextEmpty = false })

  it('requestScroll publishes a fresh, monotonically increasing token on each call', async () => {
    const { refresh, scrollRequest, requestScroll } = useReader('series-1', 'ch-a')
    await refresh()

    requestScroll('ch-a', 5)
    expect(scrollRequest.value).toMatchObject({ chapterId: 'ch-a', page: 5 })
    const first = scrollRequest.value!.token

    requestScroll('ch-b', 0)
    expect(scrollRequest.value).toMatchObject({ chapterId: 'ch-b', page: 0 })
    const second = scrollRequest.value!.token

    expect(second).toBeGreaterThan(first)
  })

  it('a resume scroll (via requestScroll) followed by a jump carries strictly increasing, distinct tokens', async () => {
    const { refresh, scrollRequest, requestScroll, jumpToChapter, mountedChapters } = useReader('series-1', 'ch-a')
    await refresh()

    // Simulates the route's resume-anchor scroll — it must go through
    // `requestScroll`, never construct a `{ token: 1 }` literal itself.
    requestScroll('ch-a', 9)
    const resumeToken = scrollRequest.value!.token

    jumpToChapter('ch-c')
    const jumpToken = scrollRequest.value!.token

    expect(jumpToken).toBeGreaterThan(resumeToken)
    expect(jumpToken).not.toBe(resumeToken)
    // The strip acts on both: the resume request is honoured (asserted above
    // via its own distinct token), and the jump takes effect too — the window
    // actually moved to the jumped-to chapter.
    expect(mountedChapters.value.map((c) => c.id)).toEqual(['ch-c'])
  })

  it('two independent useReader instances each start their own token space at 1 (instance-scoped, not module-scoped)', async () => {
    const readerA = useReader('series-1', 'ch-a')
    const readerB = useReader('series-1', 'ch-a')
    await readerA.refresh()
    await readerB.refresh()

    readerA.requestScroll('ch-a', 0)
    readerB.requestScroll('ch-a', 0)

    // Both instances mint token 1 for their own first request — proving the
    // counter is private to each `useReader()` call, not a shared module-level
    // counter that would keep climbing across instances.
    expect(readerA.scrollRequest.value!.token).toBe(1)
    expect(readerB.scrollRequest.value!.token).toBe(1)
  })
})
