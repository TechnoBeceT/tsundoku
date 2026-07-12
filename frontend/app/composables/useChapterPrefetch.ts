/**
 * useChapterPrefetch — Mihon/Komikku-style whole-chapter prefetch.
 *
 * PROBLEM (measured, not theorised): every reader page request costs a flat
 * ~135ms server-side (the backend re-opens the CBZ per page over NFS) —
 * independent of bandwidth. `ReaderPage`'s `PRELOAD_RADIUS` only ever runs a
 * few pages ahead of the reader's eyes, so a fast scroll outruns it and the
 * reader visibly waits on that 135ms tax.
 *
 * FIX: the owner's ask — open a chapter, fetch ALL its pages in the
 * background, then prefetch the NEXT chapter. The tax doesn't disappear, it
 * just happens ahead of the reader while they're still reading, so they never
 * see it. This is ONLY worth doing because the companion backend change makes
 * a `?v=<pageVersion>`-matched request cache for a full day (`private,
 * max-age=86400`) instead of the old 5-minute window — without that, a
 * prefetched 165-page chapter would expire mid-read and every page would be
 * re-fetched from scratch anyway.
 *
 * 🔴 It does NOT depend on what `ReaderStrip` has mounted. The strip only
 * ever mounts `MAX_MOUNTED` (3) chapters, and the chapter AFTER the one
 * that's mounted is frequently not in the DOM at all — a DOM-based approach
 * (e.g. rendering hidden `<img>` tags for the next chapter) structurally
 * cannot reach it. Instead this warms the browser's OWN image cache directly
 * via `new Image()` — the same cache the real `<img loading="lazy">` tags hit
 * once the reader scrolls there, so warming it is enough; there is no
 * separate cache to plumb data through.
 *
 * WHY `new Image()` over `fetch()`: it is the simplest thing that shares the
 * exact cache `<img>` consumption reads from with zero extra plumbing (no
 * blob handling, no manual cache-storage writes) — a `fetch()` response would
 * populate the HTTP cache too, but would also require pulling the whole body
 * into memory in this composable for no benefit over letting the `<img>`
 * decode pipeline do that later.
 *
 * BOUNDED CONCURRENCY: `PREFETCH_CONCURRENCY` caps in-flight page requests —
 * firing all ~165 requests for a chapter at once would saturate the browser's
 * per-origin connection pool and starve the pages the reader is ACTUALLY
 * looking at right now, defeating the whole point.
 *
 * TAIL-404 TOLERANCE: `pageCount` is a DECLARED count that may exceed the
 * CBZ's real image count (Kaizoku imports — see `ReaderChapter`'s doc
 * comment). A prefetch 404 on a trailing page is therefore expected and
 * harmless (the strip already tolerates trailing 404s — `trimTrailingFailures`
 * in `ReaderStrip.logic.ts`); `loadImage` below treats `onerror` exactly like
 * `onload` — it resolves either way and is never retried.
 */
import { watch, type Ref } from 'vue'

/** Never more than this many page requests in flight at once (see the file header). */
const PREFETCH_CONCURRENCY = 5

/** The slice of a reader chapter the prefetcher needs. `ReaderChapter` (from
 *  `useReader`) satisfies this structurally — no mapping required at the call
 *  site. */
export interface PrefetchChapter {
  /** Chapter UUID — passed to `pageUrl`. */
  id: string
  /** Declared page count (may exceed the CBZ's real image count). */
  pageCount: number
}

/**
 * loadImage — fires ONE page request by pointing a detached `Image` at `url`,
 * warming the browser's image cache. Resolves on EITHER `load` or `error` —
 * see the file header's tail-404 note for why a failure must never reject
 * (that would stall the pool worker awaiting it) or trigger a retry.
 */
function loadImage(url: string): Promise<void> {
  return new Promise((resolve) => {
    const img = new Image()
    img.onload = () => resolve()
    img.onerror = () => resolve()
    img.src = url
  })
}

/**
 * pageOrder — the page indices `0..pageCount-1`, ordered so the ones nearest
 * `center` come first, alternating outward (center, center+1, center-1,
 * center+2, center-2, …). Used for the CURRENT chapter, since the reader is
 * already somewhere inside it — the pages nearest their eyes should warm
 * first. `center` is clamped into range; an empty/invalid `pageCount` yields
 * an empty order (nothing to prefetch).
 */
