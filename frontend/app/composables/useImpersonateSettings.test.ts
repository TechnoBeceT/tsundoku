/**
 * useImpersonateSettings — GET/PUT against /api/impersonate (GAP-111,
 * Tsundoku-owned Chrome-fingerprint image-proxy config — its own endpoint).
 *
 * Pins:
 *   1. GET maps the flat ImpersonateSettings DTO onto the screen's
 *      ImpersonateConfig (no renames, no unit conversions — just enabled + url).
 *   2. save() PUTs /api/impersonate with the config mapped back to the wire
 *      shape, and drives impersonateSave through idle → saving → success.
 *   3. A save error surfaces the backend's { message } verbatim and does not
 *      clobber the still-loaded config.
 *
 * Non-vacuous: if the composable pointed at the wrong endpoint, the mocked
 * GET/PUT below — which only answer /api/impersonate — would return
 * { data: null }, and the config would stay at its all-default seed instead of
 * the fixture's `enabled: true` value.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useImpersonateSettings } from './useImpersonateSettings'

const SETTINGS_RESPONSE = {
  enabled: true,
  url: 'http://impersonate-gateway:8788',
}

let putBody: unknown = null
let putPath: string | null = null

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string) => {
      if (path === '/api/impersonate') return Promise.resolve({ data: SETTINGS_RESPONSE, error: null })
      return Promise.resolve({ data: null, error: null })
    }),
    PUT: vi.fn().mockImplementation((path: string, opts: { body: unknown }) => {
      putPath = path
      putBody = opts.body
      return Promise.resolve({ data: SETTINGS_RESPONSE, error: null })
    }),
    POST: vi.fn(),
    DELETE: vi.fn(),
    PATCH: vi.fn(),
    use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

describe('useImpersonateSettings', () => {
  beforeEach(() => {
    putBody = null
    putPath = null
  })

  it('maps the ImpersonateSettings DTO onto the screen config', async () => {
    const { config } = useImpersonateSettings()

    await vi.waitFor(() => {
      expect(config.value.enabled).toBe(true)
    })

    expect(config.value.url).toBe('http://impersonate-gateway:8788')
  })

  it('save() PUTs /api/impersonate with the wire-shaped config and drives impersonateSave to success', async () => {
    const { config, save, impersonateSave } = useImpersonateSettings()

    await vi.waitFor(() => {
      expect(config.value.enabled).toBe(true)
    })
    expect(impersonateSave.value.status).toBe('idle')

    await save({ enabled: false, url: 'http://other:8788' })

    expect(putPath).toBe('/api/impersonate')
    expect(putBody).toEqual({ enabled: false, url: 'http://other:8788' })
    expect(impersonateSave.value.status).toBe('success')

    // §16: reseeded from the authoritative response, not the local copy.
    expect(config.value.enabled).toBe(true)
  })

  it('a save error surfaces the backend message and leaves the loaded config untouched', async () => {
    const { apiClient } = await import('~/utils/api/client')
    vi.mocked(apiClient.PUT).mockResolvedValueOnce({
      data: undefined,
      error: { message: 'impersonate.url must be blank or a valid absolute http(s) URL' },
    } as never)

    const { config, save, impersonateSave } = useImpersonateSettings()
    await vi.waitFor(() => {
      expect(config.value.enabled).toBe(true)
    })

    await save({ ...config.value, url: 'not-a-url' })

    expect(impersonateSave.value).toEqual({
      status: 'error',
      message: 'impersonate.url must be blank or a valid absolute http(s) URL',
    })
    // The (rejected) edit never overwrote the loaded config.
    expect(config.value.url).toBe('http://impersonate-gateway:8788')
  })
})
