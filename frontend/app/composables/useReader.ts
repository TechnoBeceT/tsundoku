/**
 * useReader — data + windowing layer for the long-strip chapter reader.
 *
 * Loads GET /api/series/{id} (mirrors useSeriesDetail's client call), derives the
 * reader's chapter list — downloaded chapters only, number-ascending — and
 * maintains a bounded MOUNTED WINDOW of chapters the ReaderStrip renders. The
 * window grows in BOTH directions: as the reader nears the tail the strip calls
 * `onNearTail()` (appends the next chapter), and as they scroll back up towards
 * the head it calls `onNearHead()` (prepends the previous chapter) — both bound
 * the live DOM to at most `MAX_MOUNTED` chapters, dropping from whichever end the
 * reader is moving away from (`chaptersToUnmountDirectional`) so a prepend never
 * unmounts the chapter it just brought in.
 *
 * `currentChapterId`/`setCurrentChapter` track the chapter under the viewport
 * midpoint (fed by the strip's `centered` event); `prevChapter`/`nextChapter`/
 * `hasPrev`/`hasNext` are that chapter's number-order neighbours, driving the
 * reader chrome's prev/next controls. `requestScroll(chapterId, page)` is the
 * ONE place that publishes a `scrollRequest` (the route's resume-anchor scroll
 * AND `jumpToChapter` both go through it — Fix 4, see `ScrollRequest`'s doc
 * comment for why a shared token, not a boolean fuse or a caller-owned literal,
 * is required); `jumpToChapter` reseeds the window to a single target chapter
 * and calls it directly.
 *
 * The reader addresses pages by the Chapter UUID (`pageUrl`), which the backend's
 * page-bytes and progress endpoints key on — cookie auth rides a plain `<img src>`
 * same-origin (QCAT-020), so no fetch/objectURL machinery is needed; the browser
 * lazy-loads and evicts page images natively.
 */
import { ref, computed } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import { chaptersToUnmountDirectional, MAX_MOUNTED } from '~/components/reader/ReaderStrip.logic'

type SeriesDetailDTO = components['schemas']['SeriesDetail']
type ChapterDTO = components['schemas']['Chapter']

/**
 * ReaderChapter — one chapter in the reader's ordered list. `id` is the Chapter
 * UUID the page/progress endpoints key on; `pageCount` is the DECLARED count
 * (may exceed the real image count — the strip tolerates trailing 404s).
 * `read`/`lastReadPage` are the persisted reader progress (Slice 3 resumes from
 * `lastReadPage`).
 */
export interface ReaderChapter {
  /** Chapter UUID — the page/progress endpoints' identifier. */
  id: string
  /** Display/sort number (null when unknown; nulls sort last). */
  number: number | null
  /** Resolved chapter display name (may be empty). */
  name: string
  /** Declared page count (may exceed the CBZ's real image count). */
  pageCount: number
  /** Persisted "fully read" flag. */
  read: boolean
  /** Persisted 0-based last-viewed page (Slice 3 resume anchor). */
  lastReadPage: number
}

/** Maps a downloaded ChapterDTO to the reader's slimmer ReaderChapter. */
function mapReaderChapter(dto: ChapterDTO): ReaderChapter {
  return {
    id: dto.id,
    number: dto.number,
    name: dto.name,
    pageCount: dto.pageCount ?? 0,
    read: dto.read,
    lastReadPage: dto.lastReadPage,
  }
}

/** Sort helper: number-ascending, with unknown (null) numbers pushed to the end. */
function byNumberAsc(a: ReaderChapter, b: ReaderChapter): number {
  if (a.number == null) return b.number == null ? 0 : 1
  if (b.number == null) return -1
  return a.number - b.number
}

/**
 * ScrollRequest — a one-shot ask for the strip to scroll to a specific page.
 * `token` is the dedup key: the strip acts on each new token exactly once.
 * Published ONLY via `useReader.requestScroll` (Fix 4: this is the ONE token
 * space, owned here) — the route's resume-anchor scroll and every
 * `jumpToChapter` navigation draw from the same counter, so they can never
 * mint colliding tokens (the bug this fixes: the route used to hardcode
 * `token: 1` for the resume scroll, which collided with the FIRST
 * `jumpToChapter` call — itself also starting from 1 — and silently swallowed
 * whichever fired second).
 */
