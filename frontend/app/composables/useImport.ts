/**
 * useImport — data layer for the Import / Adopt wizard (Screen G).
 *
 * On init:
 *   GET /api/sources  → sources (the filter chip list)
 *   GET /api/categories → categories (string[] of category names for the picker)
 *
 * Wizard actions:
 *   search({q, sources})  → GET /api/search?q=&sources=<csv> → searchResults
 *   inspect({source, mangaId, url}) → GET /api/sources/{sourceId}/manga/{mangaId}/chapters?url=
 *   adopt(req)            → POST /api/series → exposes newSeriesId for page navigation
 *
 * Discover hand-off: if the page is opened with ?source=&mangaId=&url=&title=
 * query params (from the Discover screen), useImport defensively reads them
 * and pre-seeds an inspect call so Stage 2 already has the chapter list. All
 * four params are optional and ignored when absent or malformed — `url` is
 * REQUIRED for the seeded inspect to fire (P2 Suwayomi-removal: the backend
 * 400s without it), so a hand-off missing it simply skips the seed.
 *
 * DTO → screen type notes (all fields map 1:1 between generated DTOs and the
 * import.types.ts screen types — explicit mappers avoid implicit DTO leakage):
 *   Source:          id / name / lang       ← Source
 *   SearchCandidate: source / sourceName / lang / mangaId / url / title /
 *                    thumbnailUrl            ← SearchCandidate
 *   SearchGroup:     title / candidates     ← SearchGroup
 *   ChapterInspect:  number / name          ← ChapterInspect
 *
 * loadBreakdowns(candidates) fetches the per-scanlator chapter-coverage
 * breakdown (`GET /api/sources/{sourceId}/manga/{mangaId}/breakdown?url=`) for
 * each given (source, mangaId, url) candidate IN PARALLEL — powers the
 * Configure stage's auto-split of a source into per-scanlator rows
 * (`Import.vue`). Mirrors `useScanLibrary.ts`'s identical cache/in-flight-guard/
 * parallel-fetch shape (§2 DRY), keyed by `source:mangaId`: `breakdowns` is a
 * PERMANENT cache — an absent key means "not yet attempted"; once a key is
 * written (success or failure) it is never re-fetched by `loadBreakdowns`
 * again on its own. A per-candidate in-flight guard stops an overlapping call
 * from firing a duplicate request. A per-source failure is non-fatal: it
 * never rejects and never touches `error`.
 *
 * GAP-140: the breakdown endpoint is a persisted, asynchronously-computed
 * snapshot — `pending`/`ready`/`failed` — announced over SSE as
 * `imports.coverage.done` once a background walk finishes. This composable
 * now tracks that lifecycle the SAME way `useScanLibrary.ts` does:
 * `breakdownSnapshots` (keyed identically to `breakdowns`) carries
 * `status`/`computedAt`/`error`, and `breakdowns.value[key]` is written from
 * `res.data.scanlators` UNCONDITIONALLY (a `pending`/`failed` snapshot's
 * `scanlators` is just an empty array) — never collapsed to `null` for
 * anything but a genuine request-level failure. `Import.vue`'s
 * `useSourceConfigure` call reads `coverageStatus` off this cache and renders
 * the three real states instead of a blanket "Coverage unavailable".
 *
 * ⚠ RESOLVING THE "PERMANENT CACHE" PROBLEM: `breakdowns`/`breakdownSnapshots`
 * are still permanent in the sense that nothing EXPIRES an entry — but a
 * `pending` entry is not stuck, because a fresh fetch OVERWRITES the existing
 * key rather than being skipped by `loadBreakdowns`' cache guard. This
 * composable subscribes to `imports.coverage.done` (via the shared
 * `useProgressStream`, the SAME EventSource every other SSE consumer uses —
 * never a second connection) and, on a match by (sourceId, mangaUrl) — the
 * event's own identity, which the mangaId-keyed cache key cannot be
 * reverse-derived from — re-fetches that one entry in place. So a Stage-2 row
 * left on "Computing coverage…" updates itself the moment the background walk
 * lands, without the owner ever needing to leave or re-enter the wizard.
 * `refreshBreakdown` is the owner-triggered counterpart: it forces the SAME
 * overwrite via `?refresh=true`, for a `ready` snapshot whose counts have gone
 * stale or a `failed` one the owner wants to retry now instead of waiting out
 * its cooldown.
 */
import { onUnmounted, ref } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import { useProgressStream } from '~/composables/useProgressStream'
import { mapCoverageSnapshot, mapGroup, mapScanlatorCoverage } from '~/composables/importMappers'
import type {
  AdoptRequest,
  ChapterInspect,
  CoverageSnapshotView,
  ScanlatorCoverage,
  SearchCandidate,
  SearchGroup,
  Source,
} from '~/components/screens/import.types'

