/**
 * useSettings – metadataAutoIdentify standalone tunable.
 *
 * Pins two behaviours (mirrors useSettings.autoUpdateTrack.test.ts):
 *   1. The backend key `metadata.auto_identify` is parsed into the
 *      standalone `metadataAutoIdentify` boolean ref (NOT part of
 *      LibrarySettings).
 *   2. `saveMetadataAutoIdentify` sends a single-key PATCH and drives
 *      `metadataAutoIdentifySave` through idle → saving → success.
 *
 * Non-vacuous:
 *   - If `metadata.auto_identify` were missing from the load map,
 *     `metadataAutoIdentify.value` would stay at the default `true`
 *     regardless of the server's `false` fixture value below — the
 *     assertion would fail if the key mapping were broken.
 *   - If `saveMetadataAutoIdentify` PATCHed the full library batch instead
 *     of a single key, `patchBody.settings` would contain more than 1 key
 *     and the equality assertion would fail.
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
  { key: 'metadata.auto_identify', value: 'false' },
]

const SYSTEM_RESPONSE = {
  storageFolder: '/data/manga',
  serverPort: '4567',
  database: 'postgres@localhost/tsundoku',
}

// ── Call tracking ─────────────────────────────────────────────────────────────

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

describe('useSettings – metadataAutoIdentify', () => {
  beforeEach(() => {
    patchBody = null
  })

  it('maps metadata.auto_identify to the metadataAutoIdentify ref', async () => {
    const { metadataAutoIdentify } = useSettings()

    await vi.waitFor(() => {
      expect(metadataAutoIdentify.value).toBe(false)
    })
  })

  it('saveMetadataAutoIdentify PATCHes a single key and drives metadataAutoIdentifySave idle→saving→success', async () => {
    const { metadataAutoIdentify, saveMetadataAutoIdentify, metadataAutoIdentifySave } = useSettings()

    await vi.waitFor(() => {
      expect(metadataAutoIdentify.value).toBe(false)
    })

    expect(metadataAutoIdentifySave.value.status).toBe('idle')

    await saveMetadataAutoIdentify(true)

    expect(patchBody).toEqual({
      settings: [{ key: 'metadata.auto_identify', value: 'true' }],
    })

    expect(metadataAutoIdentifySave.value.status).toBe('success')
  })
})
