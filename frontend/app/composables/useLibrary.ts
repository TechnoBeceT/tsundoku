/**
 * useLibrary — data layer for the library list screen (Komikku/Suwayomi model).
 *
 * The WHOLE library loads ONCE. After that, category-switch + search + sort are
 * pure IN-MEMORY derivations — there is NO refetch on tab/search/sort, no
 * "Load more", no infinite scroll. Loading everything is what BUYS the right to
 * sort in memory honestly: sorting a partially-loaded page would rank "the most-
 * unread of the first 50 alphabetically" — a lie. Only the ACTIVE category is
 * ever RENDERED (a page concern), so the DOM stays bounded regardless of size.
 *
 * The one-time load pages under the 200 server cap: the first page reports the
 * exact total in X-Total-Count, and any remaining pages fire (in parallel) until
 * the whole library is in memory. At ~86 series today it is one request; the
 * design scales to the ~1000 the Kaizoku migration will reach.
 */
import { ref, computed } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import type { SeriesSummary, CategorySummary } from '~/components/screens/types'
import { sortSeries, type SortKey, type SortDir } from '~/components/library/librarySort'
import { searchSeries, filterByCategory, countMatchesElsewhere } from '~/components/library/libraryFilter'

type SeriesSummaryDTO = components['schemas']['SeriesSummary']
type CategoryDTO = components['schemas']['Category']

// pagination.MaxLimit — an internal detail of the ONE-TIME whole-library load,
// NOT a user-visible page size (the model never paginates for the user).
const PAGE = 200

/**
 * Map one backend SeriesSummaryDTO onto the screen's SeriesSummary.
 *
 * title  ← displayName  (resolved from the metadata-source provider; falls
 *                         back to the canonical title — same as what the
 *                         fixture stores as "title").
 * All other fields share the same name between DTO and screen type.
 */
function mapSeriesItem(dto: SeriesSummaryDTO): SeriesSummary {
  return {
    id: dto.id,
    title: dto.displayName,
    slug: dto.slug,
    category: dto.category,
    coverUrl: dto.coverUrl,
    monitored: dto.monitored,
    completed: dto.completed,
    chapterCounts: {
      total: dto.chapterCounts.total,
      downloaded: dto.chapterCounts.downloaded,
      wanted: dto.chapterCounts.wanted,
      failed: dto.chapterCounts.failed,
      unread: dto.chapterCounts.unread,
    },
    createdAt: dto.createdAt,
    lastChapterDownloadedAt: dto.lastChapterDownloadedAt,
  }
}

/**
 * Map one backend Category DTO onto the screen's CategorySummary.
 *
 * category ← name   (Category.name is the display label and folder name)
 * count    ← count  (series count; direct)
 */
function mapCategoryItem(dto: CategoryDTO): CategorySummary {
  return {
    category: dto.name,
    count: dto.count,
  }
}

export function useLibrary(opts: { initialCategory?: string | null } = {}) {
  // The whole library, unfiltered — the single source of truth every derivation
  // reads from.
  const allSeries = ref<SeriesSummary[]>([])
  const categories = ref<CategorySummary[]>([])
  const pending = ref(false)
  const error = ref<string | null>(null)
  const activeCategory = ref<string | null>(opts.initialCategory ?? null)
  const searchQuery = ref('')
  const sortKey = ref<SortKey>('title')
  const sortDir = ref<SortDir>('asc')

  // Fetch one page of /api/series at the given offset (no category filter — the
  // whole library is loaded, then filtered in memory).
  function fetchSeriesPage(offset: number) {
    return apiClient.GET('/api/series', {
      params: { query: { limit: PAGE, offset } },
    })
  }

  /**
   * Load the WHOLE library once. Reads X-Total-Count from the first page, then
   * fetches every remaining page in parallel and concatenates. Categories load
   * alongside; the landing category is resolved from them (see below).
   */
  async function loadAll(): Promise<void> {
    pending.value = true
    error.value = null
    try {
      const [firstPage, catRes] = await Promise.all([
        fetchSeriesPage(0),
        apiClient.GET('/api/categories'),
      ])

      if (firstPage.error || !firstPage.data) throw new Error('Failed to load library')

      const rows: SeriesSummaryDTO[] = [...firstPage.data]

      // Read the exact server total from X-Total-Count; fall back to the first
      // page length when the header is absent or non-numeric. Null-guard is
      // load-bearing: Number(null) === 0 (finite), which would suppress the
      // remaining pages.
      const raw = firstPage.response.headers.get('X-Total-Count')
      const headerTotal = Number(raw ?? NaN)
      const total = Number.isFinite(headerTotal) ? headerTotal : rows.length

      if (total > rows.length) {
        const offsets: number[] = []
        for (let off = PAGE; off < total; off += PAGE) offsets.push(off)
        const rest = await Promise.all(offsets.map(fetchSeriesPage))
        for (const p of rest) {
          if (p.error || !p.data) throw new Error('Failed to load library')
          rows.push(...p.data)
        }
      }

      allSeries.value = rows.map(mapSeriesItem)

      if (catRes.data) {
        categories.value = catRes.data.map(mapCategoryItem)
        // Landing category: ?category (opts.initialCategory) WINS. Else the
        // owner's default category (Category.isDefault — the SAME default that
        // catches new/uncategorized series). Else "All" (null) — never guess.
        if (!opts.initialCategory) {
          activeCategory.value = catRes.data.find(c => c.isDefault)?.name ?? null
        }
      }
    }
    catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to load library'
    }
    finally {
      pending.value = false
    }
  }

  // What the grid renders: the active category, narrowed by the search query,
  // then sorted — all in memory, recomputed on any input ref change, ZERO refetch.
  const series = computed(() =>
    sortSeries(
      searchSeries(filterByCategory(allSeries.value, activeCategory.value), searchQuery.value),
      sortKey.value,
      sortDir.value,
    ),
  )

  // The escape hatch's count: how many series match the query OUTSIDE the active
  // category ("3 matches in other categories").
  const matchesElsewhere = computed(() =>
    countMatchesElsewhere(allSeries.value, activeCategory.value, searchQuery.value),
  )

  function setCategory(name: string | null): void {
    activeCategory.value = name
  }

  function setSearch(q: string): void {
    searchQuery.value = q
  }

  function setSort(key: SortKey, dir: SortDir): void {
    sortKey.value = key
    sortDir.value = dir
  }

  // Widen to every category so an in-category search can escape to a library-wide one.
  function searchEverywhere(): void {
    activeCategory.value = null
  }

  void loadAll()

  return {
    series,
    categories,
    pending,
    error,
    activeCategory,
    searchQuery,
    sortKey,
    sortDir,
    matchesElsewhere,
    setCategory,
    setSearch,
    setSort,
    searchEverywhere,
    reload: loadAll,
  }
}
