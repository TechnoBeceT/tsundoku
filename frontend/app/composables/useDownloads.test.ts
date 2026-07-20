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
  provider: '2499283573021220255',
  providerName: 'MangaDex',
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
let getCalls: { path: string; state: string; limit: number | undefined; offset: number | undefined; include: boolean }[] = []
let postCount = 0
let runError = false

// ---- Module mock ------------------------------------------------------------

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string, options?: { params?: { query?: Record<string, unknown> } }) => {
      const q = options?.params?.query ?? {}
      const state = (q.state as string | undefined) ?? ''
      const limit = q.limit as number | undefined
      const offset = q.offset as number | undefined
      const include = q.include_source_failures === true

      getCalls.push({ path, state, limit, offset, include })

      if (path !== '/api/downloads') {
        return Promise.resolve({ data: null, error: null })
      }

      // Count probes: limit === 1 — server total only, no items.
      if (limit === 1) {
        const totals: Record<string, number> = {
          'downloading,upgrading': 3,
          // The honest failed-set probe passes include_source_failures=true; its
          // total (7) exceeds the 5 state-failed + 2 permanently-failed because it
          // also counts downloaded broken-upgrade rows.
          'failed,permanently_failed': 7,
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
    POST: vi.fn().mockImplementation((path: string) => {
      if (path === '/api/downloads/run') {
        postCount++
        if (runError) return Promise.resolve({ data: null, error: { message: 'boom' } })
        return Promise.resolve({ data: { started: true }, error: null })
      }
      return Promise.resolve({ data: null, error: null })
    }),
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
    postCount = 0
    runError = false
  })

  it('initial load fires active-tab page fetch + 3 count probes', async () => {
    const { loading, counts } = useDownloads()

    await vi.waitFor(() => {
      expect(loading.value).toBe(false)
      // All 3 count probes settled; queued is the most distinctive assertion.
      expect(counts.value.queued).toBe(250)
    })

    const dlCalls = getCalls.filter((c) => c.path === '/api/downloads')
    expect(dlCalls).toHaveLength(4)

    // Exactly one page fetch (limit 50) for the active tab — state-only, no widening.
    const pageFetches = dlCalls.filter((c) => c.limit !== 1)
    expect(pageFetches).toHaveLength(1)
    expect(pageFetches[0]!.state).toBe('downloading,upgrading')
    expect(pageFetches[0]!.offset ?? 0).toBe(0)
    expect(pageFetches[0]!.include).toBe(false)

    // Exactly three count probes (limit 1), one per group.
    const probes = dlCalls.filter((c) => c.limit === 1)
    expect(probes).toHaveLength(3)
    const probeStates = probes.map((p) => p.state).sort()
    expect(probeStates).toEqual(
      ['downloading,upgrading', 'failed,permanently_failed', 'wanted,upgrade_available'].sort(),
    )

    // Only the failed-set probe widens to the honest failures set.
    const failedProbe = probes.find((p) => p.state === 'failed,permanently_failed')!
    expect(failedProbe.include).toBe(true)
    const activeProbe = probes.find((p) => p.state === 'downloading,upgrading')!
    expect(activeProbe.include).toBe(false)

    // Counts populated from server totals.
    expect(counts.value.active).toBe(3)
    expect(counts.value.allFailures).toBe(7)
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

describe('useDownloads – runNow() "Download now" action', () => {
  beforeEach(() => {
    getCalls = []
    postCount = 0
    runError = false
  })

  it('POSTs /api/downloads/run and surfaces a started message', async () => {
    const { loading, running, runNow, runMessage, runError: runErrorRef } = useDownloads()

    await vi.waitFor(() => expect(loading.value).toBe(false))
    expect(postCount).toBe(0)

    await runNow()

    expect(postCount).toBe(1)
    expect(running.value).toBe(false)
    expect(runMessage.value).toBe('Download cycle started')
    expect(runErrorRef.value).toBe('')
  })

  it('surfaces a failure in runError and never swallows it (§16)', async () => {
    runError = true
    const { loading, runNow, runError: runErrorRef, runMessage } = useDownloads()

    await vi.waitFor(() => expect(loading.value).toBe(false))

    await runNow()

    expect(postCount).toBe(1)
    expect(runErrorRef.value).toBe('Failed to start download cycle')
    expect(runMessage.value).toBe('')
  })
})
