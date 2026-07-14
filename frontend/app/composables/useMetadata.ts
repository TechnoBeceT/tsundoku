/**
 * useMetadata — data layer for the native metadata engine (spec
 * `spec/metadata-engine-phase1`), keyed to ONE series: the cross-provider
 * "Identify" search + confirm, and the cover-candidate gallery + pick.
 *
 * Feeds `MetadataIdentifyModal` (search/identify) and `CoverPickerModal`
 * (loadCovers/setCover) on the series detail page. Each action owns its own
 * busy/error ref (never a single shared flag) so the two modals — and a
 * search vs. a confirm within the SAME modal — never fight over one spinner.
 *
 * `identify`/`setCover` return the RAW `SeriesDetailDTO` on success (mirrors
 * `useMatchSource.batchAddProviders`, §2 DRY) rather than re-implementing the
 * DTO→screen mapping here — the caller reseeds via `useSeriesDetail.reseed`,
 * so `mapDetail`'s logic lives in exactly one place. `null` on failure, with
 * the failure surfaced via the matching `*Error` ref (§16 — never swallowed).
 *
 * `search`/`loadCovers` populate `candidates`/`coverCandidates` directly
 * (mapped to the screen types) since nothing downstream needs the raw DTO —
 * the modals render straight off these refs.
 */
import { ref } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import type { CoverCandidate, MetadataCandidate } from '~/components/screens/seriesDetail.types'

type SeriesDetailDTO = components['schemas']['SeriesDetail']
type MetadataSearchResultDTO = components['schemas']['MetadataSearchResult']
type CoverCandidateDTO = components['schemas']['CoverCandidate']

/** Provider Key() → a human display label for the candidate/cover badges. Falls back to the raw key for a provider the fleet hasn't labelled yet (spec §9 — the provider set may grow). */
const PROVIDER_LABELS: Record<string, string> = {
  anilist: 'AniList',
  mangadex: 'MangaDex',
  mangaupdates: 'MangaUpdates',
  mal: 'MAL',
  kitsu: 'Kitsu',
}

function providerLabel(key: string): string {
  return PROVIDER_LABELS[key] ?? key
}

function mapMetadataCandidate(dto: MetadataSearchResultDTO): MetadataCandidate {
  return {
    id: `${dto.provider}:${dto.remoteId}`,
    provider: providerLabel(dto.provider),
    providerKey: dto.provider,
    remoteId: dto.remoteId,
    title: dto.title,
    coverUrl: dto.coverUrl,
    year: dto.year > 0 ? dto.year : undefined,
  }
}

/**
 * Maps one backend CoverCandidateDTO to its screen shape.
 *
 * `id` MUST include `coverUrl` (not just `sourceKind:sourceRef`): a single
 * metadata provider search can surface MULTIPLE covers (one per hit) that all
 * share the same provider `sourceRef` — `sourceKind:sourceRef` alone would
 * give every one of that provider's tiles the identical id, so
 * `CoverPickerModal`'s `c.id === selectedId` single-select would mark (and
 * confirm) ALL of them at once instead of just the clicked tile. `coverUrl`
 * is otherwise unused as an identifier elsewhere, but each candidate's own
 * cover image IS distinct by construction (that's the whole gallery), so
 * appending it is the natural disambiguator — and it stays reconstructible
 * from `series.coverSource.remoteUrl` (see `currentCoverId` on the series
 * detail page), which round-trips the exact `coverUrl` a pick was made with.
 */
function mapCoverCandidate(dto: CoverCandidateDTO): CoverCandidate {
  return {
    id: `${dto.sourceKind}:${dto.sourceRef}:${dto.coverUrl}`,
    provider: dto.sourceKind === 'metadata' ? providerLabel(dto.label) : dto.label,
    coverUrl: dto.coverUrl,
    sourceKind: dto.sourceKind,
    sourceRef: dto.sourceRef,
  }
}

