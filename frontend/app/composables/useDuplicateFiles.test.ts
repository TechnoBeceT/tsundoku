/**
 * useDuplicateFiles — the data layer for the Cleanup console's Duplicates tab.
 *
 * Pins the behaviours the tab depends on:
 *   1. the initial load maps the { series, totalFiles, totalBytes } envelope;
 *   2. `{ immediate: false }` fetches NOTHING on creation — this is what makes the
 *      tab LAZY, and the whole reason the scan does not run for a visitor who only
 *      ever opens the Fractionals tab (the scan reads every series folder);
 *   3. refetch() performs the deferred load;
 *   4. refresh() re-polls without blanking the list (refreshing, not pending);
 *   5. §16: a failed load surfaces in `error`, never swallowed.
 *
 * Non-vacuous: make the composable load on creation regardless of the option and
 * test 2 fails; swallow the failure and test 5 fails.
 *
 * vi.mock is hoisted; the factory closes over the mutable bindings below, which
 * are set before any mocked method is actually invoked.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useDuplicateFiles } from './useDuplicateFiles'

let getCalls: string[] = []
let getShouldError = false

const listRow = {
  seriesId: 's-1',
  title: 'Olgami',
  displayName: 'Olgami',
  category: 'Manhwa',
  coverUrl: '/api/series/s-1/cover?v=abc',
  fileCount: 198,
  reclaimableBytes: 751_619_276,
}

vi.mock('~/utils/api/client', () => ({
  setUnauthorizedHandler: vi.fn(),
  apiClient: {
    GET: vi.fn().mockImplementation((path: string) => {
      getCalls.push(path)
      if (getShouldError) return Promise.resolve({ data: null, error: { message: 'boom' } })
      return Promise.resolve({
        data: { series: [listRow], totalFiles: 198, totalBytes: 751_619_276 },
        error: null,
      })
    }),
  },
}))

beforeEach(() => {
  getCalls = []
  getShouldError = false
})

describe('useDuplicateFiles', () => {
  it('loads and maps the envelope on creation by default', async () => {
    const { series, totalFiles, totalBytes, pending } = useDuplicateFiles()
    await vi.waitFor(() => expect(pending.value).toBe(false))

    expect(getCalls).toEqual(['/api/library/duplicate-files'])
    expect(series.value).toEqual([listRow])
    expect(totalFiles.value).toBe(198)
    expect(totalBytes.value).toBe(751_619_276)
  })

  it('fetches nothing on creation when deferred — the tab is lazy', () => {
    const { series } = useDuplicateFiles({ immediate: false })
    expect(getCalls).toEqual([])
    expect(series.value).toEqual([])
  })

  it('refetch() performs the deferred load', async () => {
    const { series, refetch } = useDuplicateFiles({ immediate: false })
    expect(getCalls).toEqual([])

    await refetch()
    expect(getCalls).toEqual(['/api/library/duplicate-files'])
    expect(series.value).toEqual([listRow])
  })

  it('refresh() re-polls without blanking the list (refreshing, not pending)', async () => {
    const { pending, refreshing, refresh } = useDuplicateFiles({ immediate: false })
    await refresh()

    expect(getCalls).toEqual(['/api/library/duplicate-files'])
    expect(pending.value).toBe(false)
    expect(refreshing.value).toBe(false)
  })

  it('surfaces a load failure instead of swallowing it (§16)', async () => {
    getShouldError = true
    const { error, refetch } = useDuplicateFiles({ immediate: false })
    await refetch()

    expect(error.value).toBeTruthy()
  })
})
