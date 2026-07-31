/**
 * useDownloads — the early-access DTO fields survive the mapper (GAP-141).
 *
 * Pins that a chapter a source is WITHHOLDING behind a paywall carries `locked` +
 * the raw `lockedUntil` timestamp through to the screen item (so the row can render
 * "Early access · free ~3d" instead of a red failure badge), while an ordinary
 * failure maps both to their absent forms.
 *
 * Non-vacuous: drop either field from mapItem and the withheld row's assertion
 * fails; map `lockedUntil` verbatim (without the null → undefined coercion) and the
 * broken row's assertion fails.
 */
import { describe, it, expect, vi } from 'vitest'
import { useDownloads } from './useDownloads'

const makeDto = (id: string, locked: boolean, lockedUntil: string | null) => ({
  id,
  seriesId: '00000000-0001-0000-0000-000000000001',
  seriesTitle: 'Coin Gate',
  seriesCategory: 'Manhwa' as const,
  seriesCoverUrl: '',
  chapterKey: `cg-${id.slice(-1)}`,
  number: 12,
  name: 'Chapter 12',
  state: 'failed',
  provider: '42',
  providerName: 'Hive Scans',
  locked,
  lockedUntil,
  deferredUntil: null,
  deferReason: '',
  retries: 0,
  nextAttemptAt: null,
  lastError: locked ? 'Chapter locked, coins required' : 'connection reset by peer',
  errorCategory: '',
  filename: '',
  pageCount: null,
  downloadDate: null,
})

const WITHHELD_ID = '00000000-0000-0000-0000-000000000001'
const BROKEN_ID = '00000000-0000-0000-0000-000000000002'
const FREE_AT = new Date(Date.now() + 60 * 3_600_000).toISOString()

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string, options?: { params?: { query?: Record<string, unknown> } }) => {
      if (path !== '/api/downloads') return Promise.resolve({ data: null, error: null })
      if ((options?.params?.query?.limit as number | undefined) === 1) {
        return Promise.resolve({ data: { total: 2, items: [] }, error: null })
      }
      return Promise.resolve({
        data: {
          total: 2,
          items: [makeDto(WITHHELD_ID, true, FREE_AT), makeDto(BROKEN_ID, false, null)],
        },
        error: null,
      })
    }),
    POST: vi.fn(),
    PATCH: vi.fn(),
    DELETE: vi.fn(),
    use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

describe('useDownloads – early access', () => {
  it('carries locked + lockedUntil for a withheld chapter and omits them otherwise', async () => {
    const dl = useDownloads()
    await dl.refresh()

    const withheld = dl.items.value.find((i) => i.chapterId === WITHHELD_ID)
    const broken = dl.items.value.find((i) => i.chapterId === BROKEN_ID)

    expect(withheld?.locked).toBe(true)
    expect(withheld?.lockedUntil).toBe(FREE_AT)
    expect(broken?.locked).toBe(false)
    expect(broken?.lockedUntil).toBeUndefined()
  })
})
