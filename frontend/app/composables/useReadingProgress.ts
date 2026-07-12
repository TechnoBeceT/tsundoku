/**
 * useReadingProgress — persists the reader's scroll position + read state.
 *
 * The long-strip reader (ReaderStrip) emits `centered` as the owner scrolls and
 * `chapter-finished` when a chapter's end-divider passes the viewport top. This
 * composable turns those into progress writes against
 * `PATCH /api/chapters/{id}/progress`:
 *   - `record(chapterId, page)` — a DEBOUNCED (~1s trailing) position write
 *     ({ lastReadPage, read }). It DEDUPES identical positions (the `centered`
 *     event re-emits the same payload every throttle tick) and CLAMPS the page
 *     to `[0, pageCount-1]` so a declared-but-missing trailing page can never be
 *     persisted as the last-read page. **`read` is NEVER hardcoded `false`** —
 *     it reflects whatever this chapter's CURRENT known read state is (see
 *     `isRead` below). The bidirectional strip (this branch) lets the reader
 *     scroll BACKWARD into an already-finished chapter to re-read a panel; a
 *     `centered` position write fired while dwelling there must not silently
 *     un-read the chapter it just finished — that was the CRITICAL bug this
 *     guards (before bidirectional scrolling, `record` could only ever touch
 *     the chapter being actively read, so a bare `false` was harmless; it no
 *     longer is).
 *   - `markRead(chapterId, pageCount)` — an IMMEDIATE write ({ read:true,
 *     lastReadPage: pageCount-1 }) that also cancels any pending `record` for
 *     that chapter, so a trailing position write can't un-set `read`. Also
 *     records the chapter in the local `readThisSession` set so any LATER
 *     `record` on it (from re-entering it while scrolling elsewhere) keeps
 *     sending `read:true`.
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

  // Chapter ids marked read THIS SESSION via `markRead`. `chapters.value` is a
  // point-in-time snapshot loaded once by `useReader` — it is never mutated
  // locally when `markRead` fires, so a chapter finished during this visit
  // would otherwise look unread to `isRead` until the next full reload. This
  // set is the local source of truth for "read" on top of that snapshot.
  const readThisSession = new Set<string>()

  // The single in-flight trailing debounce. The reader only scrolls one position
  // at a time, so one pending write across all chapters is sufficient.
  let pendingTimer: ReturnType<typeof setTimeout> | null = null
  let pendingChapterId: string | null = null
  let pendingPage = 0

  /** Looks up a chapter's declared pageCount (0 when unknown/absent). */
  function pageCountOf(chapterId: string): number {
    return chapters.value.find((c) => c.id === chapterId)?.pageCount ?? 0
  }

  /**
   * isRead — whether a chapter is currently known to be read: either already
   * `read:true` in the loaded chapter snapshot, or marked read this session via
   * `markRead`. `record()` uses this to fill the PATCH's `read` field so a
   * scroll-driven position write can never downgrade a finished chapter back to
   * unread (FIX 1 — see the file header).
   */
  function isRead(chapterId: string): boolean {
    if (readThisSession.has(chapterId)) return true
    return chapters.value.find((c) => c.id === chapterId)?.read === true
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
    if (chapterId) void sendProgress(chapterId, page, isRead(chapterId))
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
   * any pending debounced `record` for the SAME chapter first, adds the chapter
   * to `readThisSession` (so a LATER `record` on it — e.g. re-entering it by
   * scrolling backward — keeps sending `read:true`, not a bare `false`), and
   * optimistically seeds `lastRecorded` so a follow-up identical-position
   * `record` doesn't re-send a redundant write.
   */
  function markRead(chapterId: string, pageCount: number): void {
    if (pendingChapterId === chapterId) clearPending()
    readThisSession.add(chapterId)
    const lastPage = Math.max(0, pageCount - 1)
    lastRecorded.set(chapterId, lastPage)
    void sendProgress(chapterId, lastPage, true)
  }

  /**
   * resumeTarget — the (chapterId, page) the reader should open at. Prefers the
   * explicitly-opened `startChapterId` when it's in the list (resume mid-chapter
   * at its lastReadPage — a direct chapter-click must always open THAT chapter).
   * Otherwise: the FIRST chapter (number-ascending, as `list` is always ordered)
   * that is NOT read, at its saved `lastReadPage` (0 if never opened) — a
   * partially-read chapter is not read, so it's correctly picked at its saved
   * page. If EVERY chapter is read, falls back to the LAST chapter at page 0
   * (nothing left to continue — land at the most recent chapter's start rather
   * than reopening something already finished). The page is always clamped to
   * the chosen chapter's real length.
   */
  function resumeTarget(list: ReaderChapter[]): ResumeTarget {
    if (list.length === 0) return { chapterId: '', page: 0 }
    const started = list.find((c) => c.id === startChapterId)
    if (started) return { chapterId: started.id, page: clampPage(started.pageCount, started.lastReadPage) }
    const unread = list.find((c) => !c.read)
    if (unread) return { chapterId: unread.id, page: clampPage(unread.pageCount, unread.lastReadPage) }
    const last = list[list.length - 1]!
    return { chapterId: last.id, page: 0 }
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
    void sendProgress(chapterId, page, isRead(chapterId))
  }

  return { record, markRead, resumeTarget, flush }
}
