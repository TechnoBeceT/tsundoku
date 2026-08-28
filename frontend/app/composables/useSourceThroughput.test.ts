import { beforeEach, describe, expect, it, vi } from 'vitest'
import { apiClient } from '~/utils/api/client'
import { composeSourceThroughputPolicies, useSourceThroughput } from './useSourceThroughput'

const inherited = {
  sourceId: '101',
  downloadConcurrency: { override: null, effective: 5 },
  imageRequestDelay: { override: null, effective: '500ms' },
}

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockResolvedValue({ data: { authenticated: true, ownerId: 'owner' } }),
    PATCH: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

describe('useSourceThroughput', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(apiClient.GET).mockResolvedValue({ data: { defaults: { downloadConcurrency: 5, imageRequestDelay: '500ms' }, sources: [inherited] } })
    vi.mocked(apiClient.PATCH).mockResolvedValue({ data: inherited, response: new Response() })
  })

  it('loads policies and exposes loading while the request is pending', async () => {
    let resolve!: (value: unknown) => void
    vi.mocked(apiClient.GET).mockReturnValueOnce(new Promise(r => { resolve = r }))
    const state = useSourceThroughput()
    const request = state.load()
    expect(state.loading.value).toBe(true)
    expect(state.policies.value).toBeNull()
    resolve({ data: { defaults: { downloadConcurrency: 5, imageRequestDelay: '500ms' }, sources: [inherited] } })
    await request
    expect(state.policies.value).toEqual([inherited])
    expect(state.loading.value).toBe(false)
  })

  it('keeps policy state unauthoritative when loading fails', async () => {
    vi.mocked(apiClient.GET).mockResolvedValueOnce({ error: { message: 'Policy service unavailable' }, response: new Response() })
    const state = useSourceThroughput()
    await state.load()
    expect(state.policies.value).toBeNull()
    expect(state.error.value).toBe('Policy service unavailable')
  })

  it.each([
    ['saveConcurrencyOverride', 2, { downloadConcurrency: { mode: 'override', value: 2 } }],
    ['inheritConcurrency', undefined, { downloadConcurrency: { mode: 'inherit' } }],
    ['saveImageDelayOverride', '0s', { imageRequestDelay: { mode: 'override', value: '0s' } }],
    ['inheritImageDelay', undefined, { imageRequestDelay: { mode: 'inherit' } }],
  ] as const)('%s sends only its field so the other override cannot be clobbered', async (method, value, expected) => {
    const state = useSourceThroughput()
    if (value === undefined) await state[method]('101')
    else await (state[method] as (id: string, value: never) => Promise<void>)('101', value as never)
    expect(apiClient.PATCH).toHaveBeenCalledWith('/api/sources/{sourceId}/throughput', {
      params: { path: { sourceId: '101' } },
      body: expected,
    })
  })

  it('rejects invalid concurrency without issuing a request', async () => {
    const state = useSourceThroughput()
    await state.saveConcurrencyOverride('101', 0)
    expect(apiClient.PATCH).not.toHaveBeenCalled()
    expect(state.error.value).toContain('between 1 and 32')
  })

  it('rejects a positive sub-millisecond delay before issuing a request', async () => {
    const state = useSourceThroughput()
    await state.saveImageDelayOverride('101', '0.5ms')
    expect(apiClient.PATCH).not.toHaveBeenCalled()
    expect(state.error.value).toContain('whole milliseconds')
  })

  it('retains a visible server error after saving finishes', async () => {
    vi.mocked(apiClient.PATCH).mockResolvedValueOnce({ error: { message: 'Source policy could not be saved' } } as never)
    const state = useSourceThroughput()
    await state.saveImageDelayOverride('101', '750ms')
    expect(state.savingSourceId.value).toBeNull()
    expect(state.error.value).toBe('Source policy could not be saved')
  })

  it.each([
    ['different sources', '202'],
    ['the other field on the same source', '101'],
  ])('serializes an active mutation before %s can start', async (_label, secondSourceId) => {
    let resolve!: () => void
    vi.mocked(apiClient.PATCH).mockReturnValueOnce(new Promise(r => {
      resolve = () => r({ data: { ...inherited, downloadConcurrency: { override: 2, effective: 2 } }, response: new Response() })
    }))
    const state = useSourceThroughput()
    const first = state.saveConcurrencyOverride('101', 2)
    await state.saveImageDelayOverride(secondSourceId, '750ms')
    expect(apiClient.PATCH).toHaveBeenCalledTimes(1)
    expect(state.savingSourceId.value).toBe('101')
    resolve()
    await first
    expect(state.savingSourceId.value).toBeNull()
  })

  it('does not let a blocked second mutation overwrite active request state', async () => {
    let resolve!: () => void
    vi.mocked(apiClient.PATCH).mockReturnValueOnce(new Promise(r => {
      resolve = () => r({ data: inherited, response: new Response() })
    }))
    const state = useSourceThroughput()
    const first = state.saveConcurrencyOverride('101', 2)
    await state.saveConcurrencyOverride('202', 0)
    expect(state.error.value).toBeNull()
    expect(state.savingSourceId.value).toBe('101')
    resolve()
    await first
  })
})

describe('composeSourceThroughputPolicies', () => {
  it('recomputes inherited effective values from authoritative globals including zero delay', () => {
    expect(composeSourceThroughputPolicies(
      [{ id: '101', name: 'ComicK', lang: 'en' }],
      [inherited],
      { downloadConcurrency: 8, imageRequestDelay: '0ms' },
    )).toEqual([{
      ...inherited,
      downloadConcurrency: { override: null, effective: 8 },
      imageRequestDelay: { override: null, effective: '0ms' },
    }])
  })

  it('preserves explicit overrides when globals change', () => {
    const overridden = { ...inherited, downloadConcurrency: { override: 1, effective: 1 }, imageRequestDelay: { override: '750ms', effective: '750ms' } }
    expect(composeSourceThroughputPolicies([{ id: '101', name: 'ComicK', lang: 'en' }], [overridden], { downloadConcurrency: 8, imageRequestDelay: '0ms' })[0]).toEqual(overridden)
  })
})
