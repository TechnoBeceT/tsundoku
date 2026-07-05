/**
 * useSettings – downloadConcurrency (jobs.download_concurrency).
 *
 * Mirrors useSettings.extCheck.test.ts's structure, but pins the
 * `jobs.download_concurrency` → `LibrarySettings.downloadConcurrency` mapping
 * (part of the batched library settings, unlike the standalone extCheck
 * tunable — so this asserts through `saveLibrary`, not a single-key save).
 *
 * Non-vacuous:
 *   - If `jobs.download_concurrency` were missing from the load map, the ref
 *     would silently keep the DEFAULTS.downloadConcurrency fallback (5) instead
 *     of the fixture's 7 — the first assertion pins the fixture value, not the
 *     default, so a dropped mapping fails it.
 *   - If `saveLibrary` omitted the key from its PATCH batch, the `patchBody`
 *     assertion (which checks the full 6-key settings array) would fail.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useSettings } from './useSettings'

// ── Settings response fixtures ────────────────────────────────────────────────

const BASE_SETTINGS = [
  { key: 'jobs.refresh_interval', value: '2h0m0s' },
  { key: 'jobs.download_interval', value: '15m0s' },
  { key: 'jobs.retry_backoff', value: '1m0s' },
  { key: 'jobs.max_retries', value: '3' },
  { key: 'health.stale_grace_days', value: '14' },
  { key: 'jobs.refresh_concurrency', value: '4' },
  { key: 'jobs.download_concurrency', value: '7' },
]

const SYSTEM_RESPONSE = {
  storageFolder: '/data/manga',
  serverPort: '4567',
  database: 'postgres@localhost/tsundoku',
}

// ── Call tracking ─────────────────────────────────────────────────────────────

// Captured PATCH body — reassigned per call so beforeEach can reset it.
let patchBody: unknown = null

// ── Module mock ───────────────────────────────────────────────────────────────

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string) => {
      if (path === '/api/settings') return Promise.resolve({ data: BASE_SETTINGS, error: null })
      if (path === '/api/system') return Promise.resolve({ data: SYSTEM_RESPONSE, error: null })
      return Promise.resolve({ data: null, error: null })
    }),
    PATCH: vi.fn().mockImplementation((_path: string, opts: { body: unknown }) => {
      patchBody = opts.body
      // §16: return the authoritative updated list so the composable can reseed.
      return Promise.resolve({ data: BASE_SETTINGS, error: null })
    }),
    POST: vi.fn(),
    DELETE: vi.fn(),
    PUT: vi.fn(),
    use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('useSettings – downloadConcurrency', () => {
  beforeEach(() => {
    patchBody = null
  })

  it('maps jobs.download_concurrency to library.downloadConcurrency', async () => {
    const { library } = useSettings()

    await vi.waitFor(() => {
      expect(library.value.downloadConcurrency).toBe(7)
    })
  })

  it('saveLibrary includes jobs.download_concurrency in the PATCH batch', async () => {
    const { library, saveLibrary, librarySave } = useSettings()

    // Wait for the initial load so the edited copy starts from live values.
    await vi.waitFor(() => {
      expect(library.value.downloadConcurrency).toBe(7)
    })

    await saveLibrary({ ...library.value, downloadConcurrency: 12 })

    expect(patchBody).toEqual({
      settings: [
        { key: 'jobs.refresh_interval', value: '2h' },
        { key: 'jobs.download_interval', value: '15m' },
        { key: 'jobs.retry_backoff', value: '1m' },
        { key: 'jobs.max_retries', value: '3' },
        { key: 'health.stale_grace_days', value: '14' },
        { key: 'jobs.refresh_concurrency', value: '4' },
        { key: 'jobs.download_concurrency', value: '12' },
      ],
    })

    expect(librarySave.value.status).toBe('success')
    // §16: reseeds from the authoritative response, not the local copy — the
    // mocked PATCH returns BASE_SETTINGS (download_concurrency: '7').
    expect(library.value.downloadConcurrency).toBe(7)
  })
})
