/**
 * useMatchSource — data layer for the Series-Detail "Add a source" dialog: the
 * inverse of removing a source. Lets the owner search across every Suwayomi
 * source for an ALREADY-imported series (by title, not by disk path), gather
 * one or more candidates (via the shared `useSourceConfigure` tray/Configure
 * flow), and attach them all in one batch call.
 *
 * search({q, sources}) reuses the SAME cross-source `GET /api/search` endpoint
 * (and the shared `mapGroup` mapper) the Import/Adopt wizard uses — the backend
 * returns the identical SearchGroup/SearchCandidate DTO either way (§2 DRY: no
 * second mapper for the same shape). `sources` is an optional list of source
 * IDs to restrict the search to (from the `SourceFilterChips` row); an empty
 * list searches every source, matching `useImport.search`.
 *
 * loadSources() lazily fetches the `GET /api/sources` list (mapped via the same
 * 1:1 `mapSource` as `useImport`) the first time the dialog opens — guarded so
 * it fetches at most once — to populate the filter chips.
 *
 * loadBreakdowns(candidates) is copied from `useScanLibrary.loadBreakdowns`
 * (same cache/in-flight-guard/parallel-fetch shape, §2 DRY): fetches the
 * per-scanlator chapter-coverage breakdown for each given (source, mangaId)
 * pair, caching by `source:mangaId` — an absent key = not yet fetched, `null`
 * = a request-level failure (the composable falls back to a single unsplit
 * row).
 *
 * GAP-140: the breakdown endpoint is a persisted, asynchronously-computed
 * snapshot — `pending`/`ready`/`failed` — announced over SSE as
 * `imports.coverage.done` once a background walk finishes. This composable
 * tracks that lifecycle the same way `useScanLibrary.ts` does:
 * `breakdownSnapshots` (keyed identically to `breakdowns`) carries
 * `status`/`computedAt`/`error`, and `breakdowns.value[key]` is written from
 * `res.data.scanlators` UNCONDITIONALLY (a `pending`/`failed` snapshot's
 * `scanlators` is just an empty array) — never collapsed to `null` for
 * anything but a genuine request-level failure.
 *
 * ⚠ RESOLVING THE "PERMANENT CACHE" PROBLEM: `breakdowns`/`breakdownSnapshots`
 * still never EXPIRE an entry — but a `pending` entry is not stuck, because a
 * fresh fetch OVERWRITES the existing key rather than being skipped by
 * `loadBreakdowns`'s cache guard. This composable subscribes to
 * `imports.coverage.done` (via the shared `useProgressStream`, the SAME
 * EventSource every other SSE consumer uses) and, on a match by (sourceId,
 * mangaUrl) — the event's own identity, which the mangaId-keyed cache key
 * cannot be reverse-derived from — re-fetches that one entry in place, so a
 * row left on "Computing coverage…" updates itself the moment the background
 * walk lands. `refreshBreakdown` is the owner-triggered counterpart: it
 * forces the same overwrite via `?refresh=true`.
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
import { onUnmounted, ref } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import { useProgressStream } from '~/composables/useProgressStream'
import { mapCoverageSnapshot, mapGroup, mapScanlatorCoverage } from '~/composables/importMappers'
import type { ProviderRef } from '~/composables/useSourceConfigure'
import type { CoverageSnapshotView, ScanlatorCoverage, SearchCandidate, SearchGroup, Source } from '~/components/screens/import.types'

type SeriesDetailDTO = components['schemas']['SeriesDetail']
type SourceDTO = components['schemas']['Source']

/** Shape of the imports.coverage.done SSE payload (GAP-140) — the TERMINAL
 * report of one background per-scanlator breakdown computation. */
interface CoverageDoneEventPayload {
  sourceId?: string
  mangaUrl?: string
  status?: string
  total?: number
  error?: string
}

