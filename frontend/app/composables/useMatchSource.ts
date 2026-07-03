/**
 * useMatchSource — data layer for the Series-Detail "Match source" dialog: the
 * inverse of removing a source. Lets the owner search across every Suwayomi
 * source for an ALREADY-imported series (by title, not by disk path) and
 * attach one picked candidate as a new provider.
 *
 * search(q) reuses the SAME cross-source `GET /api/search` endpoint (and the
 * shared `mapGroup` mapper) the Import/Adopt wizard uses — the backend returns
 * the identical SearchGroup/SearchCandidate DTO either way (§2 DRY: no second
 * mapper for the same shape).
 *
 * addProvider is `POST /api/series/{id}/providers` (AddProvider, shipped in
 * Library Import Phase A) — it returns the series' fresh SeriesDetail so the
 * caller can apply it without a second round-trip.
 *
 * `error` is shared across both operations (mirrors `useImport`'s single
 * error field) since only one of them is ever in flight from the dialog.
 *
 * `search()` guards its shared `groups`/`error` writes with a monotonic
 * request-generation counter (mirrors the identical fix in
 * `useScanLibrary.match()`): the owner can edit the query and re-search
 * before the previous request resolves — without the guard, a slower,
 * superseded response could land AFTER a faster later one and silently
 * overwrite `groups` with results for the WRONG query, letting the owner
 * attach a candidate that doesn't belong to the title in the search box.
 */
import { ref } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import { mapGroup } from '~/composables/importMappers'
import type { SearchGroup } from '~/components/screens/import.types'

type SeriesDetailDTO = components['schemas']['SeriesDetail']

/** The picked candidate + the priority to assign it (higher = higher priority). */
export interface AddProviderPayload {
  source: string
  mangaId: number
  importance: number
}

export function useMatchSource(seriesId: string) {
  const groups = ref<SearchGroup[]>([])
  const searching = ref(false)
  const saving = ref(false)
  const error = ref<string | null>(null)
  /** Monotonic request-generation counter for `search()`'s stale-response guard (see above). */
  let searchGeneration = 0

  /**
   * Cross-source title search — the same endpoint + grouping as the Import
   * wizard. Captures its own generation and clears `groups`/`error`
   * immediately (so a re-search never shows stale results while in flight,
   * and a failed re-search doesn't leave the PREVIOUS query's results
   * displayed as if they belonged to the new one); the eventual success or
   * failure is only written back to the shared `groups`/`error` refs if
   * this call is still the most recently started one — a superseded
   * response (even one for the same query re-run) is discarded.
   */
  async function search(q: string): Promise<void> {
    const generation = ++searchGeneration
    searching.value = true
    error.value = null
    groups.value = []
    try {
      const res = await apiClient.GET('/api/search', { params: { query: { q } } })
      if (res.error || !res.data) {
        throw new Error(res.error ? res.error.message : 'Search failed')
      }
      const mapped = res.data.map(mapGroup)
      if (generation === searchGeneration) groups.value = mapped
    }
    catch (err) {
      const message = err instanceof Error ? err.message : 'Search failed'
      if (generation === searchGeneration) error.value = message
    }
    finally {
      if (generation === searchGeneration) searching.value = false
    }
  }

  /**
   * Attaches the picked candidate as a new provider on this series. Resolves
   * the fresh SeriesDetail on success, or null on failure (with `error` set) —
   * the caller uses the null to decide whether to keep the dialog open.
   */
  async function addProvider(payload: AddProviderPayload): Promise<SeriesDetailDTO | null> {
    saving.value = true
    error.value = null
    try {
      const res = await apiClient.POST('/api/series/{id}/providers', {
        params: { path: { id: seriesId } },
        body: payload,
      })
      if (res.error || !res.data) {
        throw new Error(res.error ? res.error.message : 'Failed to add source')
      }
      return res.data
    }
    catch (err) {
      error.value = err instanceof Error ? err.message : 'Failed to add source'
      return null
    }
    finally {
      saving.value = false
    }
  }

  return { groups, searching, saving, error, search, addProvider }
}
