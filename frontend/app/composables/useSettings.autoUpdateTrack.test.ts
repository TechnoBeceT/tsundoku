/**
 * useSettings – autoUpdateTrack standalone tunable.
 *
 * Pins two behaviours:
 *   1. The backend key `trackers.auto_update_track` is parsed into the
 *      standalone `autoUpdateTrack` boolean ref (NOT part of LibrarySettings).
 *   2. `saveAutoUpdateTrack` sends a single-key PATCH and drives
 *      `autoUpdateTrackSave` through idle → saving → success.
 *
 * Non-vacuous:
 *   - If `trackers.auto_update_track` were still missing from the load map,
 *     `autoUpdateTrack.value` would stay at the default `false` regardless of
 *     the server value — the "true" fixture below would fail the assertion if
 *     the key mapping were broken.
 *   - If `saveAutoUpdateTrack` PATCHed the full library batch instead of a
 *     single key, `patchBody.settings` would contain more than 1 key and the
 *     equality assertion would fail.
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
  { key: 'jobs.download_concurrency', value: '5' },
  { key: 'trackers.auto_update_track', value: 'true' },
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

describe('useSettings – autoUpdateTrack', () => {
  beforeEach(() => {
    patchBody = null
  })

  it('maps trackers.auto_update_track to the autoUpdateTrack ref', async () => {
    const { autoUpdateTrack } = useSettings()

    await vi.waitFor(() => {
      expect(autoUpdateTrack.value).toBe(true)
    })
  })

  it('saveAutoUpdateTrack PATCHes a single key and drives autoUpdateTrackSave idle→saving→success', async () => {
    const { autoUpdateTrack, saveAutoUpdateTrack, autoUpdateTrackSave } = useSettings()

    // Wait for the initial load to populate the ref.
    await vi.waitFor(() => {
      expect(autoUpdateTrack.value).toBe(true)
    })

    // autoUpdateTrackSave starts idle.
    expect(autoUpdateTrackSave.value.status).toBe('idle')

    // Trigger save flipping it off.
    await saveAutoUpdateTrack(false)

    // PATCH called with exactly one setting key.
    expect(patchBody).toEqual({
      settings: [{ key: 'trackers.auto_update_track', value: 'false' }],
    })

    // autoUpdateTrackSave ends at success.
    expect(autoUpdateTrackSave.value.status).toBe('success')
  })
})
