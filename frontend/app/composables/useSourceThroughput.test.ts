import { beforeEach, describe, expect, it, vi } from 'vitest'
import { apiClient } from '~/utils/api/client'
import { useSourceThroughput } from './useSourceThroughput'

const inherited = {
  sourceId: '101',
  downloadConcurrency: { override: null, effective: 5 },
  imageRequestDelay: { override: null, effective: '500ms' },
}

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn(),
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
    resolve({ data: { defaults: { downloadConcurrency: 5, imageRequestDelay: '500ms' }, sources: [inherited] } })
    await request
    expect(state.policies.value).toEqual([inherited])
    expect(state.loading.value).toBe(false)
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
})
