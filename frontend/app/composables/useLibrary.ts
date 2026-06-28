/**
 * useLibrary — data layer for the library list screen.
 *
 * Fetches GET /api/series (with ?category=, ?limit=, ?offset=) and
 * GET /api/categories, maps the generated backend DTOs onto the screen's
 * SeriesSummary[] / CategorySummary[] types, and exposes a paginated, reactive
 * surface for <LibraryList>.
 *
 * Pagination note: GET /api/series returns a plain SeriesSummary[] array with
 * no pagination envelope and no total field. We use page.length === PAGE as a
 * "possibly more results" sentinel — a full page bumps total by 1 so hasMore
 * stays true; a short page closes the affordance.
 *
 * Category note: the generated schema types the ?category= param as the legacy
 * enum ("Manga"|"Manhwa"|...). M11 made categories user-definable free strings,
 * but the OpenAPI spec has not yet been updated. The cast below is safe at
 * runtime — any category name the backend stored is accepted by the backend.
 */
import { ref, computed } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import type { SeriesSummary, CategorySummary } from '~/components/screens/types'

type SeriesSummaryDTO = components['schemas']['SeriesSummary']
type CategoryDTO = components['schemas']['Category']

// The generated schema still reflects the original fixed-enum set. We widen to
// string & {} to allow user-defined category names without a hard cast.
type CategoryParam = 'Manga' | 'Manhwa' | 'Manhua' | 'Comic' | 'Other'

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

export function useLibrary() {
  const series = ref<SeriesSummary[]>([])
  const categories = ref<CategorySummary[]>([])
  const total = ref(0)
  const pending = ref(false)
  const error = ref<string | null>(null)
  const activeCategory = ref<string | null>(null)
  const offset = ref(0)

  async function load(append = false): Promise<void> {
    pending.value = true
    error.value = null
    try {
      const [s, c] = await Promise.all([
        apiClient.GET('/api/series', {
          params: {
            query: {
              // Cast required: generated schema still uses the legacy fixed enum;
              // M11 user-defined categories are free strings at runtime.
              category: (activeCategory.value ?? undefined) as CategoryParam | undefined,
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

      // Compute a sentinel total: if we got a full page there might be more;
      // a short page means we've reached the end.
      total.value = series.value.length + (page.length === PAGE ? 1 : 0)

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
