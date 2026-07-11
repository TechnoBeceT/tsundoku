/**
 * useDownloads – the upgradeTarget DTO field survives the mapper.
 *
 * Pins that a chapter mid-upgrade carries the target source through to the screen
 * item (so the row can render "MangaDex → Asura Scans"), while the backend's ""
 * ("not upgrading / no nameable target") maps to `undefined` rather than an empty
 * arrow suffix.
 *
 * Non-vacuous: drop `upgradeTarget` from mapItem and the first assertion fails;
 * map it verbatim (without the `|| undefined`) and the second fails.
 */
import { describe, it, expect, vi } from 'vitest'
import { useDownloads } from './useDownloads'

const makeDto = (id: string, upgradeTarget: string, state = 'upgrading') => ({
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
  providerName: 'MangaDex',
  upgradeTarget,
  retries: 0,
  nextAttemptAt: null,
  lastError: '',
  errorCategory: '',
  filename: '',
  pageCount: null,
  downloadDate: null,
})

const UPGRADING_ID = '00000000-0000-0000-0000-000000000001'
const PLAIN_ID = '00000000-0000-0000-0000-000000000002'

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string, options?: { params?: { query?: Record<string, unknown> } }) => {
      if (path !== '/api/downloads') return Promise.resolve({ data: null, error: null })
      // Count probes (limit:1) return totals only.
      if ((options?.params?.query?.limit as number | undefined) === 1) {
        return Promise.resolve({ data: { total: 2, items: [] }, error: null })
      }
      return Promise.resolve({
        data: {
          total: 2,
          items: [
            makeDto(UPGRADING_ID, 'Asura Scans'),
            makeDto(PLAIN_ID, '', 'downloading'), // not upgrading → backend sends ""
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

describe('useDownloads – upgradeTarget', () => {
  it('carries the upgrade target for an upgrading chapter and omits it otherwise', async () => {
    const dl = useDownloads()
    await dl.refresh()

    const upgrading = dl.items.value.find((i) => i.chapterId === UPGRADING_ID)
    const plain = dl.items.value.find((i) => i.chapterId === PLAIN_ID)

    expect(upgrading?.upgradeTarget).toBe('Asura Scans')
    expect(plain?.upgradeTarget).toBeUndefined()
  })
})
