/**
 * useDownloads – live per-page progress via the download.progress SSE event.
 *
 * Pins that when download.progress fires, useDownloads updates the matching item
 * IN PLACE (progress % + page counter) with NO refetch, and safely ignores an
 * unknown chapter / a total:0 event.
 *
 * Non-vacuous: if the on('download.progress', …) handler were removed, the item's
 * progress would stay undefined and the assertion would fail; if the handler
 * refetched instead of mutating in place, the "no extra GET" assertion would fail.
 *
 * Uses the FakeEventSource pattern from useExtensions.refetch.test.ts, extended so
 * `fire(name, payload)` can carry a JSON body (download.progress needs a real
 * {chapter_id, current, total} payload, not the empty {} the shared stub sends).
 */
import { describe, it, expect, vi, beforeAll } from 'vitest'
import { useDownloads } from './useDownloads'
import { useProgressStream } from './useProgressStream'

// ── Mock data ─────────────────────────────────────────────────────────────────

const CHAPTER_ID = '00000000-0000-0000-0000-000000000001'

const makeDto = (id: string, state = 'downloading') => ({
  id,
  seriesId: '00000000-0001-0000-0000-000000000001',
  seriesTitle: 'Series 1',
  seriesCategory: 'Manga' as const,
  seriesCoverUrl: '',
  chapterKey: 'ch-1',
  number: 1,
  name: 'Chapter 1',
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

// ── Call tracking ─────────────────────────────────────────────────────────────

let getCount = 0

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string, options?: { params?: { query?: Record<string, unknown> } }) => {
      if (path !== '/api/downloads') return Promise.resolve({ data: null, error: null })
      getCount++
      const limit = options?.params?.query?.limit as number | undefined
      // Count probes (limit:1) return totals only.
      if (limit === 1) return Promise.resolve({ data: { total: 1, items: [] }, error: null })
      // The active-tab page fetch returns one downloading chapter.
      return Promise.resolve({ data: { total: 1, items: [makeDto(CHAPTER_ID)] }, error: null })
    }),
    POST: vi.fn(),
    PATCH: vi.fn(),
    DELETE: vi.fn(),
    use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

// ── EventSource stub (carries a JSON payload) ─────────────────────────────────

interface StubSource {
  fire: (name: string, payload: unknown) => void
}

let stubSource: StubSource | null = null

class FakeEventSource {
  onopen: ((ev: Event) => void) | null = null
  onerror: ((ev: Event) => void) | null = null

  private _handlers = new Map<string, ((ev: Event) => void)[]>()

  constructor(_url: string) {
    const handlers = this._handlers
    const onOpenRef = () => this.onopen?.(new Event('open'))

    stubSource = {
      fire(name: string, payload: unknown) {
        const ev = { data: JSON.stringify(payload) } as MessageEvent
        ;(handlers.get(name) ?? []).forEach(h => h(ev))
      },
    }
    queueMicrotask(onOpenRef)
  }

  addEventListener(name: string, handler: (ev: Event) => void): void {
    if (!this._handlers.has(name)) this._handlers.set(name, [])
    this._handlers.get(name)!.push(handler)
  }

  removeEventListener(_name?: string, _handler?: (ev: Event) => void): void { void 0 }
  close(): void { stubSource = null }
}

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('useDownloads – live download.progress', () => {
  beforeAll(() => {
    vi.stubGlobal('EventSource', FakeEventSource)
    // Connect the singleton stream so the stub exists before useDownloads subscribes.
    useProgressStream().connect()
  })

  it('updates the matching item in place (no refetch) on download.progress', async () => {
    const { loading, items } = useDownloads()
    await vi.waitFor(() => expect(loading.value).toBe(false))
    await vi.waitFor(() => expect(stubSource).not.toBeNull())

    const getAfterLoad = getCount

    stubSource!.fire('download.progress', { chapter_id: CHAPTER_ID, current: 12, total: 40, state: 'downloading' })

    await vi.waitFor(() => {
      const row = items.value.find(i => i.chapterId === CHAPTER_ID)
      expect(row?.progress).toBe(30) // round(100 * 12 / 40)
    })
    const row = items.value.find(i => i.chapterId === CHAPTER_ID)!
    expect(row.pagesCurrent).toBe(12)
    expect(row.pagesTotal).toBe(40)

    // In-place update — no extra GET was issued.
    expect(getCount).toBe(getAfterLoad)
  })

  it('ignores an unknown chapter_id and a total:0 event without throwing', async () => {
    const { loading, items } = useDownloads()
    await vi.waitFor(() => expect(loading.value).toBe(false))
    await vi.waitFor(() => expect(stubSource).not.toBeNull())

    // Unknown chapter → no-op, no throw.
    expect(() =>
      stubSource!.fire('download.progress', { chapter_id: 'ffffffff-0000-0000-0000-000000000000', current: 5, total: 10, state: 'downloading' }),
    ).not.toThrow()

    // total:0 → must not set progress (no divide-by-zero) and must not throw.
    expect(() =>
      stubSource!.fire('download.progress', { chapter_id: CHAPTER_ID, current: 0, total: 0, state: 'downloading' }),
    ).not.toThrow()

    const row = items.value.find(i => i.chapterId === CHAPTER_ID)!
    expect(row.pagesTotal).not.toBe(0)
  })
})
