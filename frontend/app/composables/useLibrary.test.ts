/**
 * useLibrary — the Komikku/Suwayomi model: the WHOLE library loads ONCE, then
 * category-switch + search + sort are pure IN-MEMORY derivations. NO refetch on
 * tab/search/sort, no "Load more".
 *
 * These tests pin:
 *  - landing category resolution (?category > owner default > All)
 *  - the one-time whole-library load pages under the 200 cap
 *  - category/search/sort never trigger another GET
 *  - the in-memory filter+search+sort pipeline + the matchesElsewhere escape hatch
 *
 * vi.mock is hoisted by Vitest's transform so the mock is in place before
 * useLibrary.ts is evaluated. The series/categories responses are driven by
 * mutable module-level state each test sets before constructing the composable.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useLibrary } from './useLibrary'

// A spy hoisted alongside the mock so we can count /api/series GETs (the whole
// point of the model is that there is exactly one load, not one per interaction).
const { seriesGetSpy } = vi.hoisted(() => ({ seriesGetSpy: vi.fn() }))

interface Row {
  id: string
  title: string
  displayName: string
  slug: string
  category: string
  coverUrl: string
  monitored: boolean
  completed: boolean
  needsSource: boolean
  chapterCounts: { total: number, downloaded: number, wanted: number, failed: number, unread: number }
  createdAt: string
  lastChapterDownloadedAt: string | null
  latestChapterAt: string | null
  isStalled: boolean
}

interface Cat { id: string, name: string, sortOrder: number, protected: boolean, isDefault: boolean, count: number }

const makeRow = (n: number, over: Partial<Row> = {}): Row => ({
  id: `00000000-0000-0000-0000-${String(n).padStart(12, '0')}`,
  title: `Series ${n}`,
  displayName: `Series ${n}`,
  slug: `series-${n}`,
  category: 'Other',
  coverUrl: '',
  monitored: true,
  completed: false,
  needsSource: false,
  chapterCounts: { total: 0, downloaded: 0, wanted: 0, failed: 0, unread: 0 },
  createdAt: '2024-01-01T00:00:00Z',
  lastChapterDownloadedAt: null,
  latestChapterAt: null,
  isStalled: false,
  ...over,
})

const makeCat = (name: string, isDefault: boolean, count = 0): Cat => ({
  id: `cat-${name}`,
  name,
  sortOrder: 0,
  protected: false,
  isDefault,
  count,
})

interface Prefs {
  sortKey: string
  sortDir: string
  filters: { downloaded: boolean, unread: boolean, completed: boolean, needsSource: boolean, stalled: boolean }
}

// Mutable per-test state the mock reads at call time.
let allRows: Row[] = []
let seriesTotalHeader: string | null = null
let categoriesData: Cat[] = []
// null ⇒ the prefs GET resolves with no data (nothing stored) → defaults kept.
let libraryPrefsData: Prefs | null = null

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string, opts?: { params?: { query?: { limit?: number, offset?: number } } }) => {
      if (path === '/api/series') {
        seriesGetSpy(opts)
        const offset = opts?.params?.query?.offset ?? 0
        const limit = opts?.params?.query?.limit ?? allRows.length
        const page = allRows.slice(offset, offset + limit)
        const headers: Record<string, string>
          = seriesTotalHeader === null ? {} : { 'X-Total-Count': seriesTotalHeader }
        return Promise.resolve({ data: page, error: null, response: new Response(null, { headers }) })
      }
      if (path === '/api/categories') {
        return Promise.resolve({ data: categoriesData, error: null, response: new Response() })
      }
      if (path === '/api/library/prefs') {
        // Default: no stored prefs → load() returns null → composable keeps its
        // own defaults. Tests that pin persistence override this per-test.
        return Promise.resolve({ data: libraryPrefsData, error: libraryPrefsData ? null : 'none', response: new Response() })
      }
      return Promise.resolve({ data: [], error: null, response: new Response() })
    }),
    POST: vi.fn(),
    PATCH: vi.fn(),
    PUT: vi.fn().mockResolvedValue({ data: {}, error: null }),
    DELETE: vi.fn(),
    use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

// Construct the composable and wait until the one-time load has settled.
async function mountSettled(opts?: { initialCategory?: string | null }) {
  const lib = useLibrary(opts)
  await vi.waitFor(() => expect(lib.pending.value).toBe(false))
  return lib
}

beforeEach(() => {
  seriesGetSpy.mockClear()
  allRows = [makeRow(1)]
  seriesTotalHeader = '1'
  categoriesData = []
  libraryPrefsData = null
})

describe('useLibrary — landing category', () => {
  it('lands on the default category (Category.isDefault), not All', async () => {
    categoriesData = [makeCat('Manga', false), makeCat('Manhwa', true), makeCat('Other', false)]
    const lib = await mountSettled()
    expect(lib.activeCategory.value).toBe('Manhwa')
  })

  it('falls back to All (null) when no category is marked default', async () => {
    categoriesData = [makeCat('Manga', false), makeCat('Other', false)]
    const lib = await mountSettled()
    expect(lib.activeCategory.value).toBeNull()
  })

  it('?category (opts.initialCategory) wins over the default', async () => {
    categoriesData = [makeCat('Manhwa', true), makeCat('Manga', false)]
    const lib = await mountSettled({ initialCategory: 'Manga' })
    expect(lib.activeCategory.value).toBe('Manga')
  })
})

describe('useLibrary — no refetch on interaction', () => {
  it('does NOT refetch when category, search, sort, or filters change', async () => {
    categoriesData = [makeCat('Manga', false)]
    const lib = await mountSettled()
    const calls = seriesGetSpy.mock.calls.length

    lib.setCategory('Manga')
    lib.setSearch('solo')
    lib.setSort('unread', 'desc')
    lib.setFilters({ downloaded: false, unread: false, completed: false, needsSource: true, stalled: false })
    await Promise.resolve()

    expect(seriesGetSpy.mock.calls.length).toBe(calls)
  })
})

describe('useLibrary — one-time whole-library load', () => {
  it('pages the initial load under the 200 cap', async () => {
    allRows = Array.from({ length: 350 }, (_, i) => makeRow(i + 1))
    seriesTotalHeader = '350'
    categoriesData = [] // no default → activeCategory null → the grid shows everything

    const lib = await mountSettled()

    // Exactly two GETs: offset 0 (page 1) then offset 200 (page 2, under the cap).
    type GetOpts = { params?: { query?: { offset?: number } } } | undefined
    const offsets = seriesGetSpy.mock.calls.map(c => (c[0] as GetOpts)?.params?.query?.offset)
    expect(offsets).toEqual([0, 200])
    // Every one of the 350 rows landed in the in-memory set.
    expect(lib.series.value.length).toBe(350)
  })

  it('reload() refetches on demand', async () => {
    categoriesData = [makeCat('Manga', false)]
    const lib = await mountSettled()
    const before = seriesGetSpy.mock.calls.length
    await lib.reload()
    expect(seriesGetSpy.mock.calls.length).toBeGreaterThan(before)
  })
})

describe('useLibrary — in-memory filter/search/sort + escape hatch', () => {
  beforeEach(() => {
    categoriesData = [makeCat('Manhwa', true), makeCat('Manga', false)]
    allRows = [
      makeRow(1, { title: 'Solo Leveling', displayName: 'Solo Leveling', category: 'Manhwa', chapterCounts: { total: 10, downloaded: 10, wanted: 0, failed: 0, unread: 2 } }),
      makeRow(2, { title: 'Solo Max Level', displayName: 'Solo Max Level', category: 'Manhwa', chapterCounts: { total: 10, downloaded: 10, wanted: 0, failed: 0, unread: 9 } }),
      makeRow(3, { title: 'Berserk', displayName: 'Berserk', category: 'Manhwa', chapterCounts: { total: 10, downloaded: 10, wanted: 0, failed: 0, unread: 0 } }),
      makeRow(4, { title: 'Solo Zero', displayName: 'Solo Zero', category: 'Manga', chapterCounts: { total: 10, downloaded: 10, wanted: 0, failed: 0, unread: 1 } }),
    ]
    seriesTotalHeader = '4'
  })

  it('series is filtered + searched + sorted; matchesElsewhere counts other categories', async () => {
    const lib = await mountSettled()
    // Landed on the default category Manhwa.
    expect(lib.activeCategory.value).toBe('Manhwa')

    lib.setSearch('solo')
    // Filtered to Manhwa + matches "solo", sorted title asc by default.
    expect(lib.series.value.map(s => s.title)).toEqual(['Solo Leveling', 'Solo Max Level'])
    // One "solo" match lives OUTSIDE Manhwa (Solo Zero in Manga).
    expect(lib.matchesElsewhere.value).toBe(1)

    // Sorting is a pure in-memory derivation — unread desc reorders.
    lib.setSort('unread', 'desc')
    expect(lib.series.value.map(s => s.title)).toEqual(['Solo Max Level', 'Solo Leveling'])
  })

  it('searchEverywhere() widens activeCategory to null', async () => {
    const lib = await mountSettled()
    expect(lib.activeCategory.value).toBe('Manhwa')
    lib.searchEverywhere()
    expect(lib.activeCategory.value).toBeNull()
  })
})

describe('useLibrary — toggle filters', () => {
  const ALL_OFF = { downloaded: false, unread: false, completed: false, needsSource: false, stalled: false }

  beforeEach(() => {
    categoriesData = [] // All (null) so both rows below are in view
    allRows = [
      makeRow(1, { title: 'Needs A Source', displayName: 'Needs A Source', needsSource: true }),
      makeRow(2, { title: 'Has A Source', displayName: 'Has A Source', needsSource: false }),
    ]
    seriesTotalHeader = '2'
  })

  it('maps dto.needsSource onto the screen type (filters off by default)', async () => {
    const lib = await mountSettled()
    expect(lib.filters.value).toEqual(ALL_OFF)
    expect(lib.series.value.map(s => [s.title, s.needsSource])).toEqual([
      ['Has A Source', false],
      ['Needs A Source', true],
    ])
  })

  it('needsSource filter narrows the in-memory grid to needsSource series', async () => {
    const lib = await mountSettled()
    lib.setFilters({ ...ALL_OFF, needsSource: true })
    expect(lib.series.value.map(s => s.title)).toEqual(['Needs A Source'])
  })

  it('clearing the filter restores the full grid', async () => {
    const lib = await mountSettled()
    lib.setFilters({ ...ALL_OFF, needsSource: true })
    lib.setFilters({ ...ALL_OFF })
    expect(lib.series.value.map(s => s.title)).toEqual(['Has A Source', 'Needs A Source'])
  })
})

describe('useLibrary — persisted prefs', () => {
  it('applies stored prefs on load (sort + filters)', async () => {
    categoriesData = []
    allRows = [
      makeRow(1, { title: 'Alpha', displayName: 'Alpha', chapterCounts: { total: 5, downloaded: 5, wanted: 0, failed: 0, unread: 1 } }),
      makeRow(2, { title: 'Bravo', displayName: 'Bravo', chapterCounts: { total: 9, downloaded: 9, wanted: 0, failed: 0, unread: 8 } }),
    ]
    seriesTotalHeader = '2'
    libraryPrefsData = { sortKey: 'unread', sortDir: 'desc', filters: { downloaded: false, unread: false, completed: false, needsSource: false, stalled: false } }

    const lib = await mountSettled()
    expect(lib.sortKey.value).toBe('unread')
    expect(lib.sortDir.value).toBe('desc')
    // unread desc → Bravo (8) before Alpha (1).
    expect(lib.series.value.map(s => s.title)).toEqual(['Bravo', 'Alpha'])
  })

  it('keeps defaults when nothing is stored', async () => {
    libraryPrefsData = null
    const lib = await mountSettled()
    expect(lib.sortKey.value).toBe('title')
    expect(lib.sortDir.value).toBe('asc')
    expect(lib.filters.value).toEqual({ downloaded: false, unread: false, completed: false, needsSource: false, stalled: false })
  })
})
