/**
 * useReader — data + windowing layer for the long-strip chapter reader.
 *
 * Loads GET /api/series/{id} (mirrors useSeriesDetail's client call), derives the
 * reader's chapter list — downloaded chapters only, number-ascending — and
 * maintains a bounded MOUNTED WINDOW of chapters the ReaderStrip renders. As the
 * reader nears the tail the strip calls `onNearTail()`, which appends the next
 * chapter and unmounts far-above chapters so the live DOM stays bounded
 * (`MAX_MOUNTED`).
 *
 * The reader addresses pages by the Chapter UUID (`pageUrl`), which the backend's
 * page-bytes and progress endpoints key on — cookie auth rides a plain `<img src>`
 * same-origin (QCAT-020), so no fetch/objectURL machinery is needed; the browser
 * lazy-loads and evicts page images natively.
 *
 * Slice 3 (progress + resume) will extend this surface: it consumes `startChapterId`
 * + each chapter's `lastReadPage` to resume mid-chapter, and will add the progress
 * write. This slice only reads progress fields; it never writes them.
 */
import { ref, computed } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import { chaptersToUnmount, MAX_MOUNTED } from '~/components/reader/ReaderStrip.logic'

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
   * `chaptersToUnmount` rule (§2 DRY). A no-op once the last chapter is mounted.
   */
  const onNearTail = (): void => {
    if (lastMounted.value < 0) return
    if (lastMounted.value < chapters.value.length - 1) lastMounted.value += 1
    const mounted: number[] = []
    for (let i = firstMounted.value; i <= lastMounted.value; i++) mounted.push(i)
    const drop = chaptersToUnmount(mounted, MAX_MOUNTED)
    if (drop.length > 0) firstMounted.value = drop[drop.length - 1]! + 1
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
    loading,
    error,
    seriesTitle,
    startChapterId,
    refresh,
  }
}
