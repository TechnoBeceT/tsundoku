/**
 * useCategories — data layer for the categories overview screen AND the
 * Settings → Categories pane.
 *
 * Fetches GET /api/categories and exposes two mapped surfaces:
 *
 *   categories         — CategorySummary[] for the read-only overview grid (Part 1,
 *                        unchanged from the original implementation).
 *   settingsCategories — SettingsCategory[] for the CRUD pane (Part 2).
 *
 * Mapping (Part 1):
 *   category ← name   (Category.name is the display label and folder name)
 *   count    ← count  (series count; direct)
 *
 * Mapping (Part 2 / SettingsCategory):
 *   id        ← dto.id
 *   name      ← dto.name
 *   count     ← dto.count
 *   protected ← dto.protected  (the seeded "Other" — can never be renamed)
 *   isDefault ← dto.isDefault  (the single default landing category — can never be
 *                               deleted; any category can be promoted to it)
 *
 * CRUD mutations (§16 pattern — busy flag + inline error via categoryAction):
 *   addCategory(name)             POST /api/categories {name}
 *   renameCategory({id, name})    PATCH /api/categories/{id} {name}
 *   reorderCategory({id, dir})    swap sortOrder with neighbor; PATCH both sequentially
 *   setDefaultCategory(id)        PATCH /api/categories/{id}/default
 *   deleteCategory({id,targetId}) reassign members (if any) then DELETE
 */
import { computed, ref } from 'vue'
import { useAsyncResource } from '~/composables/useAsyncResource'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import type { CategorySummary } from '~/components/screens/types'
import {
  ADD_ACTION_ID,
  type ReorderDirection,
  type RowActionState,
  type SettingsCategory,
} from '~/components/screens/settings.types'

type CategoryDTO = components['schemas']['Category']

function mapCategory(dto: CategoryDTO): CategorySummary {
  return {
    category: dto.name,
    count: dto.count,
  }
}

// protected → can never be renamed (the seeded "Other"); isDefault → the single
// default landing category, which can never be deleted. Both come straight from
// the backend DTO — they are independent flags now (a demoted "Other" is
// protected but not the default).
function mapSettingsCategory(dto: CategoryDTO): SettingsCategory {
  return {
    id: dto.id,
    name: dto.name,
    count: dto.count,
    protected: dto.protected,
    isDefault: dto.isDefault,
  }
}

