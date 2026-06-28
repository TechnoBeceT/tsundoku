/**
 * useDownloads – per-tab pagination + server counts.
 *
 * Pins three key behaviours:
 *   1. Initial load fires one page fetch (active tab, offset 0) + 4 count probes (limit:1).
 *   2. setTab('queued') refetches offset 0 for queued states; total/hasMore reflect server total.
 *   3. loadMore() increments offset and appends items; counts remain from the initial probes.
 *
 * Non-vacuous: if the composable still fetches ALL_STATES at once without per-tab logic,
 * test 1 would see only 1 GET call (not 5) and fail on the probe-count assertion; test 2 would
 * fail because total would reflect the old ALL_STATES total; test 3 would fail because loadMore
 * would not exist on the returned object.
 *
 * vi.mock is hoisted by Vitest's transform so the mock is in place before
 * useDownloads.ts is evaluated, regardless of import order in this file.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useDownloads } from './useDownloads'

// ---- Mock data helpers ------------------------------------------------------

const makeDto = (n: number, state = 'wanted') => ({
  id: `00000000-0000-0000-0000-${String(n).padStart(12, '0')}`,
  seriesId: `00000000-0001-0000-0000-${String(n).padStart(12, '0')}`,
  seriesTitle: `Series ${n}`,
  seriesCategory: 'Manga' as const,
  seriesCoverUrl: '',
  chapterKey: `ch-${n}`,
  number: n,
  name: `Chapter ${n}`,
  state,
  provider: 'MangaDex',
  retries: 0,
  nextAttemptAt: null,
  lastError: '',
  errorCategory: '',
  filename: '',
  pageCount: null,
  downloadDate: null,
})

// 50 stub items for the first page of queued fetch
const QUEUED_PAGE_1 = Array.from({ length: 50 }, (_, i) => makeDto(i + 1, 'wanted'))
// 50 stub items for the second page (offset 50) of queued fetch
const QUEUED_PAGE_2 = Array.from({ length: 50 }, (_, i) => makeDto(i + 51, 'wanted'))
// 3 active items for the active tab fetch
const ACTIVE_ITEMS = Array.from({ length: 3 }, (_, i) => makeDto(i + 1, 'downloading'))

// ---- Call tracking ----------------------------------------------------------

// Mutable binding — the mock closes over this variable; beforeEach reassigns it.
let getCalls: { path: string; state: string; limit: number | undefined; offset: number | undefined }[] = []

// ---- Module mock ------------------------------------------------------------

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string, options?: { params?: { query?: Record<string, unknown> } }) => {
      const q = options?.params?.query ?? {}
      const state = (q.state as string | undefined) ?? ''
      const limit = q.limit as number | undefined
      const offset = q.offset as number | undefined

      getCalls.push({ path, state, limit, offset })

      if (path !== '/api/downloads') {
        return Promise.resolve({ data: null, error: null })
      }

      // Count probes: limit === 1 — server total only, no items.
      if (limit === 1) {
        const totals: Record<string, number> = {
          'downloading,upgrading': 3,
          'failed': 5,
          'permanently_failed': 2,
          'wanted,upgrade_available': 250,
        }
        return Promise.resolve({ data: { total: totals[state] ?? 0, items: [] }, error: null })
      }

      // Queued page fetch — first or second page based on offset.
      if (state === 'wanted,upgrade_available') {
        const items = (offset ?? 0) === 0 ? QUEUED_PAGE_1 : QUEUED_PAGE_2
        return Promise.resolve({ data: { total: 250, items }, error: null })
      }

      // Active tab page fetch (or any other tab).
      return Promise.resolve({ data: { total: 3, items: ACTIVE_ITEMS }, error: null })
    }),
    POST: vi.fn(),
    PATCH: vi.fn(),
    DELETE: vi.fn(),
    use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

// ---- Tests ------------------------------------------------------------------

describe('useDownloads – per-tab pagination + server counts', () => {
  beforeEach(() => {
    getCalls = []
  })

  it('initial load fires active-tab page fetch + 4 count probes', async () => {
    const { loading, counts } = useDownloads()

    await vi.waitFor(() => {
      expect(loading.value).toBe(false)
      // All 4 count probes settled; queued is the most distinctive assertion.
      expect(counts.value.queued).toBe(250)
    })

    const dlCalls = getCalls.filter((c) => c.path === '/api/downloads')
    expect(dlCalls).toHaveLength(5)

    // Exactly one page fetch (limit 50) for the active tab.
    const pageFetches = dlCalls.filter((c) => c.limit !== 1)
    expect(pageFetches).toHaveLength(1)
    expect(pageFetches[0]!.state).toBe('downloading,upgrading')
    expect(pageFetches[0]!.offset ?? 0).toBe(0)

    // Exactly four count probes (limit 1), one per state group.
    const probes = dlCalls.filter((c) => c.limit === 1)
    expect(probes).toHaveLength(4)
    const probeStates = probes.map((p) => p.state).sort()
    expect(probeStates).toEqual(
      ['downloading,upgrading', 'failed', 'permanently_failed', 'wanted,upgrade_available'].sort(),
    )

    // All four counts populated from server totals.
    expect(counts.value.active).toBe(3)
    expect(counts.value.failed).toBe(5)
    expect(counts.value.terminal).toBe(2)
    expect(counts.value.queued).toBe(250)
  })

  it('setTab(queued) refetches at offset 0 with total=250 and hasMore=true', async () => {
    const { loading, setTab, total, hasMore, items } = useDownloads()

    await vi.waitFor(() => expect(loading.value).toBe(false))
    getCalls = []

    setTab('queued')

    await vi.waitFor(() => {
      expect(total.value).toBe(250)
    })

    expect(items.value).toHaveLength(50)
    expect(hasMore.value).toBe(true)

    // One page fetch (non-count) for queued states, starting at offset 0.
    const pageFetches = getCalls.filter((c) => c.path === '/api/downloads' && c.limit !== 1)
    expect(pageFetches).toHaveLength(1)
    expect(pageFetches[0]!.state).toBe('wanted,upgrade_available')
    expect(pageFetches[0]!.offset ?? 0).toBe(0)
  })

  it('loadMore() fetches offset 50 and appends; counts unchanged', async () => {
    const { loading, setTab, total, hasMore, items, loadMore, counts } = useDownloads()

    await vi.waitFor(() => expect(loading.value).toBe(false))

    // Switch to queued tab and wait for first page.
    setTab('queued')
    await vi.waitFor(() => expect(total.value).toBe(250))
    expect(items.value).toHaveLength(50)

    getCalls = []
    await loadMore()

    // Items appended: 50 + 50 = 100; server total is unchanged.
    expect(items.value).toHaveLength(100)
    expect(total.value).toBe(250)
    expect(hasMore.value).toBe(true) // 100 < 250

    // The load-more fetch used offset 50.
    const pageFetches = getCalls.filter((c) => c.path === '/api/downloads' && c.limit !== 1)
    expect(pageFetches).toHaveLength(1)
    expect(pageFetches[0]!.offset).toBe(50)

    // counts not re-probed by loadMore — queued count stays at 250.
    expect(counts.value.queued).toBe(250)
  })
})
