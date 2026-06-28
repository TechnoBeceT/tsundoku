/**
 * useCategories — data layer for the categories overview screen.
 *
 * Fetches GET /api/categories and maps each Category DTO onto the screen's
 * CategorySummary type. Wraps useAsyncResource for the standard
 * { data, pending, error, refresh } pattern.
 *
 * Mapping:
 *   category ← name   (Category.name is the display label and folder name)
 *   count    ← count  (series count; direct)
 */
import { computed } from 'vue'
import { useAsyncResource } from '~/composables/useAsyncResource'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import type { CategorySummary } from '~/components/screens/types'

type CategoryDTO = components['schemas']['Category']

function mapCategory(dto: CategoryDTO): CategorySummary {
  return {
    category: dto.name,
    count: dto.count,
  }
}

export function useCategories() {
  const { data: raw, pending, error, refresh } = useAsyncResource(async () => {
    const { data, error: fetchError } = await apiClient.GET('/api/categories')
    if (fetchError || !data) throw new Error('Failed to load categories')
    return data.map(mapCategory)
  })

  const categories = computed(() => raw.value ?? [])

  return { categories, pending, error, refresh }
}
