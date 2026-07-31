/**
 * useMatchDiskProvider — data layer for the Series-Detail "Match to source"
 * dialog (`MatchDiskProviderDialog`): search across Suwayomi sources for the
 * real source to attribute an unlinked disk-origin provider to, then load its
 * per-scanlator chapter-coverage breakdown so the owner can pick the right
 * group. Searching/browsing sources is orthogonal to the series-detail state
 * `useSeriesDetail` owns — same split as `useMatchSource` (the Series-Detail
 * add-source dialog's own composable) — and the actual match/link mutation
 * lives on `useSeriesDetail.matchDiskProvider` since it reseeds `series`
 * directly from the response (§16).
 *
 * search({q, sources}) reuses the SAME cross-source `GET /api/search` endpoint
 * (and the shared `mapGroup` mapper) every other search surface uses (§2 DRY:
 * one DTO, one mapper); `sources` is an optional source-ID filter (from the
 * `SourceFilterChips` row, empty = all sources). loadSources() lazily fetches
 * the `GET /api/sources` list once (guarded) to populate those chips — same
 * shape as `useMatchSource`. Its stale-response guard mirrors the identical fix in
 * `useMatchSource.search()` / `useImport.search()`: the owner can edit the
 * query and re-search before a slower, earlier request resolves — without the
 * guard a superseded response could land after a later one and silently
 * overwrite `groups`.
 *
 * loadBreakdown(source, mangaId, url) fetches
 * `GET /api/sources/{sourceId}/manga/{mangaId}/breakdown?url=` (the same
 * endpoint `useImport.loadBreakdowns` uses for the Adopt wizard's auto-split,
 * and the same `mapScanlatorCoverage` mapper) for the ONE candidate the owner
 * picked.
 * Unlike `useImport`'s permanent multi-candidate cache, this dialog only ever
 * has one candidate selected at a time, so `breakdown` is a single ref that a
 * new `loadBreakdown` call simply replaces; a failure resolves `null` (never
 * throws) so the dialog can fall back to an "all chapters, no split" choice.
 */
import { ref } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import { mapGroup, mapScanlatorCoverage } from '~/composables/importMappers'
import type { ScanlatorCoverage, SearchGroup, Source } from '~/components/screens/import.types'

type SourceDTO = components['schemas']['Source']

/**
 * Maps the `GET /api/sources` DTO onto the screen `Source` type. Re-declared
 * from `useMatchSource`/`useImport` (a trivial 3-line 1:1 mapper) rather than
 * exported+shared — keeping the tiny mapper local avoids widening another
 * composable's public surface just to reach it.
 */
function mapSource(dto: SourceDTO): Source {
  return { id: dto.id, name: dto.name, lang: dto.lang, degraded: dto.degraded, degradedReason: dto.degradedReason }
}

export function useMatchDiskProvider() {
  const groups = ref<SearchGroup[]>([])
  const searching = ref(false)
  const breakdown = ref<ScanlatorCoverage[] | null>(null)
  const breakdownLoading = ref(false)
  const error = ref<string | null>(null)
  /** Monotonic request-generation counter for `search()`'s stale-response guard (see above). */
  let searchGeneration = 0

  // ---- sources (the source-filter chip list, loaded lazily on first open) ----
  // Mirrors `useMatchSource`: this dialog only needs the source list once the
  // owner opens it, so `loadSources` is called on-demand and guarded to fetch
  // at most once for the composable's lifetime.
  const sources = ref<Source[]>([])
  let sourcesLoaded = false

  /** Fetch the source list once — a no-op on every call after the first. */
  async function loadSources(): Promise<void> {
    if (sourcesLoaded) return
    sourcesLoaded = true
    const res = await apiClient.GET('/api/sources')
    if (res.data) {
      sources.value = res.data.map(mapSource)
    }
  }

  /**
   * Cross-source title search — the same endpoint + grouping as the Import
   * wizard and the add-source dialog. Captures its own generation and clears
   * `groups`/`error` immediately (so a re-search never shows stale results
   * while in flight); the eventual success or failure is only written back if
   * this call is still the most recently started one. `sources` is an optional
   * list of source IDs to restrict the search to (from `SourceFilterChips`); an
   * empty list searches every source (mirrors `useMatchSource.search`).
   */
  async function search(payload: { q: string, sources: string[] }): Promise<void> {
    const generation = ++searchGeneration
    searching.value = true
    error.value = null
    groups.value = []
    try {
      // Omit sources param when empty (all sources searched); join as CSV when set.
      const query: { q: string, sources?: string } = { q: payload.q }
      if (payload.sources.length > 0) {
        query.sources = payload.sources.join(',')
      }
      const res = await apiClient.GET('/api/search', { params: { query } })
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
   * Loads the picked candidate's per-scanlator chapter-coverage breakdown so
   * the owner can choose exactly which group to match against. Never throws —
   * a failure resolves `breakdown` to `null` (the dialog then offers an
   * "all chapters" fallback) and does NOT touch `error` (this is informational
   * coverage, not a hard failure of the match flow itself).
   */
  async function loadBreakdown(source: string, mangaId: number, url: string): Promise<void> {
    breakdownLoading.value = true
    breakdown.value = null
    try {
      const res = await apiClient.GET('/api/sources/{sourceId}/manga/{mangaId}/breakdown', {
        params: {
          path: { sourceId: source, mangaId },
          query: { url },
        },
      })
      breakdown.value = res.error || !res.data ? null : res.data.scanlators.map(mapScanlatorCoverage)
    }
    catch {
      breakdown.value = null
    }
    finally {
      breakdownLoading.value = false
    }
  }

  return { sources, groups, searching, breakdown, breakdownLoading, error, loadSources, search, loadBreakdown }
}
