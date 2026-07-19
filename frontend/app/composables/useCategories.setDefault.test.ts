/**
 * useCategories – setDefaultCategory.
 *
 * Pins the F2/F4 wiring that the FE actually calls the new
 * PATCH /api/categories/{id}/default endpoint (the old code was a literal no-op)
 * and refetches on success so the DEFAULT badge moves.
 *
 * Non-vacuous: if setDefaultCategory were still `() => {}` (the previous no-op),
 * patchPaths would stay empty and this test would fail; a wrong path (e.g. the
 * plain PATCH /api/categories/{id}) would also fail the path assertion.
 *
 * vi.mock is hoisted by Vitest's transform so the mock is in place before
 * useCategories.ts is evaluated, regardless of import order in this file.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useCategories } from './useCategories'

const CATEGORY = {
  id: 'cat-manga',
  name: 'Manga',
  count: 3,
  isDefault: false,
  sortOrder: 0,
}

// Call tracking — captured by reference by the mock closures.
let patchPaths: string[] = []
let patchParams: unknown[] = []
let categoriesGetCount = 0

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string) => {
      if (path === '/api/categories') {
        categoriesGetCount++
        return Promise.resolve({ data: [CATEGORY], error: null })
      }
      return Promise.resolve({ data: null, error: null })
    }),
    PATCH: vi.fn().mockImplementation((path: string, opts: unknown) => {
      patchPaths.push(path)
      patchParams.push(opts)
      return Promise.resolve({ data: {}, error: null })
    }),
    POST: vi.fn(),
    DELETE: vi.fn(),
    use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

describe('useCategories – setDefaultCategory', () => {
  beforeEach(() => {
    patchPaths = []
    patchParams = []
    categoriesGetCount = 0
  })

  it('PATCHes the /default endpoint for the id and refetches on success', async () => {
    const { setDefaultCategory, pending, categoryAction } = useCategories()

    // Wait for the initial GET /api/categories to settle.
    await vi.waitFor(() => expect(pending.value).toBe(false))
    const getsBefore = categoriesGetCount

    await setDefaultCategory(CATEGORY.id)

    // Exactly the default endpoint was PATCHed, with the id in the path params.
    expect(patchPaths).toEqual(['/api/categories/{id}/default'])
    expect(patchParams[0]).toEqual({ params: { path: { id: CATEGORY.id } } })

    // Success refetched the category list (so the DEFAULT badge moves).
    expect(categoriesGetCount).toBe(getsBefore + 1)

    // No error surfaced.
    expect(categoryAction.value.error).toBeUndefined()
  })
})