export function useCategories() {
  // Holds the raw CategoryDTO list from each GET /api/categories fetch. Used by
  // settingsCategories (projection) and reorderCategory (adjacency lookup).
  const rawDtos = ref<CategoryDTO[]>([])

  const { data: raw, pending, error, refresh } = useAsyncResource(async () => {
    const { data, error: fetchError } = await apiClient.GET('/api/categories')
    if (fetchError || !data) throw new Error('Failed to load categories')
    rawDtos.value = data
    return data.map(mapCategory)
  })

  // ── Part 1: read-only overview grid (existing consumers – DO NOT change) ──────
  const categories = computed(() => raw.value ?? [])

  // ── Part 2: Settings pane projection ─────────────────────────────────────────
  const settingsCategories = computed<SettingsCategory[]>(() =>
    rawDtos.value.map(mapSettingsCategory),
  )

  // §16 per-row mutation state: busyId flags the in-flight row; error carries any
  // backend failure message surfaced inline by the pane.
  const categoryAction = ref<RowActionState>({ busyId: null })

  /**
   * Internal helper — set busy, run fn, refetch on success, clear state.
   * Any thrown error (including backend {message}) surfaces in categoryAction.error.
   */
  async function categoryMutate(busyId: string, fn: () => Promise<void>): Promise<void> {
    categoryAction.value = { busyId }
    try {
      await fn()
      await refresh()
      categoryAction.value = { busyId: null }
    }
    catch (e) {
      categoryAction.value = {
        busyId: null,
        error: e instanceof Error ? e.message : 'Action failed',
      }
    }
  }

  /** Adds a new category. busyId is ADD_ACTION_ID during the call (no row id yet). */
  async function addCategory(name: string): Promise<void> {
    await categoryMutate(ADD_ACTION_ID, async () => {
      const res = await apiClient.POST('/api/categories', { body: { name } })
      if (res.error) throw new Error(res.error.message)
    })
  }

  /** Renames a category. The backend moves the on-disk folder atomically. */
  async function renameCategory({ id, name }: { id: string, name: string }): Promise<void> {
    await categoryMutate(id, async () => {
      const res = await apiClient.PATCH('/api/categories/{id}', {
        params: { path: { id } },
        body: { name },
      })
      if (res.error) throw new Error(res.error.message)
    })
  }

  /**
   * Reorders a category by swapping its sortOrder with the adjacent neighbor in the
   * given direction (−1 = up, +1 = down in the current list). No-ops silently when
   * already at the list edge (the pane's canMoveUp/canMoveDown flags prevent this
   * from the UI, but the guard is here for safety). Both rows are PATCHed sequentially
   * so the sorted view reflects the full swap on refetch.
   */
  async function reorderCategory({ id, direction }: { id: string, direction: ReorderDirection }): Promise<void> {
    const list = rawDtos.value
    const idx = list.findIndex(c => c.id === id)
    if (idx === -1) return
    const neighborIdx = idx + direction
    if (neighborIdx < 0 || neighborIdx >= list.length) return

    const target = list[idx]
    const neighbor = list[neighborIdx]

    await categoryMutate(id, async () => {
      // Give target the neighbor's sortOrder, then give neighbor the target's old value.
      const r1 = await apiClient.PATCH('/api/categories/{id}', {
        params: { path: { id: target.id } },
        body: { sortOrder: neighbor.sortOrder },
      })
      if (r1.error) throw new Error(r1.error.message)

      const r2 = await apiClient.PATCH('/api/categories/{id}', {
        params: { path: { id: neighbor.id } },
        body: { sortOrder: target.sortOrder },
      })
      if (r2.error) throw new Error(r2.error.message)
    })
  }

  /**
   * Promotes a category to be the single default landing for new / uncategorized
   * series. The backend demotes the previous default in the same transaction, so a
   * refetch reflects both the new DEFAULT badge and the previous default becoming
   * deletable.
   */
  async function setDefaultCategory(id: string): Promise<void> {
    await categoryMutate(id, async () => {
      const res = await apiClient.PATCH('/api/categories/{id}/default', {
        params: { path: { id } },
      })
      if (res.error) throw new Error(res.error.message)
    })
  }

  /**
   * Deletes a category. When `targetId` is non-empty, the category still has members
   * and they must be moved first: series are fetched page-by-page (limit 200) and
   * PATCHed to `targetId` until a fetch returns 0 results, then the now-empty
   * category is deleted.
   *
   * Member series are fetched by category NAME — GET /api/series?category=<name>
   * filters by name (string), not UUID. Re-fetching page-zero each pass works
   * because a reassigned series leaves the category filter, so the page shrinks
   * with every iteration. The loop is guaranteed to terminate (each pass removes
   * up to 200 series from the result set).
   *
   * When `targetId` is empty ("") the category is already empty and we DELETE directly.
   * Backend returns 409 if it still has members, surfaced via categoryAction.error.
   */
  async function deleteCategory({ id, targetId }: { id: string, targetId: string }): Promise<void> {
    await categoryMutate(id, async () => {
      if (targetId) {
        if (targetId === id) throw new Error('Target category must differ from the source category')

        const cat = rawDtos.value.find(c => c.id === id)
        if (!cat) throw new Error('Category not found')

        // Drain: reassigned series leave the category filter, so re-fetching
        // page-zero repeatedly drains the category batch-by-batch.
        for (;;) {
          const seriesRes = await apiClient.GET('/api/series', {
            params: { query: { category: cat.name, limit: 200 } },
          })
          if (seriesRes.error || !seriesRes.data) {
            throw new Error(seriesRes.error ? seriesRes.error.message : 'Failed to load series')
          }
          if (seriesRes.data.length === 0) break
          for (const s of seriesRes.data) {
            const r = await apiClient.PATCH('/api/series/{id}/category', {
              params: { path: { id: s.id } },
              body: { categoryId: targetId },
            })
            if (r.error) throw new Error(r.error.message)
          }
        }
      }

      const delRes = await apiClient.DELETE('/api/categories/{id}', {
        params: { path: { id } },
      })
      if (delRes.error) throw new Error(delRes.error.message)
    })
  }

  return {
    // ── Part 1: read-only surface (existing consumers, unchanged) ─────────────
    categories,
    pending,
    error,
    refresh,
    // ── Part 2: Settings CRUD surface (additive) ──────────────────────────────
    settingsCategories,
    categoryAction,
    addCategory,
    renameCategory,
    reorderCategory,
    setDefaultCategory,
    deleteCategory,
  }
}
