/**
 * useFlareSolverrSettings — GET/PATCH against /api/flaresolverr/settings
 * (QCAT-238, Tsundoku-owned FlareSolverr config — a SEPARATE endpoint from
 * useSuwayomiSettings.ts).
 *
 * Pins:
 *   1. GET maps the flat FlareSolverrSettings DTO onto the screen's
 *      FlareSolverrConfig, including the field renames (sessionName→session,
 *      asResponseFallback→fallback) and unit conversions (timeout seconds →
 *      DurationValue{unit:'s'}, sessionTtl minutes → DurationValue{unit:'m'}).
 *   2. save() PATCHes /api/flaresolverr/settings (NOT /api/suwayomi/settings)
 *      with the full config mapped back to the wire shape, and drives
 *      flareSolverrSave through idle → saving → success.
 *   3. A save error surfaces the backend's { message } verbatim and does not
 *      clobber the still-loaded config.
 *
 * Non-vacuous: if the composable still pointed at /api/suwayomi/settings (a
 * copy-paste of useSuwayomiSettings.ts), the mocked GET/PATCH below — which
 * only answer /api/flaresolverr/settings — would return { data: null }, and
 * the config would stay at its all-default seed instead of the fixture's
 * `enabled: true` value.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useFlareSolverrSettings } from './useFlareSolverrSettings'

const SETTINGS_RESPONSE = {
  enabled: true,
  url: 'http://flaresolverr:8191',
  timeout: 90,
  sessionName: 'tsundoku',
  sessionTtl: 30,
  asResponseFallback: true,
}

let patchBody: unknown = null
let patchPath: string | null = null

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string) => {
      if (path === '/api/flaresolverr/settings') return Promise.resolve({ data: SETTINGS_RESPONSE, error: null })
      return Promise.resolve({ data: null, error: null })
    }),
    PATCH: vi.fn().mockImplementation((path: string, opts: { body: unknown }) => {
      patchPath = path
      patchBody = opts.body
      return Promise.resolve({ data: SETTINGS_RESPONSE, error: null })
    }),
    POST: vi.fn(),
    DELETE: vi.fn(),
    PUT: vi.fn(),
    use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

describe('useFlareSolverrSettings', () => {
  beforeEach(() => {
    patchBody = null
    patchPath = null
  })

  it('maps the FlareSolverrSettings DTO onto the screen config, with field renames + unit conversions', async () => {
    const { config } = useFlareSolverrSettings()

    await vi.waitFor(() => {
      expect(config.value.enabled).toBe(true)
    })

    expect(config.value.url).toBe('http://flaresolverr:8191')
    expect(config.value.timeout).toEqual({ value: 90, unit: 's' })
    expect(config.value.session).toBe('tsundoku')
    expect(config.value.sessionTtl).toEqual({ value: 30, unit: 'm' })
    expect(config.value.fallback).toBe(true)
  })

  it('save() PATCHes /api/flaresolverr/settings with the wire-shaped config and drives flareSolverrSave to success', async () => {
    const { config, save, flareSolverrSave } = useFlareSolverrSettings()

    await vi.waitFor(() => {
      expect(config.value.enabled).toBe(true)
    })
    expect(flareSolverrSave.value.status).toBe('idle')

    await save({
      enabled: false,
      url: 'http://flaresolverr:8191',
      timeout: { value: 45, unit: 's' },
      session: 'other',
      sessionTtl: { value: 1, unit: 'h' },
      fallback: false,
    })

    expect(patchPath).toBe('/api/flaresolverr/settings')
    expect(patchBody).toEqual({
      enabled: false,
      url: 'http://flaresolverr:8191',
      timeout: 45,
      sessionName: 'other',
      sessionTtl: 60,
      asResponseFallback: false,
    })
    expect(flareSolverrSave.value.status).toBe('success')

    // §16: reseeded from the authoritative response, not the local copy.
    expect(config.value.enabled).toBe(true)
  })

  it('a save error surfaces the backend message and leaves the loaded config untouched', async () => {
    const { apiClient } = await import('~/utils/api/client')
    vi.mocked(apiClient.PATCH).mockResolvedValueOnce({
      data: undefined,
      error: { message: 'flaresolverr.url must be blank or a valid absolute http(s) URL' },
    } as never)

    const { config, save, flareSolverrSave } = useFlareSolverrSettings()
    await vi.waitFor(() => {
      expect(config.value.enabled).toBe(true)
    })

    await save({ ...config.value, url: 'not-a-url' })

    expect(flareSolverrSave.value).toEqual({
      status: 'error',
      message: 'flaresolverr.url must be blank or a valid absolute http(s) URL',
    })
    // The (rejected) edit never overwrote the loaded config.
    expect(config.value.url).toBe('http://flaresolverr:8191')
  })
})
