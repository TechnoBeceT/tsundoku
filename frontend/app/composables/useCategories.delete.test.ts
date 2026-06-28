/**
 * useCategories – deleteCategory drain loop.
 *
 * Pins the drain-loop behaviour: when a category has >200 members,
 * deleteCategory loops GET /api/series?category=name&limit=200 until a fetch
 * returns 0 results, PATCHing each series to the target category, then DELETEs
 * the now-empty category.
 *
 * Non-vacuous: the old single-fetch code calls GET /api/series once, PATCHes
 * only 200 series, then DELETE — leaving 50 series stranded and returning 409.
 * The drain loop correctly gives seriesGetCount=3 and patchCount=250; if the
 * loop were collapsed back to a single fetch these assertions would fail.
 *
 * vi.mock is hoisted by Vitest's transform so the mock is in place before
 * useCategories.ts is evaluated, regardless of import order in this file.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useCategories } from './useCategories'

// ── Data fixtures ─────────────────────────────────────────────────────────────

const CATEGORY = {
  id: 'cat-1',
  name: 'Manga',
  count: 250,
  protected: false,
  sortOrder: 0,
}

const TARGET_ID = 'cat-2'

// Minimal stubs — the drain loop only reads s.id.
const PAGE_200 = Array.from({ length: 200 }, (_, i) => ({ id: `series-${i + 1}` }))
const PAGE_50 = Array.from({ length: 50 }, (_, i) => ({ id: `series-${i + 201}` }))
const PAGE_0: { id: string }[] = []

// ── Call tracking ─────────────────────────────────────────────────────────────

// Mutable bindings — the mock closures capture these by reference; beforeEach resets them.
let seriesGetCount = 0
let patchCount = 0
let deleteCount = 0

// ── Module mock ───────────────────────────────────────────────────────────────

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string) => {
      if (path === '/api/categories') {
        return Promise.resolve({ data: [CATEGORY], error: null })
      }
      if (path === '/api/series') {
        seriesGetCount++
        if (seriesGetCount === 1) return Promise.resolve({ data: PAGE_200, error: null })
        if (seriesGetCount === 2) return Promise.resolve({ data: PAGE_50, error: null })
        return Promise.resolve({ data: PAGE_0, error: null })
      }
      return Promise.resolve({ data: null, error: null })
    }),
    PATCH: vi.fn().mockImplementation(() => {
      patchCount++
      return Promise.resolve({ data: {}, error: null })
    }),
    DELETE: vi.fn().mockImplementation(() => {
      deleteCount++
      return Promise.resolve({ data: null, error: null })
    }),
    POST: vi.fn(),
    use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('useCategories – deleteCategory drain loop', () => {
  beforeEach(() => {
    seriesGetCount = 0
    patchCount = 0
    deleteCount = 0
  })

  it('loops GET /api/series until empty, PATCHes 250 times, then DELETEs once', async () => {
    const { deleteCategory, pending, categoryAction } = useCategories()

    // Wait for the initial GET /api/categories (fired by useAsyncResource) to settle
    // so that rawDtos.value is populated before deleteCategory tries to look up the name.
    await vi.waitFor(() => expect(pending.value).toBe(false))

    // Run the drain + delete.
    await deleteCategory({ id: CATEGORY.id, targetId: TARGET_ID })

    // GET /api/series was called 3×: 200 items → 50 items → 0 items (exits loop).
    expect(seriesGetCount).toBe(3)

    // All 250 series were reassigned (200 first batch + 50 second batch).
    expect(patchCount).toBe(250)

    // DELETE was called exactly once — after all series were reassigned.
    // (Ordering is structural: the loop must exhaust before DELETE is reached.)
    expect(deleteCount).toBe(1)

    // No error surfaced.
    expect(categoryAction.value.error).toBeUndefined()
  })
})
