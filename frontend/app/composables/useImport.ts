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
import type {
  AdoptRequest,
  ChapterInspect,
  SearchCandidate,
  SearchGroup,
  Source,
} from '~/components/screens/import.types'

type SourceDTO = components['schemas']['Source']
type SearchCandidateDTO = components['schemas']['SearchCandidate']
type SearchGroupDTO = components['schemas']['SearchGroup']
type ChapterInspectDTO = components['schemas']['ChapterInspect']

function mapSource(dto: SourceDTO): Source {
  return { id: dto.id, name: dto.name, lang: dto.lang }
}

function mapCandidate(dto: SearchCandidateDTO): SearchCandidate {
  return {
    source: dto.source,
    sourceName: dto.sourceName,
    lang: dto.lang,
    mangaId: dto.mangaId,
    title: dto.title,
    thumbnailUrl: dto.thumbnailUrl,
  }
}

function mapGroup(dto: SearchGroupDTO): SearchGroup {
  return {
    title: dto.title,
    candidates: dto.candidates.map(mapCandidate),
  }
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
  const rawTitle = route.query.title

  // Guard: values can be string | string[] | undefined — only accept plain strings.
  const seedSource: string | null = typeof rawSource === 'string' ? rawSource : null
  const seedMangaIdNum = typeof rawMangaId === 'string' ? Number(rawMangaId) : Number.NaN
  const seedMangaId: number | null = Number.isNaN(seedMangaIdNum) ? null : seedMangaIdNum
  // Exposed so the page can pre-populate the title field if desired.
  const seedTitle: string | null = typeof rawTitle === 'string' ? rawTitle : null

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
  async function search(payload: { q: string; sources: string[] }): Promise<void> {
    searching.value = true
    error.value = ''
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
      searchResults.value = res.data.map(mapGroup)
    }
    catch (e) {
      error.value = e instanceof Error ? e.message : 'Search failed'
    }
    finally {
      searching.value = false
      // `searched` flips true on first completed search and stays true.
      searched.value = true
    }
  }

  // ---- inspect ---------------------------------------------------------------
  async function inspect(payload: { source: string; mangaId: number }): Promise<void> {
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
    seedTitle,
    search,
    inspect,
    adopt,
  }
}
