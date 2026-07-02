/**
 * useDiscover – DTO → DiscoverCandidate mapping (M4 metadata fields).
 *
 * Pins that mapCandidate carries author/artist/description/genres straight off
 * the SearchCandidate DTO onto the screen type, alongside the existing fields.
 * Non-vacuous: if the mapper dropped author/artist (the M4 addition) this test
 * would fail on the `author`/`artist` assertions while every other field still
 * passed — proving the assertion actually exercises the new mapping, not just
 * the pre-existing one.
 *
 * vi.mock is hoisted by Vitest's transform so the mock is in place before
 * useDiscover.ts is evaluated, regardless of import order in this file.
 */
import { describe, it, expect, vi } from 'vitest'
import { useDiscover } from './useDiscover'

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
              author: 'Makoto Yukimura',
              artist: 'Makoto Yukimura',
              description: "A Viking's saga.",
              genres: ['Action', 'Historical'],
            }],
            hasNextPage: false,
            page: 1,
          },
          error: null,
        })
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
  it('carries author/artist/description/genres and the proxy thumbnailUrl onto DiscoverCandidate', async () => {
    const { result } = useDiscover()

    // init() resolves the source list and page 1 asynchronously; wait for the
    // candidate to land rather than racing on `loading` (which starts false
    // before init's first await runs).
    await vi.waitFor(() => expect(result.value.manga.length).toBe(1))

    const c = result.value.manga[0]!
    expect(c.thumbnailUrl).toBe('/api/sources/src-1/manga/42/cover')
    expect(c.author).toBe('Makoto Yukimura')
    expect(c.artist).toBe('Makoto Yukimura')
    expect(c.description).toBe("A Viking's saga.")
    expect(c.genres).toEqual(['Action', 'Historical'])
  })
})
