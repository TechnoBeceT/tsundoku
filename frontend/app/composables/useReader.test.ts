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
