/**
 * useMatchSource — data layer for the Series-Detail "Add a source" dialog: the
 * inverse of removing a source. Lets the owner search across every Suwayomi
 * source for an ALREADY-imported series (by title, not by disk path), gather
 * one or more candidates (via the shared `useSourceConfigure` tray/Configure
 * flow), and attach them all in one batch call.
 *
 * search(q) reuses the SAME cross-source `GET /api/search` endpoint (and the
 * shared `mapGroup` mapper) the Import/Adopt wizard uses — the backend returns
 * the identical SearchGroup/SearchCandidate DTO either way (§2 DRY: no second
 * mapper for the same shape).
 *
 * loadBreakdowns(candidates) is copied from `useImport.loadBreakdowns` (same
 * cache/in-flight-guard/parallel-fetch shape, §2 DRY): fetches the
 * per-scanlator chapter-coverage breakdown for each given (source, mangaId)
 * pair, caching by `source:mangaId` — an absent key = not yet fetched, `null`
 * = the fetch failed (the composable falls back to a single unsplit row).
 *
 * batchAddProviders is `POST /api/series/{id}/providers/batch` (Slice P) — it
 * attaches every given `ProviderRef`, best-first, at an importance the
 * backend assigns strictly below the series' existing providers, and returns
 * the series' fresh SeriesDetail so the caller can reseed without a second
 * round-trip (§16).
 *
 * `error` is shared across all three operations (mirrors the pre-Slice-P
 * single-`addProvider` version) since only one is ever in flight from the
 * dialog.
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
import { mapGroup, mapScanlatorCoverage } from '~/composables/importMappers'
import type { ProviderRef } from '~/composables/useSourceConfigure'
import type { ScanlatorCoverage, SearchCandidate, SearchGroup } from '~/components/screens/import.types'

type SeriesDetailDTO = components['schemas']['SeriesDetail']

/** Stable cache/in-flight key for one (source, mangaId) breakdown fetch (mirrors `useImport`). */
function breakdownKey(source: string, mangaId: number): string {
  return `${source}:${mangaId}`
}

export function useMatchSource(seriesId: string) {
  const groups = ref<SearchGroup[]>([])
  const searching = ref(false)
  const saving = ref(false)
  const error = ref<string | null>(null)
  /** Monotonic request-generation counter for `search()`'s stale-response guard (see above). */
  let searchGeneration = 0

  // ---- breakdowns (per-scanlator coverage, Configure-stage auto-split) -------
  // Keyed by `source:mangaId`. `null` = fetch attempted and failed (the dialog
  // falls back to a single unsplit row); an absent key = not yet attempted.
  const breakdowns = ref<Record<string, ScanlatorCoverage[] | null>>({})
  const breakdownsInFlight = new Set<string>()

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
   * Fetches the per-scanlator breakdown for every given candidate IN
   * PARALLEL, skipping any candidate already cached (success or failure) or
   * already in flight. Never throws — a per-candidate failure caches `null`
   * and is otherwise swallowed (non-fatal; the Configure stage renders that
   * source as a single unsplit row). Copied from `useImport.loadBreakdowns`
   * (§2 DRY — identical cache/in-flight-guard/parallel-fetch shape).
   */
  async function loadBreakdowns(candidates: SearchCandidate[]): Promise<void> {
    const toFetch = candidates.filter((c) => {
      const key = breakdownKey(c.source, c.mangaId)
      return !(key in breakdowns.value) && !breakdownsInFlight.has(key)
    })
    if (toFetch.length === 0) return
    for (const c of toFetch) breakdownsInFlight.add(breakdownKey(c.source, c.mangaId))
    await Promise.all(toFetch.map(async (c) => {
      const key = breakdownKey(c.source, c.mangaId)
      try {
        const res = await apiClient.GET('/api/sources/{sourceId}/manga/{mangaId}/breakdown', {
          params: { path: { sourceId: c.source, mangaId: c.mangaId } },
        })
        breakdowns.value = {
          ...breakdowns.value,
          [key]: res.error || !res.data ? null : res.data.scanlators.map(mapScanlatorCoverage),
        }
      }
      catch {
        breakdowns.value = { ...breakdowns.value, [key]: null }
      }
      finally {
        breakdownsInFlight.delete(key)
      }
    }))
  }

  /**
   * Attaches every given source to this series in one call — the batch
   * counterpart of the old single-source `addProvider` (Slice P). Carries no
   * importance: the backend assigns each provider an importance strictly
   * below the series' existing ones, in list order (decision E). Resolves
   * the fresh SeriesDetail on success, or null on failure (with `error`
   * set) — the caller uses the null to decide whether to keep the dialog
   * open.
   */
  async function batchAddProviders(providers: ProviderRef[]): Promise<SeriesDetailDTO | null> {
    saving.value = true
    error.value = null
    try {
      const res = await apiClient.POST('/api/series/{id}/providers/batch', {
        params: { path: { id: seriesId } },
        body: { providers },
      })
      if (res.error || !res.data) {
        throw new Error(res.error ? res.error.message : 'Failed to add sources')
      }
      return res.data
    }
    catch (err) {
      error.value = err instanceof Error ? err.message : 'Failed to add sources'
      return null
    }
    finally {
      saving.value = false
    }
  }

  return { groups, searching, saving, error, breakdowns, search, loadBreakdowns, batchAddProviders }
}
