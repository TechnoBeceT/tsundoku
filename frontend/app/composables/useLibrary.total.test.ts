/**
 * useLibrary – exact total from X-Total-Count header.
 *
 * Pins the behaviour that `total.value` reflects the server total reported in
 * the `X-Total-Count` response header rather than the old sentinel:
 *   series.length + (page.length === PAGE ? 1 : 0)
 *
 * Non-vacuous: if the header is not read and the sentinel is still in place,
 * `total.value` would be 5 (the 5 stub rows, short page → no +1 sentinel),
 * not 137 — and this test would fail.
 *
 * vi.mock is hoisted by Vitest's transform so the mock is in place before
 * useLibrary.ts is evaluated, regardless of import order in this file.
 */
import { describe, it, expect, vi } from 'vitest'
import { useLibrary } from './useLibrary'

const makeRow = (n: number) => ({
  id: `00000000-0000-0000-0000-${String(n).padStart(12, '0')}`,
  title: `Series ${n}`,
  displayName: `Series ${n}`,
  slug: `series-${n}`,
  category: 'Other',
  coverUrl: '',
  monitored: true,
  completed: false,
  chapterCounts: { total: 0, downloaded: 0, wanted: 0, failed: 0 },
})

const FIVE_ROWS = Array.from({ length: 5 }, (_, i) => makeRow(i + 1))

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string) => {
      if (path === '/api/series') {
        return Promise.resolve({
          data: FIVE_ROWS,
          error: null,
          response: new Response(null, {
            headers: { 'X-Total-Count': '137' },
          }),
        })
      }
      // /api/categories — return an empty list
      return Promise.resolve({ data: [], error: null, response: new Response() })
    }),
    POST: vi.fn(),
    PATCH: vi.fn(),
    DELETE: vi.fn(),
    use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

describe('useLibrary – exact total from X-Total-Count', () => {
  it('reads total from the response header, not the sentinel', async () => {
    const { total } = useLibrary()

    // The initial load fires as a fire-and-forget void; wait until it settles.
    await vi.waitFor(() => {
      expect(total.value).toBe(137)
    })
  })
})
