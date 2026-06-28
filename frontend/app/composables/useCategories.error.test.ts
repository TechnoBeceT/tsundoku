/**
 * useCategories – error-surfacing contract.
 *
 * Pins the behaviour that a failed GET /api/categories sets error.value to
 * the message string (surfaced via useAsyncResource's catch block). Non-vacuous:
 * if the catch block in useAsyncResource were removed, error.value would stay
 * null and this test would fail.
 *
 * vi.mock is hoisted by Vitest's transform so the mock is in place before
 * useCategories.ts is evaluated, regardless of import order in this file.
 */
import { describe, it, expect, vi } from 'vitest'
import { useCategories } from './useCategories'

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockResolvedValue({ error: { message: 'boom' }, data: undefined }),
    POST: vi.fn(),
    PATCH: vi.fn(),
    DELETE: vi.fn(),
    use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

describe('useCategories – error surfacing', () => {
  it('sets error.value when GET /api/categories fails', async () => {
    const { error } = useCategories()
    // useAsyncResource fires refresh() immediately (void); wait until it settles.
    await vi.waitFor(() => {
      expect(error.value).toBe('Failed to load categories')
    })
  })
})
