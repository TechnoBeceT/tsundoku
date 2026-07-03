/**
 * useDiscover – DTO → DiscoverCandidate mapping (M4 metadata fields) and the
 * on-demand rich-details fetch (loadDetails).
 *
 * Pins that mapCandidate carries author/artist/description/genres straight off
 * the SearchCandidate DTO onto the screen type, alongside the existing fields.
 * Also pins loadDetails: it merges a forced-fetch response into the matching
 * candidate, caches by mangaId (no re-fetch on a second call), guards a
 * stale/removed candidate, and never surfaces a page-level error on failure.
 *
 * vi.mock is hoisted by Vitest's transform so the mock is in place before
 * useDiscover.ts is evaluated, regardless of import order in this file.
 */
import { describe, it, expect, vi } from 'vitest'
import { useDiscover } from './useDiscover'

// Typed via the initial implementation (not `vi.fn().mockImplementation(...)`)
// so detailsGetMock's return type is inferred as a concrete object, not `any`
// — the untyped-mock form leaks `any` into every `return detailsGetMock()`
// call site below and trips @typescript-eslint/no-unsafe-return.
const detailsGetMock = vi.fn(() =>
  Promise.resolve({
    data: {
      source: 'src-1',
      sourceName: 'MangaDex',
      lang: 'en',
      mangaId: 42,
      title: 'Vinland Saga',
      url: 'https://mangadex.org/title/42',
      thumbnailUrl: '/api/sources/src-1/manga/42/cover',
      author: 'Makoto Yukimura',
      artist: 'Makoto Yukimura',
      description: "A Viking's saga.",
      genres: ['Action', 'Historical'],
    },
    error: null as string | null,
  }),
)

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string) => {
      if (path === '/api/sources') {
        return Promise.resolve({
          data: [{ id: 'src-1', name: 'MangaDex', lang: 'en' }],
          error: null,
        })
      }
      if (path === '/api/sources/{sourceId}/browse') {
        return Promise.resolve({
          data: {
            manga: [{
              source: 'src-1',
              sourceName: 'MangaDex',
              lang: 'en',
              mangaId: 42,
              title: 'Vinland Saga',
              url: 'https://mangadex.org/title/42',
              thumbnailUrl: '/api/sources/src-1/manga/42/cover',
              // Search/Browse are lightweight — author/artist/description are
              // empty until loadDetails() forces the fetchManga mutation.
              author: '',
              artist: '',
              description: '',
              genres: [],
            }],
            hasNextPage: false,
            page: 1,
          },
          error: null,
        })
      }
      if (path === '/api/sources/{sourceId}/manga/{mangaId}/details') {
        return detailsGetMock()
      }
      return Promise.resolve({ data: null, error: null })
    }),
    POST: vi.fn(),
    PATCH: vi.fn(),
    DELETE: vi.fn(),
    use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

describe('useDiscover – candidate metadata mapping', () => {
  it('carries the proxy thumbnailUrl onto DiscoverCandidate (rich fields arrive via loadDetails, not Browse)', async () => {
    const { result } = useDiscover()

    // init() resolves the source list and page 1 asynchronously; wait for the
    // candidate to land rather than racing on `loading` (which starts false
    // before init's first await runs).
    await vi.waitFor(() => expect(result.value.manga.length).toBe(1))

    const c = result.value.manga[0]!
    expect(c.thumbnailUrl).toBe('/api/sources/src-1/manga/42/cover')
    expect(c.author).toBe('')
    expect(c.description).toBe('')
  })
})

describe('useDiscover – loadDetails (on-demand rich hover details)', () => {
  it('merges the returned author/artist/description/genres into the matching candidate', async () => {
    const { result, loadDetails } = useDiscover()
    await vi.waitFor(() => expect(result.value.manga.length).toBe(1))

    await loadDetails(result.value.manga[0]!)

    const c = result.value.manga[0]!
    expect(c.author).toBe('Makoto Yukimura')
    expect(c.artist).toBe('Makoto Yukimura')
    expect(c.description).toBe("A Viking's saga.")
    expect(c.genres).toEqual(['Action', 'Historical'])
  })

  it('caches by mangaId — a second loadDetails call for the same candidate does not re-fetch', async () => {
    const { result, loadDetails } = useDiscover()
    await vi.waitFor(() => expect(result.value.manga.length).toBe(1))

    detailsGetMock.mockClear()
    await loadDetails(result.value.manga[0]!)
    expect(detailsGetMock).toHaveBeenCalledTimes(1)

    await loadDetails(result.value.manga[0]!)
    expect(detailsGetMock).toHaveBeenCalledTimes(1)
  })

  it('is a no-op for a candidate no longer in the current page (guards a stale/removed candidate)', async () => {
    const { result, loadDetails } = useDiscover()
    await vi.waitFor(() => expect(result.value.manga.length).toBe(1))

    const candidate = result.value.manga[0]!
    // Simulate the owner switching source/listing while the request would be
    // in flight: the candidate is no longer in result.manga by the time the
    // (mocked, synchronous) response lands.
    result.value = { manga: [], hasNextPage: false, page: 0 }

    await expect(loadDetails(candidate)).resolves.toBeUndefined()
    expect(result.value.manga).toEqual([])
  })

  it('does not surface a page error on a fetch failure and leaves the fallback text (non-fatal)', async () => {
    const { result, error, loadDetails } = useDiscover()
    await vi.waitFor(() => expect(result.value.manga.length).toBe(1))

    detailsGetMock.mockImplementationOnce(() => Promise.reject(new Error('network down')))
    const candidate = result.value.manga[0]!

    await expect(loadDetails(candidate)).resolves.toBeUndefined()
    expect(error.value).toBe(false)
    expect(result.value.manga[0]!.author).toBe('') // fallback text still applies
  })
})
