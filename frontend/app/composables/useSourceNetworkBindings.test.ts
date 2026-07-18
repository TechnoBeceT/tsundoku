/**
 * useSourceNetworkBindings — sources + bindings against /api/sources and
 * /api/network/bindings (per-source network routing, QCAT-283 Slice 4).
 *
 * Pins:
 *   1. refetch loads BOTH the source list + the bindings, mapping each; a
 *      binding's absent (undefined) endpoint id normalises to null.
 *   2. setBinding PUTs to /api/network/sources/{sourceId}/binding with the body,
 *      then clears bindingAction.
 *   3. clearBinding DELETEs the binding path.
 *   4. a PUT failure surfaces in bindingAction.error (never swallowed, §16).
 *
 * Non-vacuous: point either fetch at the wrong path and the mocked GET returns
 * { data: null }, so sources/bindings would stay empty instead of the fixtures.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useSourceNetworkBindings } from './useSourceNetworkBindings'

const SOURCES = [
  { id: '111', name: 'Asura', lang: 'en', degraded: false, degradedReason: '' },
  { id: '222', name: 'Omega', lang: 'en', degraded: false, degradedReason: '' },
]
const BINDINGS = [
  // Omega bound: SOCKS endpoint + FlareSolverr endpoint.
  { sourceId: '222', socksEndpointId: 'ep-socks', flareMode: 'endpoint' as const, flareEndpointId: 'ep-flare' },
  // A SOCKS-only binding with the flare endpoint id ABSENT (should map to null).
  { sourceId: '111', flareMode: 'none' as const },
]

let lastPut: { path: string, id: unknown, body: Record<string, unknown> } | null = null
let lastDelete: { path: string, id: unknown } | null = null

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string) => {
      if (path === '/api/sources') return Promise.resolve({ data: SOURCES, error: null })
      if (path === '/api/network/bindings') return Promise.resolve({ data: BINDINGS, error: null })
      return Promise.resolve({ data: null, error: null })
    }),
    PUT: vi.fn().mockImplementation((path: string, opts: { params: { path: { sourceId: unknown } }, body: Record<string, unknown> }) => {
      lastPut = { path, id: opts.params.path.sourceId, body: opts.body }
      return Promise.resolve({ data: BINDINGS[0], error: null })
    }),
    DELETE: vi.fn().mockImplementation((path: string, opts: { params: { path: { sourceId: unknown } } }) => {
      lastDelete = { path, id: opts.params.path.sourceId }
      return Promise.resolve({ data: undefined, error: null })
    }),
    POST: vi.fn(),
    use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

describe('useSourceNetworkBindings', () => {
  beforeEach(() => {
    lastPut = null
    lastDelete = null
  })

  it('loads sources + bindings, normalising an absent endpoint id to null', async () => {
    const { sources, bindings } = useSourceNetworkBindings()
    await vi.waitFor(() => expect(sources.value.length).toBe(2))

    expect(sources.value[0]).toEqual({ id: '111', name: 'Asura', lang: 'en' })
    // The SOCKS-only binding's absent flareEndpointId maps to null (not undefined).
    const asura = bindings.value.find(b => b.sourceId === '111')!
    expect(asura).toEqual({ sourceId: '111', socksEndpointId: null, flareMode: 'none', flareEndpointId: null })
  })

  it('setBinding PUTs to the source binding path with the body, then clears the action', async () => {
    const { sources, setBinding, bindingAction } = useSourceNetworkBindings()
    await vi.waitFor(() => expect(sources.value.length).toBe(2))

    await setBinding('222', { socksEndpointId: 'ep-socks', flareMode: 'global', flareEndpointId: null })
    expect(lastPut?.path).toBe('/api/network/sources/{sourceId}/binding')
    expect(lastPut?.id).toBe('222')
    expect(lastPut?.body).toEqual({ socksEndpointId: 'ep-socks', flareMode: 'global', flareEndpointId: null })
    expect(bindingAction.value).toEqual({ busyId: null })
  })

  it('clearBinding DELETEs the source binding path', async () => {
    const { sources, clearBinding } = useSourceNetworkBindings()
    await vi.waitFor(() => expect(sources.value.length).toBe(2))

    await clearBinding('222')
    expect(lastDelete?.path).toBe('/api/network/sources/{sourceId}/binding')
    expect(lastDelete?.id).toBe('222')
  })

  it('surfaces a PUT failure in bindingAction.error (§16)', async () => {
    const { apiClient } = await import('~/utils/api/client')
    vi.mocked(apiClient.PUT).mockResolvedValueOnce({
      data: undefined,
      error: { message: 'flareEndpointId is required when flareMode is endpoint' },
    } as never)

    const { sources, setBinding, bindingAction } = useSourceNetworkBindings()
    await vi.waitFor(() => expect(sources.value.length).toBe(2))

    await setBinding('222', { flareMode: 'endpoint' })
    expect(bindingAction.value).toEqual({
      busyId: null,
      error: 'flareEndpointId is required when flareMode is endpoint',
    })
  })
})
