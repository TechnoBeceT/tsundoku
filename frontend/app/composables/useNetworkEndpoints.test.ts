/**
 * useNetworkEndpoints — CRUD against /api/network/endpoints (per-source network
 * routing, QCAT-283 Slice 4).
 *
 * Pins:
 *   1. GET maps the NetworkEndpoint DTO list onto the screen endpoints (no
 *      password field — it is write-only).
 *   2. saveEndpoint(create) POSTs, sends only the kind's field-group, and OMITS a
 *      blank password (write-only); a typed password IS sent.
 *   3. saveEndpoint(update) PATCHes /api/network/endpoints/{id} with the id.
 *   4. removeEndpoint surfaces a 409-in-use backend message VERBATIM (it lists the
 *      referencing sources) in endpointAction.error.
 *
 * Non-vacuous: point the composable at the wrong endpoint and the mocked GET —
 * which only answers /api/network/endpoints — returns { data: null }, so the list
 * would stay empty instead of the fixture's two endpoints.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useNetworkEndpoints } from './useNetworkEndpoints'
import type { NetworkEndpointInput } from '~/components/screens/settings.types'

const SOCKS_DTO = {
  id: 'ep-socks',
  name: 'VPN SOCKS',
  kind: 'socks' as const,
  enabled: true,
  host: '10.0.1.9',
  port: 1080,
  socksVersion: 5,
  username: 'tsundoku',
  url: '',
  fsProxy: '',
  session: '',
  sessionTtl: 0,
  timeout: 0,
  createdAt: '2026-07-17T00:00:00Z',
  updatedAt: '2026-07-17T00:00:00Z',
}
const FLARE_DTO = {
  ...SOCKS_DTO,
  id: 'ep-flare',
  name: 'VPN Flare',
  kind: 'flaresolverr' as const,
  host: '',
  port: 0,
  url: 'http://flare:8191',
  fsProxy: 'socks5://10.0.1.9:1080',
  session: 'omega',
  sessionTtl: 15,
  timeout: 60,
}
const ENDPOINTS = [SOCKS_DTO, FLARE_DTO]

let lastPost: { path: string, body: Record<string, unknown> } | null = null
let lastPatch: { path: string, id: unknown, body: Record<string, unknown> } | null = null
let lastDelete: { path: string, id: unknown } | null = null

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string) => {
      if (path === '/api/network/endpoints') return Promise.resolve({ data: ENDPOINTS, error: null })
      return Promise.resolve({ data: null, error: null })
    }),
    POST: vi.fn().mockImplementation((path: string, opts: { body: Record<string, unknown> }) => {
      lastPost = { path, body: opts.body }
      return Promise.resolve({ data: SOCKS_DTO, error: null })
    }),
    PATCH: vi.fn().mockImplementation((path: string, opts: { params: { path: { id: unknown } }, body: Record<string, unknown> }) => {
      lastPatch = { path, id: opts.params.path.id, body: opts.body }
      return Promise.resolve({ data: SOCKS_DTO, error: null })
    }),
    DELETE: vi.fn().mockImplementation((path: string, opts: { params: { path: { id: unknown } } }) => {
      lastDelete = { path, id: opts.params.path.id }
      return Promise.resolve({ data: undefined, error: null })
    }),
    PUT: vi.fn(),
    use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

const socksInput = (over: Partial<NetworkEndpointInput> = {}): NetworkEndpointInput => ({
  id: null,
  name: 'New SOCKS',
  kind: 'socks',
  enabled: true,
  host: 'proxy',
  port: 1080,
  socksVersion: 5,
  username: 'u',
  password: '',
  url: '',
  fsProxy: '',
  session: '',
  sessionTtl: 0,
  timeout: 0,
  ...over,
})

describe('useNetworkEndpoints', () => {
  beforeEach(() => {
    lastPost = null
    lastPatch = null
    lastDelete = null
  })

  it('maps the endpoint DTO list onto the screen endpoints (no password)', async () => {
    const { endpoints } = useNetworkEndpoints()
    await vi.waitFor(() => expect(endpoints.value.length).toBe(2))
    expect(endpoints.value[0]).toMatchObject({ id: 'ep-socks', kind: 'socks', host: '10.0.1.9', port: 1080 })
    expect(endpoints.value[1]).toMatchObject({ id: 'ep-flare', kind: 'flaresolverr', url: 'http://flare:8191' })
    // The screen type carries no password field at all (write-only).
    expect('password' in endpoints.value[0]!).toBe(false)
  })

  it('create POSTs only the SOCKS field-group and OMITS a blank password (write-only)', async () => {
    const { endpoints, saveEndpoint, endpointAction } = useNetworkEndpoints()
    await vi.waitFor(() => expect(endpoints.value.length).toBe(2))

    await saveEndpoint(socksInput({ password: '' }))

    expect(lastPost?.path).toBe('/api/network/endpoints')
    expect(lastPost?.body).toEqual({
      name: 'New SOCKS', kind: 'socks', enabled: true,
      host: 'proxy', port: 1080, socksVersion: 5, username: 'u',
    })
    expect('password' in (lastPost?.body ?? {})).toBe(false)
    expect(endpointAction.value).toEqual({ busyId: null })
  })

  it('create SENDS a typed password', async () => {
    const { endpoints, saveEndpoint } = useNetworkEndpoints()
    await vi.waitFor(() => expect(endpoints.value.length).toBe(2))

    await saveEndpoint(socksInput({ password: 'secret' }))
    expect(lastPost?.body.password).toBe('secret')
  })

  it('update PATCHes /api/network/endpoints/{id} with the endpoint id', async () => {
    const { endpoints, saveEndpoint } = useNetworkEndpoints()
    await vi.waitFor(() => expect(endpoints.value.length).toBe(2))

    await saveEndpoint(socksInput({ id: 'ep-socks', name: 'Renamed' }))
    expect(lastPatch?.path).toBe('/api/network/endpoints/{id}')
    expect(lastPatch?.id).toBe('ep-socks')
    expect(lastPatch?.body).toMatchObject({ name: 'Renamed', kind: 'socks' })
  })

  it('removeEndpoint surfaces the 409-in-use message verbatim', async () => {
    const { apiClient } = await import('~/utils/api/client')
    vi.mocked(apiClient.DELETE).mockResolvedValueOnce({
      data: undefined,
      error: { message: 'endpoint is referenced by sources: 9127482910938471028' },
    })

    const { endpoints, removeEndpoint, endpointAction } = useNetworkEndpoints()
    await vi.waitFor(() => expect(endpoints.value.length).toBe(2))

    await removeEndpoint('ep-socks')
    expect(endpointAction.value).toEqual({
      busyId: null,
      error: 'endpoint is referenced by sources: 9127482910938471028',
    })
  })

  it('removeEndpoint DELETEs the endpoint path on success', async () => {
    const { endpoints, removeEndpoint } = useNetworkEndpoints()
    await vi.waitFor(() => expect(endpoints.value.length).toBe(2))

    await removeEndpoint('ep-flare')
    expect(lastDelete?.path).toBe('/api/network/endpoints/{id}')
    expect(lastDelete?.id).toBe('ep-flare')
  })
})
