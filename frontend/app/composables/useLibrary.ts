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
import { ref, computed, watch, nextTick } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import type { SeriesSummary, CategorySummary } from '~/components/screens/types'
import { sortSeries, type SortKey, type SortDir } from '~/components/library/librarySort'
import {
  searchSeries, filterByCategory, applyFilters, countMatchesElsewhere,
  NO_FILTERS, type LibraryFilters,
} from '~/components/library/libraryFilter'
import { useLibraryPrefs } from '~/composables/useLibraryPrefs'

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
    needsSource: dto.needsSource,
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
  // The boolean toggle-filters (Downloaded / Unread / Completed / Needs source).
  // All off by default so the whole library shows; each narrows the in-memory
  // grid, independent of category/search/sort. Persisted with the sort (below).
  const filters = ref<LibraryFilters>({ ...NO_FILTERS })
  // Random-sort shuffle seed — a stable per-shuffle value so the Random order
  // doesn't reshuffle when an unrelated input (search/filter) changes. Bumped
  // only when the sort transitions INTO 'random' (see setSort).
  const randomSeed = ref(0)

  // Server-side persistence of {sortKey, sortDir, filters} — best-effort (§16):
  // a failed load keeps the defaults, a failed save is swallowed. `hydrated`
  // gates the save-watcher: it stays false until AFTER the watcher's first flush
  // has run (flipped on nextTick in loadAll — see there), so applying the loaded
  // prefs on mount never echoes a redundant save, and the pre-load defaults are
  // never persisted over stored prefs.
  const prefsApi = useLibraryPrefs()
  let hydrated = false

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
      const [firstPage, catRes, prefs] = await Promise.all([
        fetchSeriesPage(0),
        apiClient.GET('/api/categories'),
        prefsApi.load(),
      ])

      // Apply the persisted view state (sort + filters) before the first render.
      // Best-effort: prefs is null on any load failure → keep the defaults.
      if (prefs) {
        sortKey.value = prefs.sortKey
        sortDir.value = prefs.sortDir
        filters.value = { ...NO_FILTERS, ...prefs.filters }
      }
      // Flip `hydrated` on nextTick, AFTER the watcher's first flush — NOT
      // synchronously. Vue's watcher flush is async ('pre'), so the pref
      // mutations above schedule the save-watcher to run later; setting the flag
      // synchronously here would leave it already true when that first flush
      // runs, firing one redundant (idempotent) save of the just-loaded prefs.
      // nextTick resolves after the pending flush completes, so the first
      // watcher run sees hydrated=false and skips — only genuine later user
      // edits save.
      void nextTick(() => { hydrated = true })

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

  // What the grid renders: the active category, narrowed by the search query and
  // the "Needs source" toggle, then sorted — all in memory, recomputed on any
  // input ref change, ZERO refetch.
  const series = computed(() =>
    sortSeries(
      applyFilters(
        searchSeries(filterByCategory(allSeries.value, activeCategory.value), searchQuery.value),
        filters.value,
      ),
      sortKey.value,
      sortDir.value,
      randomSeed.value,
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
    // Freshly picking Random shuffles; a mere direction flip on an already-random
    // sort just reverses the existing order (no reshuffle).
    if (key === 'random' && sortKey.value !== 'random') randomSeed.value += 1
    sortKey.value = key
    sortDir.value = dir
  }

  function setFilters(next: LibraryFilters): void {
    filters.value = next
  }

  // Persist the view state whenever the sort or filters change — best-effort,
  // debounced (§16). Gated on `hydrated` so applying the loaded prefs on mount
  // doesn't immediately echo a save (and never clobbers stored prefs with the
  // pre-load defaults). randomSeed is intentionally NOT persisted — the shuffle
  // is ephemeral; only the fact that Random is the active key survives.
  watch([sortKey, sortDir, filters], () => {
    if (!hydrated) return
    prefsApi.save({ sortKey: sortKey.value, sortDir: sortDir.value, filters: filters.value })
  }, { deep: true })

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
    filters,
    matchesElsewhere,
    setCategory,
    setSearch,
    setSort,
    setFilters,
    searchEverywhere,
    reload: loadAll,
  }
}
