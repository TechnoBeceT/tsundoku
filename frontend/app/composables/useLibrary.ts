/**
 * useLibrary — data layer for the library list screen.
 *
 * Fetches GET /api/series (with ?category=, ?limit=, ?offset=) and
 * GET /api/categories, maps the generated backend DTOs onto the screen's
 * SeriesSummary[] / CategorySummary[] types, and exposes a paginated, reactive
 * surface for <LibraryList>.
 *
 * Pagination: GET /api/series includes an X-Total-Count response header with
 * the exact server-side total. We read that header and fall back to
 * series.value.length only when it is absent or non-numeric.
 */
import { ref, computed } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import type { SeriesSummary, CategorySummary } from '~/components/screens/types'

type SeriesSummaryDTO = components['schemas']['SeriesSummary']
type CategoryDTO = components['schemas']['Category']

const PAGE = 50

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
    },
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
  const series = ref<SeriesSummary[]>([])
  const categories = ref<CategorySummary[]>([])
  const total = ref(0)
  const pending = ref(false)
  const error = ref<string | null>(null)
  const activeCategory = ref<string | null>(opts.initialCategory ?? null)
  const offset = ref(0)

  async function load(append = false): Promise<void> {
    pending.value = true
    error.value = null
    try {
      const [s, c] = await Promise.all([
        apiClient.GET('/api/series', {
          params: {
            query: {
              category: activeCategory.value ?? undefined,
              limit: PAGE,
              offset: offset.value,
            },
          },
        }),
        // Skip the category fetch on subsequent pages — the list doesn't change.
        append ? Promise.resolve(null) : apiClient.GET('/api/categories'),
      ])

      if (s.error || !s.data) throw new Error('Failed to load library')

      const page = s.data.map(mapSeriesItem)
      series.value = append ? [...series.value, ...page] : page

      // Read the exact server total from the X-Total-Count header; fall back to
      // the current series length when the header is absent or non-numeric.
      const headerTotal = Number(s.response.headers.get('X-Total-Count'))
      total.value = Number.isFinite(headerTotal) ? headerTotal : series.value.length

      if (c?.data) {
        categories.value = c.data.map(mapCategoryItem)
      }
    }
    catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to load library'
    }
    finally {
      pending.value = false
    }
  }

  function setCategory(name: string | null): void {
    activeCategory.value = name
    offset.value = 0
    void load(false)
  }

  function loadMore(): void {
    offset.value += PAGE
    void load(true)
  }

  void load(false)

  return {
    series,
    categories,
    total: computed(() => total.value),
    pending,
    error,
    activeCategory,
    setCategory,
    loadMore,
  }
}