export interface ScrollRequest {
  /** Chapter UUID to scroll to. */
  chapterId: string
  /** 0-based page index within that chapter. */
  page: number
  /** Monotonically increasing per-request id, scoped to ONE `useReader()`
   *  instance (never reused within it; not shared across instances — a
   *  module-scoped counter would be needless cross-instance coupling). */
  token: number
}

/**
 * useReader — see the file header. `seriesId` is the series UUID; `startChapterId`
 * is the Chapter UUID to open at (the deep-linked chapter). Returns the reactive
 * reader surface consumed by the reader route + ReaderStrip.
 */
export function useReader(seriesId: string, startChapterId: string) {
  const chapters = ref<ReaderChapter[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)
  // The series' resolved display title (metadata-source name, else canonical
  // title) — the reader chrome's top-bar heading. Empty until the load lands.
  const seriesTitle = ref('')

  // The mounted window is the inclusive index range [firstMounted, lastMounted]
  // into `chapters`. Both are -1 until the list loads (empty window).
  const firstMounted = ref(-1)
  const lastMounted = ref(-1)

  /** The chapters currently mounted in the strip, in reading order. */
  const mountedChapters = computed<ReaderChapter[]>(() =>
    firstMounted.value < 0 ? [] : chapters.value.slice(firstMounted.value, lastMounted.value + 1),
  )

  /**
   * pageUrl — the same-origin page-bytes URL for one page of a chapter. A plain
   * string an `<img src>` loads directly (cookie auth, no fetch/objectURL). `n`
   * is the 0-based page index.
   */
  const pageUrl = (chapterId: string, n: number): string =>
    `/api/series/${seriesId}/chapters/${chapterId}/pages/${n}`

  /**
   * onNearTail — the strip calls this as the tail sentinel appears: append the
   * next chapter (if any) into the window, then unmount any far-above chapters
   * beyond `MAX_MOUNTED` so the live DOM stays bounded. Reuses the shared
   * `chaptersToUnmountDirectional` rule (§2 DRY) in its 'forward' mode — the
   * window is growing downward, so the drop stays at the top, same as before
   * the bidirectional migration. A no-op once the last chapter is mounted.
   */
  const onNearTail = (): void => {
    if (lastMounted.value < 0) return
    if (lastMounted.value < chapters.value.length - 1) lastMounted.value += 1
    const mounted: number[] = []
    for (let i = firstMounted.value; i <= lastMounted.value; i++) mounted.push(i)
    const drop = chaptersToUnmountDirectional(mounted, MAX_MOUNTED, 'forward')
    if (drop.length > 0) firstMounted.value = drop[drop.length - 1]! + 1
  }

  /**
   * onNearHead — the strip calls this as the head sentinel appears: prepend the
   * previous chapter (if any) into the window, then unmount any far-below
   * chapters beyond `MAX_MOUNTED`. The backward mirror of `onNearTail`: the
   * window is growing upward, so `chaptersToUnmountDirectional(..., 'backward')`
   * drops from the BOTTOM — this is what stops the window unmounting the very
   * chapter it just prepended (the old top-dropping rule would have). A no-op
   * once the first chapter is mounted.
   *
   * The prepend this triggers MUST be anchor-bracketed by the strip (its
   * `beforeReflow`/`afterReflow` pair) so inserting content above the viewport
   * doesn't visibly jump the reader's scroll position — that wiring lands in a
   * later slice; this composable only performs the window mutation.
   */
  const onNearHead = (): void => {
    if (firstMounted.value <= 0) return
    firstMounted.value -= 1
    const mounted: number[] = []
    for (let i = firstMounted.value; i <= lastMounted.value; i++) mounted.push(i)
    const drop = chaptersToUnmountDirectional(mounted, MAX_MOUNTED, 'backward')
    if (drop.length > 0) lastMounted.value = drop[0]! - 1
  }

  // currentChapterId — the chapter under the reader's viewport midpoint, fed by
  // the strip's `centered` event via `setCurrentChapter`. Null until the strip
  // reports a position (nothing mounted yet, or not wired up by the caller).
  const currentChapterId = ref<string | null>(null)

  /** setCurrentChapter — records the chapter currently under the viewport midpoint. */
  const setCurrentChapter = (id: string): void => {
    currentChapterId.value = id
  }

  /** Index of `currentChapterId` within `chapters`, or -1 when unset/unknown. */
  const currentIndex = computed<number>(() =>
    currentChapterId.value == null ? -1 : chapters.value.findIndex((ch) => ch.id === currentChapterId.value),
  )

  /** The chapter immediately before the current one in number order, null at the head or when unset. */
  const prevChapter = computed<ReaderChapter | null>(() =>
    currentIndex.value > 0 ? chapters.value[currentIndex.value - 1]! : null,
  )

  /** The chapter immediately after the current one in number order, null at the tail or when unset. */
  const nextChapter = computed<ReaderChapter | null>(() =>
    currentIndex.value >= 0 && currentIndex.value < chapters.value.length - 1
      ? chapters.value[currentIndex.value + 1]!
      : null,
  )

  /** Whether a previous chapter exists to navigate to. */
  const hasPrev = computed<boolean>(() => prevChapter.value !== null)
  /** Whether a next chapter exists to navigate to. */
  const hasNext = computed<boolean>(() => nextChapter.value !== null)

  /** The strip's pending scroll instruction (see `ScrollRequest`); null once nothing is pending. */
  const scrollRequest = ref<ScrollRequest | null>(null)

  // INSTANCE-scoped monotonic counter (Fix 4) — one per `useReader()` call, so
  // it stays private to this reader's own token space rather than being shared
  // module-wide across every instance in the process.
  let scrollRequestCounter = 0

  /**
   * requestScroll — the ONE place that publishes a scroll-to-target request:
   * increments the shared token counter and asks the strip to scroll to
   * (chapterId, page). BOTH the route's resume-anchor scroll on open AND
   * `jumpToChapter` go through this, so they draw from the same token space
   * and can never collide (see `ScrollRequest`'s doc comment for the bug this
   * fixes).
   */
  const requestScroll = (chapterId: string, page: number): void => {
    scrollRequestCounter += 1
    scrollRequest.value = { chapterId, page, token: scrollRequestCounter }
  }

  /**
   * jumpToChapter — collapses the mounted window down to a single target
   * chapter and asks the strip to scroll to its top (prev/next navigation and
   * direct jumps; the initial deep link/resume is handled via `requestScroll`
   * directly by the route instead). The strip's own `onNearTail`/`onNearHead`
   * re-grow the window from there as the reader scrolls. A no-op when `id`
   * isn't in the loaded chapter list.
   */
  const jumpToChapter = (id: string): void => {
    const idx = chapters.value.findIndex((ch) => ch.id === id)
    if (idx < 0) return
    firstMounted.value = idx
    lastMounted.value = idx
    currentChapterId.value = id
    requestScroll(id, 0)
  }

  /**
   * refresh — (re)load the series and rebuild the chapter list + window. The
   * window opens at `startChapterId`; if that chapter is absent (not downloaded
   * or unknown) it falls back to the first downloaded chapter. §16: sets
   * `loading` while in flight and surfaces a real message on failure.
   */
  async function refresh(): Promise<void> {
    loading.value = true
    error.value = null
    try {
      const res = await apiClient.GET('/api/series/{id}', { params: { path: { id: seriesId } } })
      if (res.error || !res.data) throw new Error('Failed to load chapters')
      const detail: SeriesDetailDTO = res.data
      seriesTitle.value = detail.displayName || detail.title || ''
      const list = detail.chapters
        .filter((ch) => ch.state === 'downloaded')
        .map(mapReaderChapter)
        .sort(byNumberAsc)
      chapters.value = list
      if (list.length === 0) {
        firstMounted.value = -1
        lastMounted.value = -1
        return
      }
      const found = list.findIndex((ch) => ch.id === startChapterId)
      const start = found >= 0 ? found : 0
      firstMounted.value = start
      lastMounted.value = start
    }
    catch (err) {
      error.value = err instanceof Error ? err.message : 'Failed to load chapters'
    }
    finally {
      loading.value = false
    }
  }

  void refresh()

  return {
    chapters,
    mountedChapters,
    pageUrl,
    onNearTail,
    onNearHead,
    currentChapterId,
    setCurrentChapter,
    prevChapter,
    nextChapter,
    hasPrev,
    hasNext,
    jumpToChapter,
    requestScroll,
    scrollRequest,
    loading,
    error,
    seriesTitle,
    startChapterId,
    refresh,
  }
}
