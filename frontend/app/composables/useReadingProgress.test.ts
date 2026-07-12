/**
 * useReadingProgress — the reader's progress-persistence layer.
 *
 * Pins (fake timers throughout, since the position write is debounced):
 *   1. record DEBOUNCES: a burst of positions collapses to one trailing write.
 *   2. record DEDUPES: an identical position (post-success) writes nothing.
 *   3. record CLAMPS the page to [0, pageCount-1] before sending.
 *   4. markRead is IMMEDIATE (read:true, last page) and cancels a pending record.
 *   5. resumeTarget prefers startChapterId, else the first UNREAD chapter at its
 *      saved page, else (all read) the last chapter at page 0, always clamped.
 *   6. flush sends the pending write right away (route-leave safety).
 *   7. a rejected PATCH is best-effort — never throws, retried next debounce.
 *   8. FIX 1 — record NEVER hardcodes read:false: it preserves whatever the
 *      chapter's current known read state is, so scrolling backward into an
 *      already-finished chapter (bidirectional strip) can never silently
 *      un-read it.
 *
 * vi.mock is hoisted so the apiClient mock is in place before the composable loads.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { ref } from 'vue'
import { useReadingProgress } from './useReadingProgress'
import type { ReaderChapter } from './useReader'

const patch = vi.fn()

vi.mock('~/utils/api/client', () => ({
  apiClient: { PATCH: (...args: unknown[]): unknown => patch(...args) },
  setUnauthorizedHandler: vi.fn(),
}))

function chapter(over: Partial<ReaderChapter> & { id: string }): ReaderChapter {
  return { number: 1, name: '', pageCount: 10, read: false, lastReadPage: 0, ...over }
}

const chapters = ref<ReaderChapter[]>([
  chapter({ id: 'ch-a', number: 1, pageCount: 10, read: true, lastReadPage: 9 }),
  chapter({ id: 'ch-b', number: 2, pageCount: 15, read: false, lastReadPage: 3 }),
  chapter({ id: 'ch-c', number: 3, pageCount: 20, read: false, lastReadPage: 0 }),
])

/** The (path, body) of the Nth PATCH call, for concise assertions. */
function callBody(n = 0): { path: string, id: string, body: { lastReadPage: number, read: boolean } } {
  const [path, opts] = patch.mock.calls[n] as [string, { params: { path: { id: string } }, body: { lastReadPage: number, read: boolean } }]
  return { path, id: opts.params.path.id, body: opts.body }
}

beforeEach(() => {
  vi.useFakeTimers()
  patch.mockReset()
  patch.mockResolvedValue({ data: {}, error: null })
})

describe('useReadingProgress — record (debounce + dedupe + clamp)', () => {
  it('coalesces a burst of positions into a single trailing write', async () => {
    const { record } = useReadingProgress(chapters, 'ch-a')
    record('ch-b', 0)
    record('ch-b', 1)
    record('ch-b', 2)
    expect(patch).not.toHaveBeenCalled() // nothing before the debounce elapses
    await vi.advanceTimersByTimeAsync(1000)
    expect(patch).toHaveBeenCalledTimes(1)
    expect(callBody()).toMatchObject({ path: '/api/chapters/{id}/progress', id: 'ch-b', body: { lastReadPage: 2, read: false } })
  })

  it('skips an identical position that was already recorded', async () => {
    const { record } = useReadingProgress(chapters, 'ch-a')
    record('ch-b', 5)
    await vi.advanceTimersByTimeAsync(1000)
    expect(patch).toHaveBeenCalledTimes(1)
    // Same position again — deduped, no second write.
    record('ch-b', 5)
    await vi.advanceTimersByTimeAsync(1000)
    expect(patch).toHaveBeenCalledTimes(1)
  })

  it('does not endlessly reset the timer for a repeated identical pending position', async () => {
    const { record } = useReadingProgress(chapters, 'ch-a')
    record('ch-b', 4)
    await vi.advanceTimersByTimeAsync(600)
    record('ch-b', 4) // same pending position — must NOT push the deadline out
    await vi.advanceTimersByTimeAsync(400)
    expect(patch).toHaveBeenCalledTimes(1) // fired at the original 1000ms mark
  })

  it('clamps the page to [0, pageCount-1] before sending', async () => {
    const { record } = useReadingProgress(chapters, 'ch-a')
    record('ch-a', 99) // ch-a has pageCount 10 → last page is 9
    await vi.advanceTimersByTimeAsync(1000)
    expect(callBody().body.lastReadPage).toBe(9)
  })

  it('floors a negative page at 0', async () => {
    const { record } = useReadingProgress(chapters, 'ch-a')
    record('ch-b', -5)
    await vi.advanceTimersByTimeAsync(1000)
    expect(callBody().body.lastReadPage).toBe(0)
  })
})