/**
 * Maps the `GET /api/sources` DTO onto the screen `Source` type. Re-declared
 * from `useImport` (a trivial 3-line 1:1 mapper) rather than exported+shared —
 * keeping the tiny mapper local avoids widening `useImport`'s public surface.
 */
function mapSource(dto: SourceDTO): Source {
  return { id: dto.id, name: dto.name, lang: dto.lang, degraded: dto.degraded, degradedReason: dto.degradedReason }
}

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

  // ---- breakdowns (per-scanlator coverage, Configure-stage auto-split) -------
  // Keyed by `source:mangaId`. `null` = a request-level failure (the dialog
  // falls back to a single unsplit row); an absent key = not yet attempted;
  // otherwise the mapped scanlator groups (possibly `[]` for a pending/failed
  // snapshot — see `breakdownSnapshots` for that lifecycle, GAP-140).
  const breakdowns = ref<Record<string, ScanlatorCoverage[] | null>>({})
  // The same cache's snapshot-level metadata (GAP-140) — status/computedAt/
  // error, keyed identically. Populated alongside `breakdowns` by every fetch
  // (initial, SSE-triggered, or an owner-triggered refresh).
  const breakdownSnapshots = ref<Record<string, CoverageSnapshotView>>({})
  const breakdownsInFlight = new Set<string>()
  // Maps a cache key back to the candidate coordinates that produced it —
  // imports.coverage.done identifies its subject by (sourceId, mangaUrl), not
  // the mangaId-keyed cache key, so the SSE handler needs this to resolve
  // which cached entry to refetch (mirrors useScanLibrary.ts).
  interface AddressRef { source: string, mangaId: number, url: string, addressMode?: 'unknown' | 'direct' | 'url_search', webUrl?: string }
  const breakdownRefs = new Map<string, AddressRef>()

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
  async function search(payload: { q: string, sources: string[] }): Promise<void> {
    const generation = ++searchGeneration
    searching.value = true
    error.value = null
    groups.value = []
    try {
      // Omit sources param when empty (all sources searched); join as CSV when set (mirrors `useImport.search`).
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
   * Fetches one candidate's breakdown and writes both caches. Shared by
   * `loadBreakdowns`, the `imports.coverage.done` refetch, and
   * `refreshBreakdown` below (mirrors `useScanLibrary.fetchBreakdown`).
   * `opts.refresh` threads `?refresh=true` (GAP-140 follow-up) — forces the
   * backend to bypass its `ready`/`failed`-cooldown admission guards, without
   * ever duplicating a walk already in flight (the backend's own guarantee).
   */
  async function fetchBreakdown(ref: AddressRef, opts?: { refresh?: boolean }): Promise<void> {
    const key = breakdownKey(ref.source, ref.mangaId)
    try {
      const res = await apiClient.GET('/api/sources/{sourceId}/manga/{mangaId}/breakdown', {
        params: {
          path: { sourceId: ref.source, mangaId: ref.mangaId },
          query: { url: ref.url, addressMode: ref.addressMode, webUrl: ref.webUrl, refresh: opts?.refresh ? true : undefined },
        },
      })
      if (res.error || !res.data) {
        breakdowns.value = { ...breakdowns.value, [key]: null }
        breakdownSnapshots.value = {
          ...breakdownSnapshots.value,
          [key]: { status: 'failed', computedAt: '', error: res.error ? res.error.message : 'Failed to load breakdown' },
        }
        return
      }
      breakdowns.value = { ...breakdowns.value, [key]: res.data.scanlators.map(mapScanlatorCoverage) }
      breakdownSnapshots.value = { ...breakdownSnapshots.value, [key]: mapCoverageSnapshot(res.data) }
    }
    catch {
      breakdowns.value = { ...breakdowns.value, [key]: null }
      breakdownSnapshots.value = { ...breakdownSnapshots.value, [key]: { status: 'failed', computedAt: '', error: 'Failed to load breakdown' } }
    }
  }

  /**
   * Fetches the per-scanlator breakdown for every given candidate IN
   * PARALLEL, skipping any candidate already cached (success or failure) or
   * already in flight. Never throws — a per-candidate failure caches `null`
   * and is otherwise swallowed (non-fatal; the Configure stage renders that
   * source as a single unsplit row). Mirrors `useScanLibrary.loadBreakdowns`
   * (§2 DRY — identical cache/in-flight-guard/parallel-fetch shape).
   */
  async function loadBreakdowns(candidates: SearchCandidate[]): Promise<void> {
    const toFetch = candidates.filter((c) => {
      const key = breakdownKey(c.source, c.mangaId)
      return !(key in breakdowns.value) && !breakdownsInFlight.has(key)
    })
    if (toFetch.length === 0) return
    for (const c of toFetch) {
      const key = breakdownKey(c.source, c.mangaId)
      breakdownsInFlight.add(key)
      breakdownRefs.set(key, { source: c.source, mangaId: c.mangaId, url: c.url, addressMode: c.addressMode, webUrl: c.realUrl })
    }
    await Promise.all(toFetch.map(async (c) => {
      const key = breakdownKey(c.source, c.mangaId)
      try {
        await fetchBreakdown({ source: c.source, mangaId: c.mangaId, url: c.url, addressMode: c.addressMode, webUrl: c.realUrl })
      }
      finally {
        breakdownsInFlight.delete(key)
      }
    }))
  }

  /**
   * Forces a recomputation of one already-resolved (or failed) candidate's
   * breakdown (GAP-140 follow-up, the Configure-stage row's refresh control).
   * Unlike `loadBreakdowns`, this does NOT check whether the key is already
   * cached — that guard exists to avoid re-fetching a SETTLED result, which is
   * exactly what an explicit refresh click means to override. It still takes
   * the same `breakdownsInFlight` latch, so a click landing while a fetch for
   * the same key is already resolving is a no-op rather than a second
   * concurrent request.
   */
  async function refreshBreakdown(candidate: SearchCandidate): Promise<void> {
    const key = breakdownKey(candidate.source, candidate.mangaId)
    if (breakdownsInFlight.has(key)) return
    breakdownsInFlight.add(key)
    breakdownRefs.set(key, { source: candidate.source, mangaId: candidate.mangaId, url: candidate.url, addressMode: candidate.addressMode, webUrl: candidate.realUrl })
    try {
      await fetchBreakdown({ source: candidate.source, mangaId: candidate.mangaId, url: candidate.url, addressMode: candidate.addressMode, webUrl: candidate.realUrl }, { refresh: true })
    }
    finally {
      breakdownsInFlight.delete(key)
    }
  }

  // A pending breakdown's eventual outcome arrives here, not by polling —
  // match it against every cache entry sharing this (source, url) pair (in
  // practice at most one) and re-fetch it in place. Takes the SAME
  // `breakdownsInFlight` latch `loadBreakdowns`/`refreshBreakdown` do (§2 DRY —
  // one guard, not two), so a burst of events for one pair collapses into a
  // single re-fetch.
  const { on } = useProgressStream()
  const unsubCoverageDone = on('imports.coverage.done', (data) => {
    const payload = data as CoverageDoneEventPayload
    if (!payload.sourceId || !payload.mangaUrl) return
    for (const [key, ref] of breakdownRefs.entries()) {
      if (ref.source !== payload.sourceId || ref.url !== payload.mangaUrl) continue
      if (breakdownsInFlight.has(key)) continue
      breakdownsInFlight.add(key)
      void fetchBreakdown(ref).finally(() => breakdownsInFlight.delete(key))
    }
  })

  onUnmounted(() => {
    unsubCoverageDone()
  })

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

  return { sources, groups, searching, saving, error, breakdowns, breakdownSnapshots, loadSources, search, loadBreakdowns, refreshBreakdown, batchAddProviders }
}
