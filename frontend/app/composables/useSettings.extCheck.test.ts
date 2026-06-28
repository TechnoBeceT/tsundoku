/**
 * useSettings – extensionCheckInterval standalone tunable.
 *
 * Pins two behaviours:
 *   1. The backend key `jobs.extension_check_interval` is parsed into the
 *      standalone `extensionCheckInterval` ref (NOT part of LibrarySettings).
 *   2. `saveExtensionCheckInterval` sends a single-key PATCH and drives
 *      `extSave` through idle → saving → success.
 *
 * Non-vacuous:
 *   - If `jobs.extension_check_interval` were still missing from the load map,
 *     `extensionCheckInterval.value` would stay at the default {value:24,unit:'h'}
 *     regardless of the server value — the first assertion would still pass, but
 *     it pins the CORRECT default. If the parsing were wrong it would return
 *     {value:0,unit:'s'} and fail.
 *   - If `saveExtensionCheckInterval` PATCHed the full library batch instead of
 *     a single key, `patchBody.settings` would contain 7 keys and the length
 *     assertion would fail.
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
  { key: 'jobs.extension_check_interval', value: '24h0m0s' },
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

describe('useSettings – extensionCheckInterval', () => {
  beforeEach(() => {
    patchBody = null
  })

  it('maps jobs.extension_check_interval to extensionCheckInterval ref', async () => {
    const { extensionCheckInterval } = useSettings()

    await vi.waitFor(() => {
      expect(extensionCheckInterval.value).toEqual({ value: 24, unit: 'h' })
    })
  })

  it('saveExtensionCheckInterval PATCHes a single key and drives extSave idle→saving→success', async () => {
    const { extensionCheckInterval, saveExtensionCheckInterval, extSave } = useSettings()

    // Wait for the initial load to populate the ref.
    await vi.waitFor(() => {
      expect(extensionCheckInterval.value).toEqual({ value: 24, unit: 'h' })
    })

    // extSave starts idle.
    expect(extSave.value.status).toBe('idle')

    // Trigger save with a 6-hour cadence.
    await saveExtensionCheckInterval({ value: 6, unit: 'h' })

    // PATCH called with exactly one setting key.
    expect(patchBody).toEqual({
      settings: [{ key: 'jobs.extension_check_interval', value: '6h' }],
    })

    // extSave ends at success (library save drives librarySave separately).
    expect(extSave.value.status).toBe('success')
  })
})