// FIX 1 (CRITICAL): the bidirectional strip lets the reader scroll BACKWARD
// into a chapter it already finished. Before bidirectional scrolling this was
// impossible, so `record` hardcoding `read:false` was harmless — it could only
// ever touch the chapter actively being read. Now re-entering a finished
// chapter and dwelling >1s fires `record`, and a bare `false` would silently
// un-read it (clears readAt, un-dims the row, increments the unread badge,
// moves the resume target backward). `record` must always send the chapter's
// CURRENT known read state instead.
describe('useReadingProgress — record preserves read state (FIX 1)', () => {
  it('sends read:true when recording a position on a chapter already read at load (position updates, read flag preserved)', async () => {
    // ch-a is read:true in the shared fixture — a plain scroll through it must
    // not downgrade it back to unread.
    const { record } = useReadingProgress(chapters, 'not-here')
    record('ch-a', 4)
    await vi.advanceTimersByTimeAsync(1000)
    expect(callBody()).toMatchObject({ id: 'ch-a', body: { lastReadPage: 4, read: true } })
  })

  it('still sends read:false when recording a position on a chapter that is not read', async () => {
    const { record } = useReadingProgress(chapters, 'not-here')
    record('ch-b', 4) // ch-b is read:false in the fixture
    await vi.advanceTimersByTimeAsync(1000)
    expect(callBody()).toMatchObject({ id: 'ch-b', body: { lastReadPage: 4, read: false } })
  })

  it('the full repro: markRead a chapter, then record a mid-chapter position on it — the PATCH must NOT set read:false', async () => {
    const list = ref<ReaderChapter[]>([
      chapter({ id: 'ch-5', number: 5, pageCount: 20, read: false, lastReadPage: 0 }),
      chapter({ id: 'ch-6', number: 6, pageCount: 20, read: false, lastReadPage: 0 }),
    ])
    const { record, markRead } = useReadingProgress(list, 'not-here')

    // Read ch.5 to the end.
    markRead('ch-5', 20)
    expect(callBody(0)).toMatchObject({ id: 'ch-5', body: { read: true, lastReadPage: 19 } })

    // Scroll on into ch.6, then BACK UP into ch.5 to re-read a panel and dwell.
    record('ch-6', 2)
    record('ch-5', 12)
    await vi.advanceTimersByTimeAsync(1000)

    // Only the ch-5 re-entry write should have fired after markRead (ch-6's
    // record was superseded by ch-5's — one pending slot); it must carry
    // read:true, never read:false.
    const last = callBody(patch.mock.calls.length - 1)
    expect(last).toMatchObject({ id: 'ch-5', body: { lastReadPage: 12, read: true } })
    expect(last.body.read).not.toBe(false)
  })
})

describe('useReadingProgress — markRead', () => {
  it('writes read:true at the last page immediately (no debounce)', () => {
    const { markRead } = useReadingProgress(chapters, 'ch-a')
    markRead('ch-b', 15)
    expect(patch).toHaveBeenCalledTimes(1)
    expect(callBody()).toMatchObject({ id: 'ch-b', body: { lastReadPage: 14, read: true } })
  })

  it('cancels a pending record for the same chapter so it cannot un-set read', async () => {
    const { record, markRead } = useReadingProgress(chapters, 'ch-a')
    record('ch-b', 7) // schedules a read:false write
    markRead('ch-b', 15) // immediate read:true, cancels the pending record
    await vi.advanceTimersByTimeAsync(1000)
    expect(patch).toHaveBeenCalledTimes(1) // only the markRead write
    expect(callBody().body.read).toBe(true)
  })

  it('leaves a pending record for a DIFFERENT chapter intact', async () => {
    const { record, markRead } = useReadingProgress(chapters, 'ch-a')
    record('ch-c', 5)
    markRead('ch-b', 15)
    await vi.advanceTimersByTimeAsync(1000)
    expect(patch).toHaveBeenCalledTimes(2)
    expect(callBody(0)).toMatchObject({ id: 'ch-b', body: { read: true } })
    expect(callBody(1)).toMatchObject({ id: 'ch-c', body: { lastReadPage: 5, read: false } })
  })
})

