/**
 * useSeriesDetail — data layer for the series detail screen.
 *
 * Fetches GET /api/series/{id} and GET /api/categories in parallel, maps the
 * generated backend DTOs onto the screen's SeriesDetail type and a
 * categoryOptions string[] (category NAMES for the recategorize select), and
 * exposes all mutations with the §16 busy/error/refetch pattern.
 *
 * Category name→id resolution: the PATCH /api/series/{id}/category body needs
 * a UUID, but the screen emits a NAME. A categoryMap (name→id) is built from
 * the /api/categories response and consulted by setCategory — unknown names set
 * error and bail early.
 *
 * DTO→screen field notes:
 *   title         ← dto.displayName        (same as useLibrary)
 *   coverUrl      ← dto.coverUrl           (already the proxy path; pass-through)
 *   metadataProviderId ← providers.find(isMetadataSource)?.id ?? null
 *   Chapter.state is typed as string in the DTO → cast to ChapterState
 *   Provider.newestChapterAt / lastSyncedAt are optional in DTO → ?? null
 *
 * providerCoverage / loadProviderCoverage: the Sources panel's LAZY per-source
 * coverage (Slice P follow-up) — see the doc comments on the two below. Fetched
 * ONLY on a row's "Show coverage" click, never as part of `refresh()`.
 */
import { ref } from 'vue'
import type { Ref } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import { mapScanlatorCoverage } from '~/composables/importMappers'
import type { ScanlatorCoverage } from '~/components/screens/import.types'
import type { SeriesDetail, Chapter, ChapterState, Provider } from '~/components/screens/seriesDetail.types'

type SeriesDetailDTO = components['schemas']['SeriesDetail']
type ChapterDTO = components['schemas']['Chapter']
type ProviderDTO = components['schemas']['Provider']

/**
 * MatchDiskProviderPayload — the body of `POST
 * /api/series/{id}/providers/{providerId}/match` (the `AddProviderRequest`
 * shape): the real Suwayomi source/scanlator to attribute an unlinked
 * disk-origin provider's existing chapters to, plus the importance to assign
 * the newly-linked provider.
 */
export interface MatchDiskProviderPayload {
  /** Suwayomi source ID to attach. */
  source: string
  /** Suwayomi-internal manga identifier within that source. */
  mangaId: number
  /** Priority to assign the newly-linked provider (higher = preferred). */
  importance: number
  /** Scanlation group to track; "" (or omitted) = all chapters from the source. */
  scanlator?: string
}

function mapChapter(dto: ChapterDTO): Chapter {
  return {
    chapterKey: dto.chapterKey,
    number: dto.number,
    name: dto.name,
    state: dto.state as ChapterState,
    filename: dto.filename,
    pageCount: dto.pageCount,
  }
}

function mapProvider(dto: ProviderDTO): Provider {
  return {
    id: dto.id,
    provider: dto.provider,
    providerName: dto.providerName,
    linked: dto.linked,
    mangaId: dto.mangaId,
    chapterCount: dto.chapterCount,
    scanlator: dto.scanlator,
    language: dto.language,
    importance: dto.importance,
    health: dto.health,
    chaptersBehind: dto.chaptersBehind,
    newestChapterAt: dto.newestChapterAt ?? null,
    lastSyncedAt: dto.lastSyncedAt ?? null,
    lastError: dto.lastError,
  }
}

function mapDetail(dto: SeriesDetailDTO): SeriesDetail {
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
    chapters: dto.chapters.map(mapChapter),
    providers: dto.providers.map(mapProvider),
    metadataProviderId: dto.providers.find((p) => p.isMetadataSource)?.id ?? null,
  }
}

