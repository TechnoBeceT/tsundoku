/**
 * Source search and attachment for an existing library series.
 * useSourceSearch owns progressive result snapshots and cancellation;
 * useSourceCoverage owns this dialog's exact-address breakdown cache,
 * completion-event refetches and explicit refresh. Coverage failures stay
 * separate from search/attachment errors so configuration retains its fallback.
 * Source options load lazily when the dialog opens. batchAddProviders submits
 * the owner's ordered providers and returns the fresh series detail on success.
 */
import { useSourceSearch } from './useSourceSearch'
import { useSourceCoverage } from './useSourceCoverage'
import { ref, watch } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import type { ProviderRef } from '~/composables/useSourceConfigure'
import type { Source } from '~/components/screens/import.types'

type SeriesDetailDTO = components['schemas']['SeriesDetail']
type SourceDTO = components['schemas']['Source']

/**
 * Maps the `GET /api/sources` DTO onto the screen `Source` type. Re-declared
 * from `useImport` (a trivial 3-line 1:1 mapper) rather than exported+shared —
 * keeping the tiny mapper local avoids widening `useImport`'s public surface.
 */
function mapSource(dto: SourceDTO): Source {
  return { id: dto.id, name: dto.name, lang: dto.lang, degraded: dto.degraded, degradedReason: dto.degradedReason }
}

export function useMatchSource(seriesId: string) {
  const sourceSearch = useSourceSearch()
  const groups = sourceSearch.groups
  const pendingSources = sourceSearch.pendingSources
  const searching = sourceSearch.searching
  const saving = ref(false)
  const error = ref<string | null>(null)
  // Search failures share this surface with the domain actions below.
  watch(sourceSearch.error, value => { error.value = value })

  /** Starts a fresh dialog lifecycle and makes every earlier search response stale. */
  const resetSearch = sourceSearch.reset

  // ---- sources (the source-filter chip list, loaded lazily on first open) ----
  // Unlike the Import wizard (which loads sources eagerly on mount), this dialog
  // only needs the list once the owner opens it, so `loadSources` is called
  // on-demand and guarded to fetch at most once for the composable's lifetime.
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

  const { breakdowns, breakdownSnapshots, loadBreakdowns, refreshBreakdown } = useSourceCoverage()

  /** Shares progressive snapshots, cancellation and stale-frame protection
   * with the other search surfaces through useSourceSearch. */
  async function search(payload: { q: string, sources: string[] }): Promise<void> {
    error.value = null
    await sourceSearch.search(payload)
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

  return { sources, groups, searching, pendingSources, saving, error, breakdowns, breakdownSnapshots, loadSources, resetSearch, search, loadBreakdowns, refreshBreakdown, batchAddProviders }
}
