/**
 * useDiscover — data layer for the Discover (per-source catalog browse) screen.
 *
 * On init: fetches GET /api/sources → sources picker; sets activeSource to the
 * first source's id and activeType to 'popular'; loads page 1.
 *
 * Browse: GET /api/sources/{sourceId}/browse?type={activeType}&page={n}.
 * Maps the generated SearchCandidate DTO → DiscoverCandidate (fields align
 * 1-to-1; description/genres/author/artist come straight off the DTO — a
 * backend-provided "" or [] simply renders as "no value" on the screen type;
 * inLibrary has no DTO counterpart and stays undefined).
 *
 * Pagination: loadPage(n) APPENDS each page's manga onto result.manga for the
 * standard "Load more" accumulation pattern (BrowseResult.manga is documented
 * as "accumulated, the parent appends each loaded page"). A fresh source/type
 * selection or a retry always resets to page 1 and clears existing manga.
 *
 * State:
 *   result       — accumulated BrowseResult for the active source + type
 *   sources      — all available sources (populated once at init)
 *   activeSource — ID of the currently browsed source
 *   activeType   — 'popular' | 'latest'
 *   loading      — true while a browse fetch is in flight
 *   error        — boolean: true when the latest browse fetch failed
 *
 * Methods:
 *   setSource(id) — switch source, reset to page 1, refetch
 *   setType(t)    — switch listing, reset to page 1, refetch
 *   loadPage(n)   — fetch page n; n === 1 replaces manga, n > 1 appends
 *   retry()       — retry the last attempted page (resets to page 1 on initial-
 *                   load errors; retries the failed page on load-more errors)
 */
import { ref } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import type { BrowseResult, BrowseType, DiscoverCandidate, DiscoverSource } from '~/components/screens/discover.types'

type SearchCandidateDTO = components['schemas']['SearchCandidate']
type SourceDTO = components['schemas']['Source']

const EMPTY_RESULT: BrowseResult = { manga: [], hasNextPage: false, page: 0 }

function mapSource(dto: SourceDTO): DiscoverSource {
  return { id: dto.id, name: dto.name, lang: dto.lang }
}

function mapCandidate(dto: SearchCandidateDTO): DiscoverCandidate {
  return {
    source: dto.source,
    sourceName: dto.sourceName,
    lang: dto.lang,
    mangaId: dto.mangaId,
    title: dto.title,
    thumbnailUrl: dto.thumbnailUrl,
    url: dto.url,
    description: dto.description,
    genres: dto.genres,
    author: dto.author,
    artist: dto.artist,
    // inLibrary: no DTO counterpart; left undefined.
  }
}

export function useDiscover() {
  const result = ref<BrowseResult>({ ...EMPTY_RESULT })
  const sources = ref<DiscoverSource[]>([])
  const activeSource = ref<string>('')
  const activeType = ref<BrowseType>('popular')
  const loading = ref(false)
  const error = ref(false)

  // Tracks the last attempted page so retry() can re-issue the same fetch
  // (e.g. retry page 3 without losing the already-loaded pages 1+2).
  let lastPage = 1

  async function loadPage(n: number): Promise<void> {
    if (!activeSource.value) return
    lastPage = n
    loading.value = true
    error.value = false
    try {
      const res = await apiClient.GET('/api/sources/{sourceId}/browse', {
        params: {
          path: { sourceId: activeSource.value },
          query: { type: activeType.value, page: n },
        },
      })
      if (res.error || !res.data) throw new Error('Browse failed')
      const dto = res.data
      const newManga = dto.manga.map(mapCandidate)
      result.value = {
        // Page 1 starts fresh; subsequent pages accumulate (Load more pattern).
        manga: n === 1 ? newManga : [...result.value.manga, ...newManga],
        hasNextPage: dto.hasNextPage,
        page: dto.page,
      }
    }
    catch {
      error.value = true
    }
    finally {
      loading.value = false
    }
  }

  async function init(): Promise<void> {
    const res = await apiClient.GET('/api/sources')
    if (res.error || !res.data || res.data.length === 0) return
    sources.value = res.data.map(mapSource)
    activeSource.value = sources.value[0].id
    await loadPage(1)
  }

  function setSource(id: string): void {
    activeSource.value = id
    result.value = { ...EMPTY_RESULT }
    void loadPage(1)
  }

  function setType(t: BrowseType): void {
    activeType.value = t
    result.value = { ...EMPTY_RESULT }
    void loadPage(1)
  }

  function retry(): void {
    void loadPage(lastPage)
  }

  void init()

  return {
    result,
    sources,
    activeSource,
    activeType,
    loading,
    error,
    setSource,
    setType,
    loadPage,
    retry,
  }
}
