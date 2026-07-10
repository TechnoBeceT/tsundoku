/**
 * useReadingProgress — persists the reader's scroll position + read state.
 *
 * The long-strip reader (ReaderStrip) emits `centered` as the owner scrolls and
 * `chapter-finished` when a chapter's end-divider passes the viewport top. This
 * composable turns those into progress writes against
 * `PATCH /api/chapters/{id}/progress`:
 *   - `record(chapterId, page)` — a DEBOUNCED (~1s trailing) position write
 *     ({ lastReadPage, read:false }). It DEDUPES identical positions (the
 *     `centered` event re-emits the same payload every throttle tick) and CLAMPS
 *     the page to `[0, pageCount-1]` so a declared-but-missing trailing page can
 *     never be persisted as the last-read page.
 *   - `markRead(chapterId, pageCount)` — an IMMEDIATE write ({ read:true,
 *     lastReadPage: pageCount-1 }) that also cancels any pending `record` for
 *     that chapter, so a trailing position write can't un-set `read`.
 *   - `resumeTarget(chapters)` — the (chapterId, page) the reader should open at.
 *   - `flush()` — sends any pending debounced write immediately (the route calls
 *     it on leave so the last position is never lost).
 *
 * BEST-EFFORT (the sanctioned §16 exception): progress writes must NEVER block
 * reading or surface a page-level error. A failed PATCH is swallowed — the
 * position simply isn't marked recorded, so the next debounce re-sends it. This
 * is the one place we deliberately do NOT surface an error state, because the
 * write is an invisible background side effect of scrolling, not a user-driven
 * action awaiting a result.
 */
import type { Ref } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { ReaderChapter } from '~/composables/useReader'

/** Trailing-debounce window for position writes (ms). */
const RECORD_DEBOUNCE_MS = 1000

/** A resume anchor: the chapter UUID + 0-based page to open the reader at. */
export interface ResumeTarget {
  /** Chapter UUID to scroll to (empty string when there are no chapters). */
  chapterId: string
  /** 0-based page within that chapter to land on. */
  page: number
}

/**
 * clampPage — floors a page at 0 and, WHEN a real `pageCount` is known (>0),
 * caps it at the chapter's last page. An unknown pageCount (0 = not downloaded /
 * undeclared) can't be clamped from above, so only the floor applies.
 */
function clampPage(pageCount: number, page: number): number {
  const floored = Math.max(0, page)
  return pageCount > 0 ? Math.min(floored, pageCount - 1) : floored
}

/**
 * useReadingProgress — see the file header. `chapters` is the reader's live
 * chapter list (used to clamp a recorded page to the chapter's real length);
 * `startChapterId` is the deep-linked chapter the owner explicitly opened at
 * (preferred by `resumeTarget`).
 */
export function useReadingProgress(chapters: Ref<ReaderChapter[]>, startChapterId: string) {
  // The last position we SUCCESSFULLY recorded per chapter (chapterId → page) —
  // the dedupe key so the re-emitted `centered` payloads don't spam writes.
  const lastRecorded = new Map<string, number>()

  // The single in-flight trailing debounce. The reader only scrolls one position
  // at a time, so one pending write across all chapters is sufficient.
  let pendingTimer: ReturnType<typeof setTimeout> | null = null
  let pendingChapterId: string | null = null
  let pendingPage = 0

  /** Looks up a chapter's declared pageCount (0 when unknown/absent). */
  function pageCountOf(chapterId: string): number {
    return chapters.value.find((c) => c.id === chapterId)?.pageCount ?? 0
  }

  /** Clears the pending debounce timer + target (no write). */
  function clearPending(): void {
    if (pendingTimer) {
      clearTimeout(pendingTimer)
      pendingTimer = null
    }
    pendingChapterId = null
  }

  /**
   * Sends one progress PATCH. Best-effort: on any error the position is left
   * un-recorded (so a later debounce retries) and nothing throws or surfaces.
   * Marks the position recorded (for dedupe) only on a clean success.
   */
  async function sendProgress(chapterId: string, page: number, read: boolean): Promise<void> {
    try {
      const res = await apiClient.PATCH('/api/chapters/{id}/progress', {
        params: { path: { id: chapterId } },
        body: { lastReadPage: page, read },
      })
      if (res.error) return
      lastRecorded.set(chapterId, page)
    }
    catch {
      // Best-effort — swallow; the next debounced record re-sends this position.
    }
  }

  /** Fires the pending debounced write (the trailing edge). */
  function firePending(): void {
    pendingTimer = null
    const chapterId = pendingChapterId
    const page = pendingPage
    pendingChapterId = null
    if (chapterId) void sendProgress(chapterId, page, false)
  }

  /**
   * record — schedules a debounced position write. Dedupes against both the
   * last SUCCESSFULLY-recorded position and the currently PENDING one, so a
   * stream of identical `centered` re-emits neither re-writes nor endlessly
   * resets the timer. A new position (re)arms the ~1s trailing debounce.
   */
  function record(chapterId: string, page: number): void {
    const clamped = clampPage(pageCountOf(chapterId), page)
    if (lastRecorded.get(chapterId) === clamped) return
    if (pendingTimer && pendingChapterId === chapterId && pendingPage === clamped) return
    pendingChapterId = chapterId
    pendingPage = clamped
    if (pendingTimer) clearTimeout(pendingTimer)
    pendingTimer = setTimeout(firePending, RECORD_DEBOUNCE_MS)
  }

  /**
   * markRead — immediately marks a chapter fully read at its last page. Cancels
   * any pending debounced `record` for the SAME chapter first, and optimistically
   * seeds `lastRecorded` so a follow-up identical-position `record` can't fire a
   * `read:false` write that would un-set it.
   */
  function markRead(chapterId: string, pageCount: number): void {
    if (pendingChapterId === chapterId) clearPending()
    const lastPage = Math.max(0, pageCount - 1)
    lastRecorded.set(chapterId, lastPage)
    void sendProgress(chapterId, lastPage, true)
  }

  /**
   * resumeTarget — the (chapterId, page) the reader should open at. Prefers the
   * explicitly-opened `startChapterId` when it's in the list (resume mid-chapter
   * at its lastReadPage); otherwise the furthest-along chapter showing any
   * progress; otherwise the first chapter at page 0. The page is clamped to the
   * chosen chapter's real length.
   */
  function resumeTarget(list: ReaderChapter[]): ResumeTarget {
    if (list.length === 0) return { chapterId: '', page: 0 }
    const started = list.find((c) => c.id === startChapterId)
    if (started) return { chapterId: started.id, page: clampPage(started.pageCount, started.lastReadPage) }
    // No explicit start in the list — the last chapter with any recorded progress.
    let progressed: ReaderChapter | null = null
    for (const c of list) {
      if (c.read || c.lastReadPage > 0) progressed = c
    }
    const pick = progressed ?? list[0]!
    return { chapterId: pick.id, page: clampPage(pick.pageCount, pick.lastReadPage) }
  }

  /**
   * flush — sends any pending debounced write immediately (the trailing edge,
   * now). The reader route calls this on unmount / route-leave so the last
   * scrolled position isn't lost when navigating away.
   */
  function flush(): void {
    if (!pendingTimer || !pendingChapterId) return
    clearTimeout(pendingTimer)
    const chapterId = pendingChapterId
    const page = pendingPage
    clearPending()
    void sendProgress(chapterId, page, false)
  }

  return { record, markRead, resumeTarget, flush }
}