describe('useReadingProgress — resumeTarget', () => {
  it('prefers startChapterId at its clamped lastReadPage', () => {
    const { resumeTarget } = useReadingProgress(chapters, 'ch-b')
    expect(resumeTarget(chapters.value)).toEqual({ chapterId: 'ch-b', page: 3 })
  })

  // FIX 2: resumeTarget's fallback (no explicit start in the list) used to pick
  // the LAST chapter showing any progress — which includes a chapter markRead
  // just finished (read:true IS progress), so the FAB kept reopening the
  // chapter the reader just completed instead of advancing to the next one.
  it('targets the first UNREAD chapter, not the last chapter with any progress', () => {
    // ch.1-10 read, ch.11 never touched — must land on ch.11 page 0, never
    // ch.10 (the just-finished chapter, which also "has progress").
    const list = ref<ReaderChapter[]>([
      ...Array.from({ length: 10 }, (_, i) =>
        chapter({ id: `ch-${i + 1}`, number: i + 1, pageCount: 20, read: true, lastReadPage: 19 })),
      chapter({ id: 'ch-11', number: 11, pageCount: 20, read: false, lastReadPage: 0 }),
    ])
    const { resumeTarget } = useReadingProgress(list, 'not-here')
    expect(resumeTarget(list.value)).toEqual({ chapterId: 'ch-11', page: 0 })
  })

  it('resumes a partially-read chapter at its saved page even with earlier read chapters', () => {
    const list = ref<ReaderChapter[]>([
      chapter({ id: 'ch-1', number: 1, pageCount: 10, read: true, lastReadPage: 9 }),
      chapter({ id: 'ch-2', number: 2, pageCount: 10, read: true, lastReadPage: 9 }),
      chapter({ id: 'ch-3', number: 3, pageCount: 10, read: true, lastReadPage: 9 }),
      chapter({ id: 'ch-4', number: 4, pageCount: 10, read: false, lastReadPage: 5 }),
      chapter({ id: 'ch-5', number: 5, pageCount: 10, read: false, lastReadPage: 0 }),
    ])
    const { resumeTarget } = useReadingProgress(list, 'not-here')
    expect(resumeTarget(list.value)).toEqual({ chapterId: 'ch-4', page: 5 })
  })

  it('falls back to the last chapter at page 0 when every chapter is read', () => {
    const list = ref<ReaderChapter[]>([
      chapter({ id: 'ch-1', number: 1, pageCount: 10, read: true, lastReadPage: 9 }),
      chapter({ id: 'ch-2', number: 2, pageCount: 10, read: true, lastReadPage: 9 }),
    ])
    const { resumeTarget } = useReadingProgress(list, 'not-here')
    expect(resumeTarget(list.value)).toEqual({ chapterId: 'ch-2', page: 0 })
  })

  it('falls back to the first chapter at page 0 when nothing has progress', () => {
    const fresh = ref<ReaderChapter[]>([
      chapter({ id: 'ch-x', number: 1, pageCount: 5 }),
      chapter({ id: 'ch-y', number: 2, pageCount: 5 }),
    ])
    const { resumeTarget } = useReadingProgress(fresh, 'not-here')
    expect(resumeTarget(fresh.value)).toEqual({ chapterId: 'ch-x', page: 0 })
  })

  it('returns an empty target for an empty list', () => {
    const { resumeTarget } = useReadingProgress(ref([]), 'ch-a')
    expect(resumeTarget([])).toEqual({ chapterId: '', page: 0 })
  })

  it('clamps a stored lastReadPage that exceeds the chapter length', () => {
    const over = ref<ReaderChapter[]>([chapter({ id: 'ch-o', number: 1, pageCount: 4, lastReadPage: 99 })])
    const { resumeTarget } = useReadingProgress(over, 'ch-o')
    expect(resumeTarget(over.value)).toEqual({ chapterId: 'ch-o', page: 3 })
  })
})

describe('useReadingProgress — flush', () => {
  it('sends the pending write immediately', () => {
    const { record, flush } = useReadingProgress(chapters, 'ch-a')
    record('ch-b', 6)
    expect(patch).not.toHaveBeenCalled()
    flush()
    expect(patch).toHaveBeenCalledTimes(1)
    expect(callBody()).toMatchObject({ id: 'ch-b', body: { lastReadPage: 6, read: false } })
  })

  it('is a no-op when there is nothing pending', () => {
    const { flush } = useReadingProgress(chapters, 'ch-a')
    flush()
    expect(patch).not.toHaveBeenCalled()
  })

  it('does not double-send after the debounce already fired', async () => {
    const { record, flush } = useReadingProgress(chapters, 'ch-a')
    record('ch-b', 6)
    await vi.advanceTimersByTimeAsync(1000)
    expect(patch).toHaveBeenCalledTimes(1)
    flush() // nothing pending anymore
    expect(patch).toHaveBeenCalledTimes(1)
  })
})

describe('useReadingProgress — best-effort', () => {
  it('swallows a rejected PATCH and re-sends the position on the next debounce', async () => {
    patch.mockRejectedValueOnce(new Error('network'))
    const { record } = useReadingProgress(chapters, 'ch-a')
    record('ch-b', 8)
    await expect(vi.advanceTimersByTimeAsync(1000)).resolves.not.toThrow()
    expect(patch).toHaveBeenCalledTimes(1)
    // The failed position was never marked recorded → a fresh record re-sends it.
    patch.mockResolvedValue({ data: {}, error: null })
    record('ch-b', 8)
    await vi.advanceTimersByTimeAsync(1000)
    expect(patch).toHaveBeenCalledTimes(2)
  })

  it('swallows an error-result PATCH without throwing', async () => {
    patch.mockResolvedValueOnce({ data: null, error: { message: 'boom' } })
    const { record } = useReadingProgress(chapters, 'ch-a')
    record('ch-b', 2)
    await expect(vi.advanceTimersByTimeAsync(1000)).resolves.not.toThrow()
    expect(patch).toHaveBeenCalledTimes(1)
  })
})