export function useSeriesDetail(id: string) {
  const series = ref<SeriesDetail | null>(null)
  const categoryOptions = ref<string[]>([])
  const categoryMap = ref(new Map<string, string>())
  const pending = ref(false)
  const error = ref<string | null>(null)
  const saving = ref(false)
  const deleteBusy = ref(false)
  const removeBusy = ref(false)
  const matchBusy = ref(false)
  const dedupBusy = ref(false)
  const dedupeFilesBusy = ref(false)
  const dedupMessage = ref<string | null>(null)

  // ---- providerCoverage (lazy per-source coverage, Sources panel) ------------
  // Keyed by SeriesProvider `id`. An absent key = never fetched (the row shows
  // "Show coverage"); `null` = fetch attempted and failed (row shows "Coverage
  // unavailable"); an array = the loaded per-scanlator breakdown. NEVER fetched
  // eagerly — `loadProviderCoverage` is called ONLY from the row's own
  // "Show coverage" user action (see pages/series/[id].vue), never from
  // `refresh()`/onMounted. This is deliberate anti-IP-block politeness: the
  // `/breakdown` endpoint does a LIVE source fetch, and an eager fetch across
  // every tracked source on every Series-Detail visit is exactly the traffic
  // that got the owner Cloudflare-IP-blocked.
  const providerCoverage = ref<Record<string, ScanlatorCoverage[] | null>>({})
  const providerCoverageInFlight = new Set<string>()

  async function refresh(): Promise<void> {
    pending.value = true
    error.value = null
    try {
      const [s, c] = await Promise.all([
        apiClient.GET('/api/series/{id}', { params: { path: { id } } }),
        apiClient.GET('/api/categories'),
      ])
      if (s.error || !s.data) throw new Error('Failed to load series')
      series.value = mapDetail(s.data)
      if (c.data) {
        const map = new Map<string, string>()
        const names: string[] = []
        for (const cat of c.data) {
          map.set(cat.name, cat.id)
          names.push(cat.name)
        }
        categoryMap.value = map
        categoryOptions.value = names
      }
    }
    catch (err) {
      error.value = err instanceof Error ? err.message : 'Failed to load series'
    }
    finally {
      pending.value = false
    }
  }

  /**
   * Lazily fetches the per-scanlator coverage breakdown for ONE source row —
   * the SOLE call site is the row's "Show coverage" click, never `refresh()`
   * or `onMounted` (see the `providerCoverage` doc comment above). No-ops for
   * an unlinked disk provider (`mangaId <= 0` — nothing to fetch) or a
   * provider whose coverage is already cached (loaded or previously failed)
   * or already in flight. Never throws — a failure caches `null` under the
   * provider's id so the row renders "Coverage unavailable". Reuses the same
   * `GET /api/sources/{sourceId}/manga/{mangaId}/breakdown` fetch shape as
   * `useImport.loadBreakdowns`/`useMatchSource.loadBreakdowns` (§2 DRY — same
   * endpoint, same cache/in-flight-guard pattern), just keyed by the
   * SeriesProvider `id` instead of `source:mangaId` since this is a single
   * already-resolved row, not a set of adopt candidates.
   */
  const loadProviderCoverage = async (provider: Provider): Promise<void> => {
    if (provider.mangaId <= 0) return
    if (provider.id in providerCoverage.value || providerCoverageInFlight.has(provider.id)) return
    providerCoverageInFlight.add(provider.id)
    try {
      const res = await apiClient.GET('/api/sources/{sourceId}/manga/{mangaId}/breakdown', {
        params: { path: { sourceId: provider.provider, mangaId: provider.mangaId } },
      })
      providerCoverage.value = {
        ...providerCoverage.value,
        [provider.id]: res.error || !res.data ? null : res.data.scanlators.map(mapScanlatorCoverage),
      }
    }
    catch {
      providerCoverage.value = { ...providerCoverage.value, [provider.id]: null }
    }
    finally {
      providerCoverageInFlight.delete(provider.id)
    }
  }

  /**
   * Shared mutation wrapper: set busy, clear error, call fn, check result.error,
   * then call onSuccess (default: refresh). Any throw sets error.value.
   * busyRef defaults to `saving`; pass deleteBusy/removeBusy for those actions.
   * onSuccess defaults to refresh; pass a nav callback for deleteSeries.
   */
  async function mutate(
    fn: () => Promise<{ error?: unknown }>,
    busyRef: Ref<boolean> = saving,
    onSuccess: () => void | Promise<void> = refresh,
  ): Promise<void> {
    busyRef.value = true
    error.value = null
    try {
      const result = await fn()
      if (result.error) throw new Error('Update failed')
      await onSuccess()
    }
    catch (err) {
      error.value = err instanceof Error ? err.message : 'Update failed'
    }
    finally {
      busyRef.value = false
    }
  }

  const setMonitored = (monitored: boolean): Promise<void> =>
    mutate(() => apiClient.PATCH('/api/series/{id}/monitored', { params: { path: { id } }, body: { monitored } }))

  const setCompleted = (completed: boolean): Promise<void> =>
    mutate(() => apiClient.PATCH('/api/series/{id}/completed', { params: { path: { id } }, body: { completed } }))

  const setCategory = async (name: string): Promise<void> => {
    const categoryId = categoryMap.value.get(name)
    if (!categoryId) {
      error.value = `Unknown category: ${name}`
      return
    }
    return mutate(() => apiClient.PATCH('/api/series/{id}/category', { params: { path: { id } }, body: { categoryId } }))
  }

  const reorderProviders = (providers: { id: string, importance: number }[]): Promise<void> =>
    mutate(() => apiClient.PATCH('/api/series/{id}/providers', { params: { path: { id } }, body: { providers } }))

  const removeSource = (providerId: string): Promise<void> =>
    mutate(
      () => apiClient.DELETE('/api/series/{id}/providers/{providerId}', { params: { path: { id, providerId } } }),
      removeBusy,
    )

  const chooseMetadataSource = (providerId: string): Promise<void> =>
    mutate(() => apiClient.PATCH('/api/series/{id}/metadata-source', { params: { path: { id } }, body: { providerId } }))

  const deleteSeries = (deleteFiles: boolean): Promise<void> =>
    mutate(
      () => apiClient.DELETE('/api/series/{id}', { params: { path: { id }, query: { deleteFiles } } }),
      deleteBusy,
      () => { void navigateTo('/') },
    )

  /**
   * Matches a disk-origin (unlinked) provider to a real Suwayomi source: the
   * backend attaches the source, re-points every chapter it already satisfies
   * onto it (no re-download), and drops the now-redundant disk-origin row.
   * Unlike `mutate`'s default onSuccess (a full `refresh()` round-trip) this
   * reseeds `series` DIRECTLY from the match response — the endpoint already
   * returns the authoritative, fully-refreshed SeriesDetail, so a second
   * fetch would be a wasted round-trip (§16 mutate-reseeds-from-response).
   * Resolves true on success / false on failure (error surfaced via `error`,
   * never swallowed) so the dialog knows whether to close.
   */
  const matchDiskProvider = async (providerId: string, payload: MatchDiskProviderPayload): Promise<boolean> => {
    matchBusy.value = true
    error.value = null
    try {
      const res = await apiClient.POST('/api/series/{id}/providers/{providerId}/match', {
        params: { path: { id, providerId } },
        body: payload,
      })
      if (res.error || !res.data) throw new Error(res.error ? res.error.message : 'Match failed')
      series.value = mapDetail(res.data)
      return true
    }
    catch (err) {
      error.value = err instanceof Error ? err.message : 'Match failed'
      return false
    }
    finally {
      matchBusy.value = false
    }
  }

  /**
   * Folds every already-drifted disk/live duplicate source pair on this series
   * into one row (no re-download) via POST /api/series/{id}/providers/dedup.
   * Reseeds `series` DIRECTLY from the authoritative response (§16, like
   * matchDiskProvider), and surfaces the merged/skipped counts in dedupMessage.
   * Errors set `error`, never swallowed; `series` is left untouched on failure.
   */
  const dedupProviders = async (): Promise<void> => {
    dedupBusy.value = true
    error.value = null
    dedupMessage.value = null
    try {
      const res = await apiClient.POST('/api/series/{id}/providers/dedup', { params: { path: { id } } })
      if (res.error || !res.data) throw new Error(res.error ? res.error.message : 'Dedup failed')
      series.value = mapDetail(res.data.series)
      const { merged, skipped } = res.data
      dedupMessage.value = skipped > 0
        ? `Merged ${merged} duplicate source${merged === 1 ? '' : 's'}, skipped ${skipped}`
        : `Merged ${merged} duplicate source${merged === 1 ? '' : 's'}`
    }
    catch (err) {
      error.value = err instanceof Error ? err.message : 'Dedup failed'
    }
    finally {
      dedupBusy.value = false
    }
  }

  /**
   * Sweeps this series' orphan/duplicate CBZ files (any .cbz that is not a
   * chapter's current winning filename) via POST /api/series/{id}/dedupe-files.
   * A pure on-disk sweep — NO DB/series change — so it does not reseed; it only
   * reports how many files were removed in dedupMessage. Errors set `error`.
   */
  const dedupeFiles = async (): Promise<void> => {
    dedupeFilesBusy.value = true
    error.value = null
    dedupMessage.value = null
    try {
      const res = await apiClient.POST('/api/series/{id}/dedupe-files', { params: { path: { id } } })
      if (res.error || !res.data) throw new Error(res.error ? res.error.message : 'Dedupe files failed')
      const { removed } = res.data
      dedupMessage.value = `Removed ${removed} duplicate file${removed === 1 ? '' : 's'}`
    }
    catch (err) {
      error.value = err instanceof Error ? err.message : 'Dedupe files failed'
    }
    finally {
      dedupeFilesBusy.value = false
    }
  }

  const dismissError = (): void => { error.value = null }

  /**
   * Reseeds `series` directly from an authoritative SeriesDetail returned by
   * a mutation the PARENT drove through its own composable — e.g.
   * `useMatchSource.batchAddProviders` (Slice P's "Add a source" dialog).
   * Mirrors `matchDiskProvider`'s own direct reseed: the endpoint already
   * returns the fresh, fully-refreshed SeriesDetail, so a second
   * `GET /api/series/{id}` round-trip would be wasted (§16
   * mutate-reseeds-from-response).
   */
  const reseed = (dto: SeriesDetailDTO): void => {
    series.value = mapDetail(dto)
  }

  void refresh()

  return {
    series,
    categoryOptions,
    pending,
    error,
    saving,
    deleteBusy,
    removeBusy,
    matchBusy,
    dedupBusy,
    dedupeFilesBusy,
    dedupMessage,
    providerCoverage,
    loadProviderCoverage,
    setMonitored,
    setCompleted,
    setCategory,
    reorderProviders,
    removeSource,
    chooseMetadataSource,
    deleteSeries,
    matchDiskProvider,
    dedupProviders,
    dedupeFiles,
    dismissError,
    refresh,
    reseed,
  }
}
