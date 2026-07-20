/**
 * useDownloads — the E1 read-model fields survive the mapper, and download.fail
 * triggers a refetch.
 *
 * Pins:
 *   1. attempts / maxRetries / isUpgrade / waitingReason map through to the item,
 *      with backend "" waitingReason → undefined (a ready row shows no chip).
 *   2. A live download.fail SSE event refetches the list + counts (so a newly-failed
 *      chapter and its error appear without waiting for cycle.done).
 *
 * Non-vacuous: drop the E1 fields from mapItem and test 1 fails; drop the
 * on('download.fail') listener and test 2 sees no extra GET.
 */
import { describe, it, expect, vi, beforeAll } from 'vitest'
import { useDownloads } from './useDownloads'
import { useProgressStream } from './useProgressStream'

const WAITING_ID = '00000000-0000-0000-0000-000000000001'
const READY_ID = '00000000-0000-0000-0000-000000000002'
const FUTURE = new Date(Date.now() + 20 * 60_000).toISOString()

const makeDto = (over: Record<string, unknown>) => ({
  id: '00000000-0000-0000-0000-000000000000',
  seriesId: '00000000-0001-0000-0000-000000000001',
  seriesTitle: 'Berserk',
  seriesCategory: 'Manga' as const,
  seriesCoverUrl: '',
  chapterKey: 'ch-1',
  number: 1,
  name: 'Chapter 1',
  state: 'upgrade_available',
  provider: '2499283573021220255',
  providerName: 'Comix',
  attempts: 0,
  maxRetries: 3,
  isUpgrade: true,
  upgradeTarget: 'Asura Scans',
  waitingReason: '',
  deferredUntil: null,
  deferReason: '',
  retries: 0,
  nextAttemptAt: null,
  lastError: '',
  errorCategory: '',
  filename: '',
  pageCount: null,
  downloadDate: null,
  ...over,
})

let getCount = 0

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string, options?: { params?: { query?: Record<string, unknown> } }) => {
      if (path !== '/api/downloads') return Promise.resolve({ data: null, error: null })
      getCount++
      if ((options?.params?.query?.limit as number | undefined) === 1) {
        return Promise.resolve({ data: { total: 2, items: [] }, error: null })
      }
      return Promise.resolve({
        data: {
          total: 2,
          items: [
            makeDto({ id: WAITING_ID, attempts: 2, waitingReason: 'cooling_down', deferredUntil: FUTURE, deferReason: 'rate limited' }),
            makeDto({ id: READY_ID, attempts: 0, waitingReason: '', deferredUntil: null }),
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

// ── EventSource stub ──────────────────────────────────────────────────────────
interface StubSource { fire: (name: string, payload: unknown) => void }
let stubSource: StubSource | null = null

class FakeEventSource {
  onopen: ((ev: Event) => void) | null = null
  onerror: ((ev: Event) => void) | null = null
  private _handlers = new Map<string, ((ev: Event) => void)[]>()
  constructor(_url: string) {
    const handlers = this._handlers
    const onOpenRef = (): void => { this.onopen?.(new Event('open')) }
    stubSource = {
      fire(name: string, payload: unknown) {
        const ev = { data: JSON.stringify(payload) } as MessageEvent
        ;(handlers.get(name) ?? []).forEach((h) => h(ev))
      },
    }
    queueMicrotask(onOpenRef)
  }

  addEventListener(name: string, handler: (ev: Event) => void): void {
    if (!this._handlers.has(name)) this._handlers.set(name, [])
    this._handlers.get(name)!.push(handler)
  }

  removeEventListener(): void { void 0 }
  close(): void { stubSource = null }
}

describe('useDownloads – E1 fields + download.fail', () => {
  beforeAll(() => {
    vi.stubGlobal('EventSource', FakeEventSource)
    useProgressStream().connect()
  })

  it('maps attempts / maxRetries / isUpgrade / waitingReason', async () => {
    const dl = useDownloads()
    await dl.refresh()

    const waiting = dl.items.value.find((i) => i.chapterId === WAITING_ID)
    const ready = dl.items.value.find((i) => i.chapterId === READY_ID)

    expect(waiting?.attempts).toBe(2)
    expect(waiting?.maxRetries).toBe(3)
    expect(waiting?.isUpgrade).toBe(true)
    expect(waiting?.waitingReason).toBe('cooling_down')
    // Backend "" (ready) → undefined so the row shows no waiting chip.
    expect(ready?.waitingReason).toBeUndefined()
  })

  it('refetches on a live download.fail SSE event', async () => {
    const dl = useDownloads()
    await dl.refresh()
    await vi.waitFor(() => expect(stubSource).not.toBeNull())

    const before = getCount
    stubSource!.fire('download.fail', { chapter_id: WAITING_ID, state: 'failed', error: 'boom' })
    // refresh() = 1 page fetch + 3 count probes.
    await vi.waitFor(() => expect(getCount).toBeGreaterThan(before))
  })
})
