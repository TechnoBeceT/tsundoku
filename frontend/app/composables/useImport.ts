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
 * Coverage cache, snapshot state, loadBreakdowns and refreshBreakdown delegate
 * to useSourceCoverage. Each wizard owns its exact source/address cache; the
 * shared helper preserves pending/ready/failed snapshots, completion-event
 * refetches and explicit refresh without touching the wizard's error state.
 */
import { useSourceSearch } from './useSourceSearch'
import { useSourceCoverage } from './useSourceCoverage'
import { ref, watch } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import type {
  AdoptRequest,
  ChapterInspect,
  Source,
} from '~/components/screens/import.types'

type SourceDTO = components['schemas']['Source']
type ChapterInspectDTO = components['schemas']['ChapterInspect']

function mapSource(dto: SourceDTO): Source {
  return { id: dto.id, name: dto.name, lang: dto.lang, degraded: dto.degraded, degradedReason: dto.degradedReason }
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
  const rawUrl = route.query.url
  const rawAddressMode = route.query.addressMode
  const rawWebUrl = route.query.webUrl

  // Guard: values can be string | string[] | undefined — only accept plain strings.
  const seedSource: string | null = typeof rawSource === 'string' ? rawSource : null
  const seedMangaIdNum = typeof rawMangaId === 'string' ? Number(rawMangaId) : Number.NaN
  const seedMangaId: number | null = Number.isNaN(seedMangaIdNum) ? null : seedMangaIdNum
  // Required for the seeded inspect() call — no fallback resolution by mangaId
  // alone (P2 Suwayomi-removal), so a hand-off without it simply skips the seed.
  const seedUrl: string | null = typeof rawUrl === 'string' && rawUrl !== '' ? rawUrl : null
  const seedAddressMode = rawAddressMode === 'direct' || rawAddressMode === 'url_search' ? rawAddressMode : 'unknown'
  const seedWebUrl = typeof rawWebUrl === 'string' ? rawWebUrl : undefined

  // ---- Wizard state ----------------------------------------------------------
  const sources = ref<Source[]>([])
  const categories = ref<string[]>([])
  const sourceSearch = useSourceSearch()
  const searchResults = sourceSearch.groups
  const pendingSources = sourceSearch.pendingSources
  const searching = sourceSearch.searching
  const searched = ref(false)
  const inspectChapters = ref<ChapterInspect[] | null>(null)
  const adopting = ref(false)
  const error = ref('')
  /** Set on a successful adopt; the page watches and navigates to /series/{id}. */
  const newSeriesId = ref<string | null>(null)
  // Search failures share this surface with the domain actions below.
  watch(sourceSearch.error, value => { error.value = value ?? '' })

  const { breakdowns, breakdownSnapshots, loadBreakdowns, refreshBreakdown } = useSourceCoverage()

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
  /** Shares progressive snapshots, cancellation and stale-frame protection
   * with the other search surfaces through useSourceSearch. */
  async function search(payload: { q: string, sources: string[] }): Promise<void> {
    error.value = ''
    await sourceSearch.search(payload)
    searched.value = true
  }

  interface AddressRef { source: string, mangaId: number, url: string, addressMode?: 'unknown' | 'direct' | 'url_search', webUrl?: string }

  // ---- inspect ---------------------------------------------------------------
  async function inspect(payload: AddressRef): Promise<void> {
    error.value = ''
    // Reset so the Import component shows its "loading" state until data arrives.
    inspectChapters.value = null
    try {
      const res = await apiClient.GET('/api/sources/{sourceId}/manga/{mangaId}/chapters', {
        params: {
          path: { sourceId: payload.source, mangaId: payload.mangaId },
          query: { url: payload.url, addressMode: payload.addressMode, webUrl: payload.webUrl },
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
    void inspect({ source: seedSource, mangaId: seedMangaId, url: seedUrl, addressMode: seedAddressMode, webUrl: seedWebUrl })
  }

  return {
    sources,
    categories,
    searchResults,
    searching,
    pendingSources,
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
