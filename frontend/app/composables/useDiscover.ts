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
 *   setSource(id)      — switch source, reset to page 1, refetch
 *   setType(t)         — switch listing, reset to page 1, refetch
 *   loadPage(n)        — fetch page n; n === 1 replaces manga, n > 1 appends
 *   retry()            — retry the last attempted page (resets to page 1 on
 *                        initial-load errors; retries the failed page on
 *                        load-more errors)
 *   loadDetails(c)     — on-demand rich-details fetch for the Discover hover
 *                        preview (see below)
 *
 * loadDetails(candidate) FORCES Suwayomi to fetch full metadata
 * (author/artist/description/genres) for one candidate via
 * `GET /api/sources/{sourceId}/manga/{mangaId}/details?url=` (`url` is
 * REQUIRED, P2 Suwayomi-removal) — Search/Browse only
 * ever return the lightweight fields, so the hover preview stays empty until
 * this is called. It is on-demand and cached: a mangaId whose details already
 * loaded (or are currently loading) is a no-op, so repeatedly hovering the
 * same card never re-fetches. On success the returned author/artist/
 * description/genres are merged into the matching candidate IN PLACE in
 * `result.manga` so `DiscoverHoverPreview` (which already renders those
 * fields) fills in reactively. The merge is guarded against a stale/removed
 * candidate: if the candidate is no longer in the current page (the owner
 * switched source/listing while the request was in flight) the response is
 * discarded. A fetch failure is non-fatal — it is NOT cached as "loaded" (so a
 * later hover can retry), and no page-level error is surfaced; the hover
 * preview simply keeps its "No description available" fallback.
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

  // loadDetails' cache/in-flight guards, keyed by "source:mangaId" (a
  // candidate's identity — see detailsKey). detailsLoaded is the PERMANENT
  // cache (a mangaId lands here only after a successful fetch, so a later
  // hover never re-fetches). detailsInFlight guards a rapid re-hover of the
  // SAME card from firing a second overlapping request before the first
  // resolves; it is cleared once that request settles either way.
  const detailsLoaded = new Set<string>()
  const detailsInFlight = new Set<string>()

  function detailsKey(c: DiscoverCandidate): string {
    return `${c.source}:${c.mangaId}`
  }

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
    const mapped = res.data.map(mapSource)
    sources.value = mapped
    // res.data is non-empty (guarded above), so mapped[0] exists; the guard
    // narrows it out of `undefined` (noUncheckedIndexedAccess).
    const first = mapped[0]
    if (!first) return
    activeSource.value = first.id
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

  async function loadDetails(candidate: DiscoverCandidate): Promise<void> {
    const key = detailsKey(candidate)
    if (detailsLoaded.has(key) || detailsInFlight.has(key)) return
    detailsInFlight.add(key)
    try {
      const res = await apiClient.GET('/api/sources/{sourceId}/manga/{mangaId}/details', {
        params: {
          path: { sourceId: candidate.source, mangaId: candidate.mangaId },
          query: { url: candidate.url },
        },
      })
      if (res.error || !res.data) return // non-fatal: leave the fallback text
      const dto = res.data

      // Guard a stale/removed candidate: the owner may have switched
      // source/listing while this request was in flight, so the candidate
      // this response describes might no longer be on the current page.
      const idx = result.value.manga.findIndex(m => m.source === dto.source && m.mangaId === dto.mangaId)
      if (idx === -1) return

      result.value.manga.splice(idx, 1, {
        ...result.value.manga[idx]!,
        author: dto.author,
        artist: dto.artist,
        description: dto.description,
        genres: dto.genres,
      })
      detailsLoaded.add(key)
    }
    catch {
      // non-fatal: swallow, leave the fallback "No description available" text.
    }
    finally {
      detailsInFlight.delete(key)
    }
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
    loadDetails,
  }
}
