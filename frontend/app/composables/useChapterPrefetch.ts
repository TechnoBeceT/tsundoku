/**
 * Warms every page of the current chapter, then its successor, using the same
 * versioned URLs as the reader images. Detached images reach chapters outside
 * the strip's mounted window without a second cache or object URLs.
 *
 * Pending work follows the live reading position. Already-started requests keep
 * their slots across chapter changes, bounding background transfers to five.
 * Low fetch priority lets visible reader images compete for the connection.
 * Errors advance the queue without retrying: imported page counts can include
 * missing trailing images, which the strip handles separately.
 */
import { watch, type Ref } from 'vue'

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
 * missing trailing pages must advance the queue without retries.
 */
function loadImage(url: string): Promise<void> {
  return new Promise((resolve) => {
    const img = new Image()
    img.onload = () => resolve()
    img.onerror = () => resolve()
    img.fetchPriority = 'low'
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
 * Prefetches all current pages nearest the live centre first, then all next
 * pages in ascending order. A page or chapter change replaces pending work;
 * started URLs remain deduplicated, including failures. Dispose stops admission
 * and watches while allowing the bounded in-flight requests to finish.
 */
export function useChapterPrefetch(
  currentChapter: Ref<PrefetchChapter | null>,
  nextChapter: Ref<PrefetchChapter | null>,
  currentPage: Ref<number>,
  pageUrl: (chapterId: string, page: number) => string,
): { dispose: () => void } {
  const requested = new Set<string>()
  const inFlight = new Set<string>()
  let currentUrls = new Set<string>()
  let currentQueue: string[] = []
  let nextQueue: string[] = []
  let disposed = false

  function pump(): void {
    while (!disposed && inFlight.size < PREFETCH_CONCURRENCY) {
      // The next chapter starts only after the current chapter's requests have
      // settled, including requests started before the latest position change.
      const currentPending = [...inFlight].some(url => currentUrls.has(url))
      const url = currentQueue.shift() ?? (!currentPending ? nextQueue.shift() : undefined)
      if (!url) return
      if (requested.has(url)) continue
      requested.add(url)
      inFlight.add(url)
      void loadImage(url).then(() => {
        inFlight.delete(url)
        pump()
      })
    }
  }

  const stop = watch([currentChapter, nextChapter, currentPage], ([chapter, next, page]) => {
    const urls = chapter ? pageOrder(chapter.pageCount, page).map(n => pageUrl(chapter.id, n)) : []
    currentUrls = new Set(urls)
    currentQueue = urls.filter(url => !requested.has(url))
    nextQueue = chapter && next
      ? pageOrder(next.pageCount, 0).map(n => pageUrl(next.id, n)).filter(url => !requested.has(url))
      : []
    pump()
  }, { immediate: true })

  function dispose(): void {
    disposed = true
    stop()
    currentQueue = []
    nextQueue = []
  }

  return { dispose }
}
