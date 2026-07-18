/**
 * useDownloads — the deferral DTO fields survive the mapper.
 *
 * Pins that a queued chapter whose source is on a persisted cooldown carries the raw
 * `deferredUntil` timestamp + `deferReason` through to the screen item (so the row
 * can render "waiting on X · retry ~Nm"), while a null/absent cooldown maps to
 * `undefined` (no waiting note).
 *
 * Non-vacuous: drop `deferredUntil` from mapItem and the first assertion fails; map
 * `deferReason` verbatim (without `|| undefined`) and the ready row's assertion fails.
 */
import { describe, it, expect, vi } from 'vitest'
import { useDownloads } from './useDownloads'

const makeDto = (id: string, deferredUntil: string | null, deferReason: string, state = 'upgrade_available') => ({
  id,
  seriesId: '00000000-0001-0000-0000-000000000001',
  seriesTitle: 'Berserk',
  seriesCategory: 'Manga' as const,
  seriesCoverUrl: '',
  chapterKey: 'ch-1',
  number: 365,
  name: 'Chapter 365',
  state,
  provider: '2499283573021220255',
  providerName: 'Comix',
  upgradeTarget: 'Asura Scans',
  deferredUntil,
  deferReason,
  retries: 0,
  nextAttemptAt: null,
  lastError: '',
  errorCategory: '',
  filename: '',
  pageCount: null,
  downloadDate: null,
})

const DEFERRED_ID = '00000000-0000-0000-0000-000000000001'
const READY_ID = '00000000-0000-0000-0000-000000000002'
const FUTURE = new Date(Date.now() + 20 * 60_000).toISOString()

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
          items: [
            makeDto(DEFERRED_ID, FUTURE, 'Cloudflare challenge failed (403)'),
            makeDto(READY_ID, null, ''), // source ready → no cooldown
          ],
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

describe('useDownloads – deferral', () => {
  it('carries deferredUntil + deferReason for a deferred chapter and omits them otherwise', async () => {
    const dl = useDownloads()
    await dl.refresh()

    const deferred = dl.items.value.find((i) => i.chapterId === DEFERRED_ID)
    const ready = dl.items.value.find((i) => i.chapterId === READY_ID)

    expect(deferred?.deferredUntil).toBe(FUTURE)
    expect(deferred?.deferReason).toBe('Cloudflare challenge failed (403)')
    expect(ready?.deferredUntil).toBeUndefined()
    expect(ready?.deferReason).toBeUndefined()
  })
})
