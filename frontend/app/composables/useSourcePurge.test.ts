/**
 * useSourcePurge — the two-phase source-purge flow (preview → confirm).
 *
 * Pins four behaviours:
 *   1. start(source) opens the modal and loads the dry-run preview
 *      (GET /api/engine/purge-source/preview with the source identity as query).
 *   2. confirm() POSTs /api/engine/purge-source with {sourceId, sourceName},
 *      resolves TRUE, and closes the modal on success.
 *   3. A failed preview surfaces in `error` and leaves the modal open.
 *   4. A failed purge surfaces the server's message in `error`, resolves FALSE,
 *      and keeps the modal open (so the owner can retry or cancel).
 *
 * Non-vacuous: if start() dropped the query, the GET path branch below would miss
 * and preview would stay null; if confirm() didn't post, postCount would stay 0;
 * if the error paths swallowed failures, `error` would stay null and confirm()
 * would wrongly resolve true.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useSourcePurge } from './useSourcePurge'

const PREVIEW = {
  sourceId: '100',
  sourceName: 'Lunar Manga',
  seriesAffected: 3,
  providers: 3,
  providerChapters: 240,
  chaptersDeleted: 2,
  metrics: 1,
  breaker: 1,
}

let getCount = 0
let postCount = 0
let lastPostBody: unknown = null
let getError = false
let postError = false

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string) => {
      if (path === '/api/engine/purge-source/preview') {
        getCount++
        if (getError) return Promise.resolve({ data: null, error: { message: 'preview boom' } })
        return Promise.resolve({ data: PREVIEW, error: null })
      }
      return Promise.resolve({ data: null, error: null })
    }),
    POST: vi.fn().mockImplementation((path: string, opts: { body: unknown }) => {
      if (path === '/api/engine/purge-source') {
        postCount++
        lastPostBody = opts.body
        if (postError) return Promise.resolve({ data: null, error: { message: 'purge boom' } })
        return Promise.resolve({ data: { providersRemoved: 3 }, error: null })
      }
      return Promise.resolve({ data: null, error: null })
    }),
    PATCH: vi.fn(),
    DELETE: vi.fn(),
    PUT: vi.fn(),
    use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

describe('useSourcePurge', () => {
  beforeEach(() => {
    getCount = 0
    postCount = 0
    lastPostBody = null
    getError = false
    postError = false
  })

  it('start() opens the modal and loads the dry-run preview', async () => {
    const { start, open, preview, previewing } = useSourcePurge()

    await start({ id: '100', name: 'Lunar Manga' })

    expect(open.value).toBe(true)
    expect(previewing.value).toBe(false)
    expect(getCount).toBe(1)
    expect(preview.value?.providerChapters).toBe(240)
    expect(preview.value?.seriesAffected).toBe(3)
  })

  it('confirm() POSTs the identity, resolves true, and closes on success', async () => {
    const { start, confirm, open } = useSourcePurge()

    await start({ id: '100', name: 'Lunar Manga' })
    const ok = await confirm()

    expect(ok).toBe(true)
    expect(postCount).toBe(1)
    expect(lastPostBody).toEqual({ sourceId: '100', sourceName: 'Lunar Manga' })
    expect(open.value).toBe(false)
  })

  it('surfaces a failed preview and keeps the modal open', async () => {
    getError = true
    const { start, error, open, preview } = useSourcePurge()

    await start({ id: '100', name: 'Lunar Manga' })

    expect(error.value).toBe('preview boom')
    expect(open.value).toBe(true)
    expect(preview.value).toBeNull()
  })

  it('surfaces a failed purge, resolves false, and keeps the modal open', async () => {
    postError = true
    const { start, confirm, error, open } = useSourcePurge()

    await start({ id: '100', name: 'Lunar Manga' })
    const ok = await confirm()

    expect(ok).toBe(false)
    expect(error.value).toBe('purge boom')
    expect(open.value).toBe(true)
  })
})