function pageOrder(pageCount: number, center: number): number[] {
  if (pageCount <= 0) return []
  const start = Math.min(Math.max(0, center), pageCount - 1)
  const order = [start]
  let lo = start - 1
  let hi = start + 1
  while (lo >= 0 || hi < pageCount) {
    if (hi < pageCount) order.push(hi++)
    if (lo >= 0) order.push(lo--)
  }
  return order
}

/**
 * useChapterPrefetch — see the file header.
 *
 * `currentChapter` / `nextChapter` are the reader route's own resolved
 * chapter refs (current = the chapter the reader has open, falling back to
 * the deep-linked chapter before the strip reports a centred position; next =
 * its number-order successor). `currentPage` is the reader's live 0-based
 * page within the current chapter, read ONCE per chapter change to seed the
 * "nearest first" fetch order (not re-read continuously — the order is
 * decided when the chapter becomes current, not re-shuffled mid-flight).
 * `pageUrl` is `useReader.pageUrl` — reused so the prefetcher and the
 * rendered `<img>` tags build IDENTICAL URLs (same `?v=`, same cache entry).
 *
 * Behaviour:
 *   - A `currentChapter` change enqueues ALL of its pages, nearest-to-
 *     `currentPage` first, through the bounded pool.
 *   - Once that queue drains, `nextChapter`'s pages are enqueued (ascending —
 *     nobody is centred inside it yet).
 *   - A LATER `currentChapter` change abandons whichever queue (current-chapter
 *     or next-chapter) was still running — see `generation` below.
 *   - A URL already requested by THIS composable instance is never re-fired
 *     (re-entering an already-warmed chapter, e.g. scrolling back, is a no-op).
 *
 * Returns `dispose()` — the route calls it `onBeforeUnmount` so a still-running
 * queue stops silently instead of continuing to fire requests for a reader
 * that's no longer open.
 */
export function useChapterPrefetch(
  currentChapter: Ref<PrefetchChapter | null>,
  nextChapter: Ref<PrefetchChapter | null>,
  currentPage: Ref<number>,
  pageUrl: (chapterId: string, page: number) => string,
): { dispose: () => void } {
  // URLs already requested this composable's lifetime — the "don't re-fire"
  // dedup set. Never cleared; a warmed page stays warmed for the session.
  const requested = new Set<string>()

  // Bumped on every `currentChapter` change (including dispose). A running
  // `drain` loop checks its OWN captured generation before each page it
  // enqueues and stops the moment it goes stale — this is the abandonment
  // mechanism (no AbortController plumbing needed: a stale worker just quietly
  // stops asking for more pages; any request already in flight is left to
  // resolve, which is harmless — it's cache-warming, not cache-poisoning).
  let generation = 0

  /**
   * drain — runs `order` (page indices for `chapterId`) through the bounded
   * pool. Each worker pulls the next un-fetched index off the shared cursor
   * until the order is exhausted or `gen` is superseded. Resolves once every
   * worker stops (queue exhausted OR abandoned).
   */
  async function drain(chapterId: string, order: number[], gen: number): Promise<void> {
    let cursor = 0
    async function worker(): Promise<void> {
      while (cursor < order.length) {
        if (gen !== generation) return // superseded — a newer chapter took over
        const n = order[cursor]!
        cursor += 1
        const url = pageUrl(chapterId, n)
        if (requested.has(url)) continue
        requested.add(url)
        await loadImage(url)
      }
    }
    const workerCount = Math.min(PREFETCH_CONCURRENCY, order.length)
    await Promise.all(Array.from({ length: workerCount }, worker))
  }

  watch(currentChapter, (chapter) => {
    generation += 1
    const gen = generation
    if (!chapter) return
    const order = pageOrder(chapter.pageCount, currentPage.value)
    void drain(chapter.id, order, gen).then(() => {
      if (gen !== generation) return // this chapter is no longer current — don't chase a stale "next"
      const next = nextChapter.value
      if (!next) return
      void drain(next.id, pageOrder(next.pageCount, 0), gen)
    })
  }, { immediate: true })

  /** Abandons any in-flight queue. The route calls this `onBeforeUnmount`. */
  function dispose(): void {
    generation += 1
  }

  return { dispose }
}
