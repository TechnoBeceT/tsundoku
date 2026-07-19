/**
 * useFractionals — the data layer for the library Fractionals page.
 *
 * Pins the four behaviours the screen + cleanup dialog depend on:
 *   1. the initial load maps the { series } envelope onto SeriesFractionals[];
 *   2. setIgnoreForSeries PATCHes the whole-series ignore-fractional route with the
 *      right body AND re-polls the list (so both counts refresh);
 *   3. fetchPreview maps the per-series removable preview for the dialog;
 *   4. removeFractionals POSTs the ticked ids and re-polls the list.
 * Plus §16: a failed toggle surfaces in toggleError (never swallowed).
 *
 * Non-vacuous: drop the re-poll in setIgnoreForSeries and test 2's second GET
 * assertion fails; swallow the PATCH error and the error-surfacing test fails.
 *
 * vi.mock is hoisted; the factory closes over the mutable bindings below, which
 * are set before any mocked method is actually invoked.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useFractionals } from './useFractionals'

// ---- Mutable call tracking (reassigned per test) ----------------------------
let getCalls: { path: string; id?: string }[] = []
let patchCalls: { id?: string; ignore?: boolean }[] = []
let postCalls: { id?: string; chapterIds?: string[] }[] = []
let patchShouldError = false

const listRow = {
  seriesId: 's-1',
  title: 'Alpha',
  displayName: 'Alpha (EN)',
  category: 'Manga',
  coverUrl: '/api/series/s-1/cover?v=abc',
  fractionalCount: 6,
  removableCount: 5,
  providersTotal: 3,
  providersIgnoring: 2,
  allProvidersIgnoring: false,
}

const previewBody = {
  typicalPageCount: 96,
  chapters: [
    { chapterId: 'c-1', number: 5.1, pageCount: 2, provider: 'kaliscan', filename: '5.1.cbz' },
  ],
}

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string, opts?: { params?: { path?: { id?: string } } }) => {
      getCalls.push({ path, id: opts?.params?.path?.id })
      if (path === '/api/library/fractionals') {
        return Promise.resolve({ data: { series: [listRow] }, error: null })
      }
      if (path === '/api/series/{id}/fractional-cleanup') {
        return Promise.resolve({ data: previewBody, error: null })
      }
      return Promise.resolve({ data: null, error: { message: 'unexpected GET ' + path } })
    }),
    PATCH: vi.fn().mockImplementation((_path: string, opts: { params: { path: { id: string } }; body: { ignoreFractional: boolean } }) => {
      patchCalls.push({ id: opts.params.path.id, ignore: opts.body.ignoreFractional })
      if (patchShouldError) return Promise.resolve({ data: null, error: { message: 'boom' } })
      return Promise.resolve({ data: { id: opts.params.path.id }, error: null })
    }),
    POST: vi.fn().mockImplementation((_path: string, opts: { params: { path: { id: string } }; body: { chapterIds: string[] } }) => {
      postCalls.push({ id: opts.params.path.id, chapterIds: opts.body.chapterIds })
      return Promise.resolve({ data: { removed: opts.body.chapterIds.length }, error: null })
    }),
    use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

beforeEach(() => {
  getCalls = []
  patchCalls = []
  postCalls = []
  patchShouldError = false
})

describe('useFractionals', () => {
  it('maps the { series } envelope onto SeriesFractionals[]', async () => {
    const { series } = useFractionals()
    await vi.waitFor(() => expect(series.value.length).toBe(1))
    expect(series.value[0]).toEqual({
      seriesId: 's-1',
      title: 'Alpha',
      displayName: 'Alpha (EN)',
      category: 'Manga',
      coverUrl: '/api/series/s-1/cover?v=abc',
      fractionalCount: 6,
      removableCount: 5,
      providersTotal: 3,
      providersIgnoring: 2,
      allProvidersIgnoring: false,
    })
  })

  it('setIgnoreForSeries PATCHes the whole-series route and re-polls the list', async () => {
    const { series, setIgnoreForSeries } = useFractionals()
    await vi.waitFor(() => expect(series.value.length).toBe(1))
    getCalls = [] // ignore the initial load; measure the refetch only

    const ok = await setIgnoreForSeries('s-1', true)
    expect(ok).toBe(true)
    expect(patchCalls).toEqual([{ id: 's-1', ignore: true }])
    // The list is re-polled so removable/fractional counts refresh.
    expect(getCalls.some((c) => c.path === '/api/library/fractionals')).toBe(true)
  })

  it('surfaces a failed toggle in toggleError (§16, never swallowed)', async () => {
    patchShouldError = true
    const { series, setIgnoreForSeries, toggleError } = useFractionals()
    await vi.waitFor(() => expect(series.value.length).toBe(1))

    const ok = await setIgnoreForSeries('s-1', true)
    expect(ok).toBe(false)
    expect(toggleError.value).toBe('boom')
  })

  it('fetchPreview maps the removable-chapter preview for the dialog', async () => {
    const { fetchPreview } = useFractionals()
    const preview = await fetchPreview('s-1')
    expect(preview).toEqual({
      typicalPageCount: 96,
      chapters: [
        { chapterId: 'c-1', number: 5.1, pageCount: 2, provider: 'kaliscan', filename: '5.1.cbz' },
      ],
    })
    expect(getCalls.some((c) => c.path === '/api/series/{id}/fractional-cleanup' && c.id === 's-1')).toBe(true)
  })

  it('removeFractionals POSTs the ticked ids and re-polls the list', async () => {
    const { series, removeFractionals } = useFractionals()
    await vi.waitFor(() => expect(series.value.length).toBe(1))
    getCalls = []

    const ok = await removeFractionals('s-1', ['c-1'])
    expect(ok).toBe(true)
    expect(postCalls).toEqual([{ id: 's-1', chapterIds: ['c-1'] }])
    expect(getCalls.some((c) => c.path === '/api/library/fractionals')).toBe(true)
  })
})
