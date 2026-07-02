/**
 * useCategories – reorderCategory tie-break (F3 defensive).
 *
 * Pins that a reorder swap writes DISTINCT sortOrder values even when the two
 * rows currently SHARE a sortOrder (a legacy collision the backend
 * NormalizeSortOrder repairs on startup, but a not-yet-restarted DB may still
 * carry). A plain value-swap of two equal values is a no-op — the top slot could
 * never move (the reported bug). The defensive guard forces distinct values.
 *
 * Non-vacuous: with the old plain-swap code both PATCH bodies would carry the
 * same sortOrder (0), and the `toEqual` distinct-values assertion would fail.
 *
 * vi.mock is hoisted by Vitest's transform so the mock is in place before
 * useCategories.ts is evaluated, regardless of import order in this file.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useCategories } from './useCategories'

// Two categories that (pathologically) SHARE sortOrder 0 — the deployed tie.
const TIED = [
  { id: 'cat-manga', name: 'Manga', count: 0, protected: false, isDefault: false, sortOrder: 0 },
  { id: 'cat-nsfw', name: 'NSFW', count: 0, protected: false, isDefault: false, sortOrder: 0 },
]

// Records each PATCH's { id, sortOrder } so the test can assert distinct values.
let patchBodies: { id: string, sortOrder: number }[] = []

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string) => {
      if (path === '/api/categories') return Promise.resolve({ data: TIED, error: null })
      return Promise.resolve({ data: null, error: null })
    }),
    PATCH: vi.fn().mockImplementation((_path: string, opts: { params: { path: { id: string } }, body: { sortOrder: number } }) => {
      patchBodies.push({ id: opts.params.path.id, sortOrder: opts.body.sortOrder })
      return Promise.resolve({ data: {}, error: null })
    }),
    POST: vi.fn(),
    DELETE: vi.fn(),
    use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

describe('useCategories – reorderCategory tie-break', () => {
  beforeEach(() => {
    patchBodies = []
  })

  it('writes distinct sortOrder values when the two rows are tied (moving the second row up)', async () => {
    const { reorderCategory, pending } = useCategories()
    await vi.waitFor(() => expect(pending.value).toBe(false))

    // Move NSFW (index 1) UP past Manga (index 0). Both currently share order 0.
    await reorderCategory({ id: 'cat-nsfw', direction: -1 })

    expect(patchBodies).toHaveLength(2)
    // The two writes must carry DISTINCT sortOrder values, else the swap is a no-op.
    expect(patchBodies[0]!.sortOrder).not.toBe(patchBodies[1]!.sortOrder)
    // The moved row (target = NSFW) takes the neighbor's order (0); the neighbor
    // (Manga) is pushed one step down so NSFW ends up above it.
    const nsfw = patchBodies.find(p => p.id === 'cat-nsfw')!
    const manga = patchBodies.find(p => p.id === 'cat-manga')!
    expect(nsfw.sortOrder).toBeLessThan(manga.sortOrder)
  })
})
