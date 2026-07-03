/**
 * useImport — data layer for the Import / Adopt wizard (Screen G).
 *
 * On init:
 *   GET /api/sources  → sources (the filter chip list)
 *   GET /api/categories → categories (string[] of category names for the picker)
 *
 * Wizard actions:
 *   search({q, sources})  → GET /api/search?q=&sources=<csv> → searchResults
 *   inspect({source, mangaId}) → GET /api/sources/{sourceId}/manga/{mangaId}/chapters
 *   adopt(req)            → POST /api/series → exposes newSeriesId for page navigation
 *
 * Discover hand-off: if the page is opened with ?source=&mangaId=&title= query
 * params (from the Discover screen), useImport defensively reads them and
 * pre-seeds an inspect call so Stage 2 already has the chapter list. All three
 * params are optional and ignored when absent or malformed.
 *
 * DTO → screen type notes (all fields map 1:1 between generated DTOs and the
 * import.types.ts screen types — explicit mappers avoid implicit DTO leakage):
 *   Source:          id / name / lang       ← Source
 *   SearchCandidate: source / sourceName / lang / mangaId / title / thumbnailUrl
 *                    ← SearchCandidate (url is in DTO but not in screen type)
 *   SearchGroup:     title / candidates     ← SearchGroup
 *   ChapterInspect:  number / name          ← ChapterInspect
 */
import { ref } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import { mapGroup } from '~/composables/importMappers'
import type {
  AdoptRequest,
  ChapterInspect,
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

export function useImport() {
  // ---- Discover hand-off: read query params defensively ----------------------
  const route = useRoute()
  const rawSource = route.query.source
  const rawMangaId = route.query.mangaId

  // Guard: values can be string | string[] | undefined — only accept plain strings.
  const seedSource: string | null = typeof rawSource === 'string' ? rawSource : null
  const seedMangaIdNum = typeof rawMangaId === 'string' ? Number(rawMangaId) : Number.NaN
  const seedMangaId: number | null = Number.isNaN(seedMangaIdNum) ? null : seedMangaIdNum

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
  async function inspect(payload: { source: string; mangaId: number }): Promise<void> {
    error.value = ''
    // Reset so the Import component shows its "loading" state until data arrives.
    inspectChapters.value = null
    try {
      const res = await apiClient.GET('/api/sources/{sourceId}/manga/{mangaId}/chapters', {
        params: { path: { sourceId: payload.source, mangaId: payload.mangaId } },
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
  if (seedSource !== null && seedMangaId !== null) {
    void inspect({ source: seedSource, mangaId: seedMangaId })
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
    search,
    inspect,
    adopt,
  }
}
