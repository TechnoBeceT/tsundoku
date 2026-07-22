/**
 * useSourceless — the data layer for the library Sourceless page.
 *
 * Pins the three behaviours the screen + cleanup dialog depend on:
 *   1. the initial load maps the { series } envelope onto SeriesSourceless[];
 *   2. fetchPreview maps the per-series removable preview for the dialog;
 *   3. removeSourceless POSTs the ticked ids and re-polls the list.
 * Plus §16: a failed removal surfaces in removeError (never swallowed).
 *
 * Non-vacuous: drop the re-poll in removeSourceless and test 3's second GET
 * assertion fails; swallow the POST error and the error-surfacing test fails.
 *
 * vi.mock is hoisted; the factory closes over the mutable bindings below, which
 * are set before any mocked method is actually invoked.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useSourceless } from './useSourceless'

// ---- Mutable call tracking (reassigned per test) ----------------------------
let getCalls: { path: string; id?: string }[] = []
let postCalls: { id?: string; chapterIds?: string[] }[] = []
let postShouldError = false
let previewShouldBeMalformed = false

const listRow = {
  seriesId: 's-1',
  title: 'Alpha',
  displayName: 'Alpha (EN)',
  category: 'Manga',
  coverUrl: '/api/series/s-1/cover?v=abc',
  sourcelessCount: 4,
}

const previewBody = {
  chapters: [
    { chapterId: 'c-1', number: 5, pageCount: 20, provider: '', filename: '5.cbz' },
  ],
}

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string, opts?: { params?: { path?: { id?: string } } }) => {
      getCalls.push({ path, id: opts?.params?.path?.id })
      if (path === '/api/library/sourceless') {
        return Promise.resolve({ data: { series: [listRow] }, error: null })
      }
      if (path === '/api/series/{id}/sourceless-cleanup') {
        if (previewShouldBeMalformed) return Promise.resolve({ data: {}, error: null })
        return Promise.resolve({ data: previewBody, error: null })
      }
      return Promise.resolve({ data: null, error: { message: 'unexpected GET ' + path } })
    }),
    PATCH: vi.fn(),
    POST: vi.fn().mockImplementation((_path: string, opts: { params: { path: { id: string } }; body: { chapterIds: string[] } }) => {
      postCalls.push({ id: opts.params.path.id, chapterIds: opts.body.chapterIds })
      if (postShouldError) return Promise.resolve({ data: null, error: { message: 'boom' } })
      return Promise.resolve({ data: { removed: opts.body.chapterIds.length }, error: null })
    }),
    use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

beforeEach(() => {
  getCalls = []
  postCalls = []
  postShouldError = false
  previewShouldBeMalformed = false
})

describe('useSourceless', () => {
  it('maps the { series } envelope onto SeriesSourceless[]', async () => {
    const { series } = useSourceless()
    await vi.waitFor(() => expect(series.value.length).toBe(1))
    expect(series.value[0]).toEqual({
      seriesId: 's-1',
      title: 'Alpha',
      displayName: 'Alpha (EN)',
      category: 'Manga',
      coverUrl: '/api/series/s-1/cover?v=abc',
      sourcelessCount: 4,
    })
  })

  it('fetchPreview maps the removable-chapter preview for the dialog', async () => {
    const { fetchPreview } = useSourceless()
    const preview = await fetchPreview('s-1')
    expect(preview).toEqual({
      chapters: [
        { chapterId: 'c-1', number: 5, pageCount: 20, provider: '', filename: '5.cbz' },
      ],
    })
    expect(getCalls.some((c) => c.path === '/api/series/{id}/sourceless-cleanup' && c.id === 's-1')).toBe(true)
  })

  it('fetchPreview resolves null on a malformed response (shape guard)', async () => {
    previewShouldBeMalformed = true
    const { fetchPreview } = useSourceless()
    const preview = await fetchPreview('s-1')
    expect(preview).toBeNull()
  })

  it('removeSourceless POSTs the ticked ids and re-polls the list', async () => {
    const { series, removeSourceless } = useSourceless()
    await vi.waitFor(() => expect(series.value.length).toBe(1))
    getCalls = []

    const ok = await removeSourceless('s-1', ['c-1'])
    expect(ok).toBe(true)
    expect(postCalls).toEqual([{ id: 's-1', chapterIds: ['c-1'] }])
    expect(getCalls.some((c) => c.path === '/api/library/sourceless')).toBe(true)
  })

  it('surfaces a failed removal in removeError (§16, never swallowed)', async () => {
    postShouldError = true
    const { series, removeSourceless, removeError } = useSourceless()
    await vi.waitFor(() => expect(series.value.length).toBe(1))

    const ok = await removeSourceless('s-1', ['c-1'])
    expect(ok).toBe(false)
    expect(removeError.value).toBe('boom')
  })
})
