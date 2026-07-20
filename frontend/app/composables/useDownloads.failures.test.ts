/**
 * useDownloads – the honest-failures wiring (include_source_failures).
 *
 * Pins:
 *   1. The Failed tab's List fetch passes include_source_failures=true; Active/Queued
 *      do not (state-only).
 *   2. The failed-set count probe passes the flag (→ allFailures honest total).
 *   3. retryAll passes include_source_failures=true.
 *   4. The failing-source fields (failingProviderName / failingAttempts / retryable /
 *      terminal / isUpgrade / upgradeTarget) survive the DTO→item mapper.
 *
 * Non-vacuous: drop the `tab === 'failed'` flag branch and test 1 fails (the failed
 * List call has include=false); skip the failing-* fields in mapItem and test 4 fails.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useDownloads } from './useDownloads'

// A downloaded chapter whose UPGRADE source keeps failing — the surfaced honest row.
const FAILING_ID = '00000000-0000-0000-0000-0000000000f1'
const makeFailingDto = () => ({
  id: FAILING_ID,
  seriesId: '00000000-0001-0000-0000-000000000001',
  seriesTitle: 'Solo Leveling',
  seriesCategory: 'Manhwa' as const,
  seriesCoverUrl: '',
  chapterKey: 'ch-91',
  number: 91,
  name: 'Chapter 91',
  state: 'downloaded',
  provider: 'comix-id',
  providerName: 'Comix',
  attempts: 0,
  maxRetries: 5,
  isUpgrade: true,
  upgradeTarget: 'Hive Scans',
  failingProvider: 'hive-id',
  failingProviderName: 'Hive Scans',
  failingAttempts: 3,
  failingLastError: 'broken page: empty image response',
  failingErrorCategory: 'no_pages',
  retryable: true,
  terminal: false,
  waitingReason: '',
  deferredUntil: null,
  deferReason: '',
  retries: 0,
  nextAttemptAt: null,
  lastError: '',
  errorCategory: '',
  filename: '[comix] ch-91.cbz',
  pageCount: 40,
  downloadDate: null,
})

interface Call { path: string; state: string; limit: number | undefined; include: boolean }
let getCalls: Call[] = []
let retryAllCalls: { state: string; include: boolean }[] = []

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string, options?: { params?: { query?: Record<string, unknown> } }) => {
      const q = options?.params?.query ?? {}
      const state = (q.state as string | undefined) ?? ''
      const limit = q.limit as number | undefined
      const include = q.include_source_failures === true
      getCalls.push({ path, state, limit, include })
      if (path !== '/api/downloads') return Promise.resolve({ data: null, error: null })
      if (limit === 1) return Promise.resolve({ data: { total: 7, items: [] }, error: null })
      // Only the failed tab (widened) returns the downloaded failing row.
      const items = include ? [makeFailingDto()] : []
      return Promise.resolve({ data: { total: items.length, items }, error: null })
    }),
    POST: vi.fn().mockImplementation((path: string, options?: { params?: { query?: Record<string, unknown> } }) => {
      if (path === '/api/downloads/retry-all') {
        const q = options?.params?.query ?? {}
        retryAllCalls.push({ state: (q.state as string | undefined) ?? '', include: q.include_source_failures === true })
      }
      return Promise.resolve({ data: { retried: 1 }, error: null })
    }),
    PATCH: vi.fn(),
    DELETE: vi.fn(),
    use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

describe('useDownloads – honest-failures wiring', () => {
  beforeEach(() => {
    getCalls = []
    retryAllCalls = []
  })

  it('passes include_source_failures only on the Failed tab List + the failed count probe', async () => {
    const dl = useDownloads()
    await dl.refresh()

    // Active tab (initial): its List + all count probes must be state-only EXCEPT the
    // failed-set probe.
    const pageFetches = getCalls.filter((c) => c.path === '/api/downloads' && c.limit !== 1)
    expect(pageFetches.every((c) => c.include === false)).toBe(true) // active List not widened

    const failedProbe = getCalls.find((c) => c.limit === 1 && c.state === 'failed,permanently_failed')!
    expect(failedProbe.include).toBe(true)
    const queuedProbe = getCalls.find((c) => c.limit === 1 && c.state === 'wanted,upgrade_available')!
    expect(queuedProbe.include).toBe(false)

    // Now switch to the Failed tab → its List fetch widens.
    getCalls = []
    dl.setTab('failed')
    await vi.waitFor(() => expect(dl.items.value.length).toBe(1))
    const failedList = getCalls.find((c) => c.path === '/api/downloads' && c.limit !== 1)!
    expect(failedList.state).toBe('failed,permanently_failed')
    expect(failedList.include).toBe(true)
  })

  it('maps the failing-source fields through to the item', async () => {
    const dl = useDownloads()
    dl.setTab('failed')
    await vi.waitFor(() => expect(dl.items.value.length).toBe(1))

    const row = dl.items.value.find((i) => i.chapterId === FAILING_ID)!
    expect(row.state).toBe('downloaded')
    expect(row.isUpgrade).toBe(true)
    expect(row.upgradeTarget).toBe('Hive Scans')
    expect(row.failingProviderName).toBe('Hive Scans')
    expect(row.failingAttempts).toBe(3)
    expect(row.maxRetries).toBe(5)
    expect(row.failingLastError).toBe('broken page: empty image response')
    expect(row.failingErrorCategory).toBe('no_pages')
    expect(row.retryable).toBe(true)
    expect(row.terminal).toBe(false)
  })

  it('retryAll passes include_source_failures=true', async () => {
    const dl = useDownloads()
    await dl.retryAll('failed')
    expect(retryAllCalls).toHaveLength(1)
    expect(retryAllCalls[0]).toEqual({ state: 'failed', include: true })
  })
})