type SourceDTO = components['schemas']['Source']
type ChapterInspectDTO = components['schemas']['ChapterInspect']

/** Shape of the imports.coverage.done SSE payload (GAP-140) — the TERMINAL
 * report of one background per-scanlator breakdown computation. */
interface CoverageDoneEventPayload {
  sourceId?: string
  mangaUrl?: string
  status?: string
  total?: number
  error?: string
}

function mapSource(dto: SourceDTO): Source {
  return { id: dto.id, name: dto.name, lang: dto.lang, degraded: dto.degraded, degradedReason: dto.degradedReason }
}

function mapChapterInspect(dto: ChapterInspectDTO): ChapterInspect {
  return {
    number: dto.number,
    name: dto.name,
  }
}

/** Stable cache/in-flight key for one (source, mangaId) breakdown fetch. */
function breakdownKey(source: string, mangaId: number): string {
  return `${source}:${mangaId}`
}

export function useImport() {
  // ---- Discover hand-off: read query params defensively ----------------------
  const route = useRoute()
  const rawSource = route.query.source
  const rawMangaId = route.query.mangaId
  const rawUrl = route.query.url

  // Guard: values can be string | string[] | undefined — only accept plain strings.
  const seedSource: string | null = typeof rawSource === 'string' ? rawSource : null
  const seedMangaIdNum = typeof rawMangaId === 'string' ? Number(rawMangaId) : Number.NaN
  const seedMangaId: number | null = Number.isNaN(seedMangaIdNum) ? null : seedMangaIdNum
  // Required for the seeded inspect() call — no fallback resolution by mangaId
  // alone (P2 Suwayomi-removal), so a hand-off without it simply skips the seed.
  const seedUrl: string | null = typeof rawUrl === 'string' && rawUrl !== '' ? rawUrl : null

  // ---- Wizard state ----------------------------------------------------------
  const sources = ref<Source[]>([])
  const categories = ref<string[]>([])
  const searchResults = ref<SearchGroup[]>([])
  const searching = ref(false)
  const searched = ref(false)
  const inspectChapters = ref<ChapterInspect[] | null>(null)
  const adopting = ref(false)
  const error = ref('')
  /** Set on a successful adopt; the page watches and navigates to /series/{id}. */
  const newSeriesId = ref<string | null>(null)
  /** Monotonic request-generation counter for `search()`'s stale-response guard (mirrors useMatchSource/useScanLibrary). */
  let searchGeneration = 0

  // ---- breakdowns (per-scanlator coverage, Configure stage auto-split) -------
  // Keyed by `source:mangaId`. `null` = a request-level failure (Import.vue
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
  const breakdownRefs = new Map<string, { source: string, mangaId: number, url: string }>()

  // ---- Init: load sources + categories in parallel ---------------------------
  async function loadInitial(): Promise<void> {
    const [srcRes, catRes] = await Promise.all([
      apiClient.GET('/api/sources'),
      apiClient.GET('/api/categories'),
    ])
    if (srcRes.data) {
      sources.value = srcRes.data.map(mapSource)
    }
    if (catRes.data) {
      categories.value = catRes.data.map((c) => c.name)
    }
  }

  // ---- search ----------------------------------------------------------------
  /**
   * Cross-source title search. Captures its own generation and clears
   * `searchResults`/`error` immediately (so a re-search never shows stale
   * results while in flight, and a failed re-search doesn't leave the
   * PREVIOUS query's results displayed as if they belonged to the new one);
   * the eventual success or failure is only written back to the shared
   * `searchResults`/`error` refs if this call is still the most recently
   * started one — a superseded response is discarded. `searched` is a
   * monotonic "has ever searched" flag and stays unconditional.
   */
  async function search(payload: { q: string; sources: string[] }): Promise<void> {
    const generation = ++searchGeneration
    searching.value = true
    error.value = ''
    searchResults.value = []
    try {
      // Omit sources param when empty (all sources searched); join as CSV when set.
      const query: { q: string; sources?: string } = { q: payload.q }
      if (payload.sources.length > 0) {
        query.sources = payload.sources.join(',')
      }
      const res = await apiClient.GET('/api/search', { params: { query } })
      if (res.error || !res.data) {
        throw new Error(res.error ? res.error.message : 'Search failed')
      }
      const mapped = res.data.map(mapGroup)
      if (generation === searchGeneration) searchResults.value = mapped
    }
    catch (e) {
      const message = e instanceof Error ? e.message : 'Search failed'
      if (generation === searchGeneration) error.value = message
    }
    finally {
      if (generation === searchGeneration) searching.value = false
      // `searched` flips true on first completed search and stays true —
      // monotonic, so it stays unconditional even for a superseded response.
      searched.value = true
    }
  }

  // ---- inspect ---------------------------------------------------------------
  async function inspect(payload: { source: string; mangaId: number; url: string }): Promise<void> {
    error.value = ''
    // Reset so the Import component shows its "loading" state until data arrives.
    inspectChapters.value = null
    try {
      const res = await apiClient.GET('/api/sources/{sourceId}/manga/{mangaId}/chapters', {
        params: {
          path: { sourceId: payload.source, mangaId: payload.mangaId },
          query: { url: payload.url },
        },
      })
      if (res.error || !res.data) {
        throw new Error(res.error ? res.error.message : 'Failed to load chapters')
      }
      inspectChapters.value = res.data.map(mapChapterInspect)
    }
    catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to load chapters'
    }
  }

  // ---- loadBreakdowns ----------------------------------------------------------
  /**
   * Fetches one candidate's breakdown and writes both caches. Shared by
   * `loadBreakdowns`, the `imports.coverage.done` refetch, and
   * `refreshBreakdown` below (mirrors `useScanLibrary.fetchBreakdown`).
   * `opts.refresh` threads `?refresh=true` (GAP-140 follow-up) — forces the
   * backend to bypass its `ready`/`failed`-cooldown admission guards, without
   * ever duplicating a walk already in flight (the backend's own guarantee).
   */
  async function fetchBreakdown(ref: { source: string, mangaId: number, url: string }, opts?: { refresh?: boolean }): Promise<void> {
    const key = breakdownKey(ref.source, ref.mangaId)
    try {
      const res = await apiClient.GET('/api/sources/{sourceId}/manga/{mangaId}/breakdown', {
        params: {
          path: { sourceId: ref.source, mangaId: ref.mangaId },
          query: { url: ref.url, refresh: opts?.refresh ? true : undefined },
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
   * Fetches the per-scanlator breakdown for every given candidate IN PARALLEL,
   * skipping any candidate already cached (success or failure) or already
   * in flight. Never throws — a per-candidate failure caches `null` and is
   * otherwise swallowed (non-fatal; `Import.vue` renders that source as a
   * single unsplit row).
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
      breakdownRefs.set(key, { source: c.source, mangaId: c.mangaId, url: c.url })
    }
    await Promise.all(toFetch.map(async (c) => {
      const key = breakdownKey(c.source, c.mangaId)
      try {
        await fetchBreakdown({ source: c.source, mangaId: c.mangaId, url: c.url })
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
    breakdownRefs.set(key, { source: candidate.source, mangaId: candidate.mangaId, url: candidate.url })
    try {
      await fetchBreakdown({ source: candidate.source, mangaId: candidate.mangaId, url: candidate.url }, { refresh: true })
    }
    finally {
      breakdownsInFlight.delete(key)
    }
  }

  // A pending breakdown's eventual outcome arrives here, not by polling — match
  // it against every cache entry sharing this (source, url) pair (in practice
  // at most one) and re-fetch it in place. Takes the SAME `breakdownsInFlight`
  // latch `loadBreakdowns`/`refreshBreakdown` do (§2 DRY — one guard, not two),
  // so a burst of events for one pair collapses into a single re-fetch.
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

  // ---- adopt -----------------------------------------------------------------
  async function adopt(req: AdoptRequest): Promise<void> {
    adopting.value = true
    error.value = ''
    newSeriesId.value = null
    try {
      const res = await apiClient.POST('/api/series', { body: req })
      if (res.error || !res.data) {
        // Surface the backend {message} from the central error shape.
        throw new Error(res.error ? res.error.message : 'Adopt failed')
      }
      newSeriesId.value = res.data.id
    }
    catch (e) {
      error.value = e instanceof Error ? e.message : 'Adopt failed'
    }
    finally {
      adopting.value = false
    }
  }

  // ---- Bootstrap -------------------------------------------------------------
  void loadInitial()

  // Optionally seed an inspect from the Discover hand-off.
  if (seedSource !== null && seedMangaId !== null && seedUrl !== null) {
    void inspect({ source: seedSource, mangaId: seedMangaId, url: seedUrl })
  }

  return {
    sources,
    categories,
    searchResults,
    searching,
    searched,
    inspectChapters,
    adopting,
    error,
    newSeriesId,
    breakdowns,
    breakdownSnapshots,
    search,
    inspect,
    loadBreakdowns,
    refreshBreakdown,
    adopt,
  }
}
