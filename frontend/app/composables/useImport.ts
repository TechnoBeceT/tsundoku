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
 * (`Import.vue`). Mirrors
 * `useDiscover.loadDetails`'s on-demand cache/in-flight-guard pattern, keyed by
 * `source:mangaId`: `breakdowns` is a PERMANENT cache (both a successful result
 * — the mapped `ScanlatorCoverage[]` — and a failure — `null`, so `Import.vue`
 * falls back to a single unsplit row — are cached so a candidate is never
 * re-fetched); a per-candidate in-flight guard stops an overlapping call to
 * `loadBreakdowns` for the same candidate from firing a duplicate request. A
 * per-source failure is non-fatal: it never rejects and never touches `error`.
 */
import { ref } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import { mapGroup, mapScanlatorCoverage } from '~/composables/importMappers'
import type {
  AdoptRequest,
  ChapterInspect,
  ScanlatorCoverage,
  SearchGroup,
  Source,
} from '~/components/screens/import.types'

type SourceDTO = components['schemas']['Source']
type ChapterInspectDTO = components['schemas']['ChapterInspect']

function mapSource(dto: SourceDTO): Source {
  return { id: dto.id, name: dto.name, lang: dto.lang }
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
  // Keyed by `source:mangaId`. `null` = fetch attempted and failed (Import.vue
  // falls back to a single unsplit row); an absent key = not yet attempted.
  const breakdowns = ref<Record<string, ScanlatorCoverage[] | null>>({})
  const breakdownsInFlight = new Set<string>()

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
   * Fetches the per-scanlator breakdown for every given candidate IN PARALLEL,
   * skipping any candidate already cached (success or failure) or already
   * in flight. Never throws — a per-candidate failure caches `null` and is
   * otherwise swallowed (non-fatal; `Import.vue` renders that source as a
   * single unsplit row).
   */
  async function loadBreakdowns(candidates: { source: string, mangaId: number, url: string }[]): Promise<void> {
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
          params: {
            path: { sourceId: c.source, mangaId: c.mangaId },
            query: { url: c.url },
          },
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
    search,
    inspect,
    loadBreakdowns,
    adopt,
  }
}