export function useMetadata(seriesId: string) {
  // ---- Search (feeds MetadataIdentifyModal's candidate grid) ----------------
  const candidates = ref<MetadataCandidate[]>([])
  const searching = ref(false)
  const searchError = ref<string | null>(null)

  /**
   * Cross-provider candidate search (GET /api/metadata/search). `providers`
   * restricts the fan-out to the given provider Key()s; omitted/empty
   * searches every registered provider. A failure clears `candidates` (never
   * leaves a stale gallery from a previous query) and sets `searchError`.
   */
  async function search(q: string, providers?: string[]): Promise<void> {
    searching.value = true
    searchError.value = null
    try {
      const query: { q: string, providers?: string } = { q }
      if (providers && providers.length > 0) query.providers = providers.join(',')
      const res = await apiClient.GET('/api/metadata/search', { params: { query } })
      if (res.error || !res.data) throw new Error(res.error ? res.error.message : 'Search failed')
      candidates.value = res.data.map(mapMetadataCandidate)
    }
    catch (err) {
      searchError.value = err instanceof Error ? err.message : 'Search failed'
      candidates.value = []
    }
    finally {
      searching.value = false
    }
  }

  // ---- Identify (the owner's confirmed match) --------------------------------
  const identifying = ref(false)
  const identifyError = ref<string | null>(null)

  /**
   * Confirms a candidate as the series' primary metadata source
   * (POST /api/series/{id}/metadata/identify). The backend anchor-then-
   * aggregates: it auto-matches every other provider by the primary's title,
   * merges, and persists — this call's response already carries the fully
   * refreshed series. Resolves the raw DTO on success (the caller reseeds
   * `useSeriesDetail`'s `series`), or null on failure (`identifyError` set).
   */
  async function identify(provider: string, remoteId: string): Promise<SeriesDetailDTO | null> {
    identifying.value = true
    identifyError.value = null
    try {
      const res = await apiClient.POST('/api/series/{id}/metadata/identify', {
        params: { path: { id: seriesId } },
        body: { provider, remoteId },
      })
      if (res.error || !res.data) throw new Error(res.error ? res.error.message : 'Identify failed')
      return res.data
    }
    catch (err) {
      identifyError.value = err instanceof Error ? err.message : 'Identify failed'
      return null
    }
    finally {
      identifying.value = false
    }
  }

  // ---- Cover gallery (feeds CoverPickerModal) --------------------------------
  const coverCandidates = ref<CoverCandidate[]>([])
  const coversLoading = ref(false)
  const coversError = ref<string | null>(null)

  /**
   * Aggregated cover-candidate gallery for this series
   * (GET /api/series/{id}/metadata/covers) — every metadata provider's cover
   * for the series' own title. A failure clears `coverCandidates` and sets
   * `coversError`; this is a background gallery load, so the modal's empty
   * state (never a page-level error banner) is what the owner sees.
   */
  async function loadCovers(): Promise<void> {
    coversLoading.value = true
    coversError.value = null
    try {
      const res = await apiClient.GET('/api/series/{id}/metadata/covers', { params: { path: { id: seriesId } } })
      if (res.error || !res.data) throw new Error(res.error ? res.error.message : 'Failed to load covers')
      coverCandidates.value = res.data.map(mapCoverCandidate)
    }
    catch (err) {
      coversError.value = err instanceof Error ? err.message : 'Failed to load covers'
      coverCandidates.value = []
    }
    finally {
      coversLoading.value = false
    }
  }

  // ---- Set cover (the owner's confirmed pick) --------------------------------
  const settingCover = ref(false)
  const setCoverError = ref<string | null>(null)

  /**
   * The owner's explicit cover pick (POST /api/series/{id}/cover) — fetches
   * `coverUrl`'s bytes, caches them, and records `cover_source` independently
   * of `metadata_source` (a cover pick never implies a metadata re-merge, per
   * QCAT-228). Resolves the raw DTO on success (caller reseeds), or null on
   * failure (`setCoverError` set).
   */
  async function setCover(sourceKind: string, sourceRef: string, coverUrl: string): Promise<SeriesDetailDTO | null> {
    settingCover.value = true
    setCoverError.value = null
    try {
      const res = await apiClient.POST('/api/series/{id}/cover', {
        params: { path: { id: seriesId } },
        body: { sourceKind, sourceRef, coverUrl },
      })
      if (res.error || !res.data) throw new Error(res.error ? res.error.message : 'Failed to set cover')
      return res.data
    }
    catch (err) {
      setCoverError.value = err instanceof Error ? err.message : 'Failed to set cover'
      return null
    }
    finally {
      settingCover.value = false
    }
  }

  return {
    candidates,
    searching,
    searchError,
    identifying,
    identifyError,
    coverCandidates,
    coversLoading,
    coversError,
    settingCover,
    setCoverError,
    search,
    identify,
    loadCovers,
    setCover,
  }
}
