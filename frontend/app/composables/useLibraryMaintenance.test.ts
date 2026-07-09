import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useLibraryMaintenance } from './useLibraryMaintenance'

let nextOk = true
const calls: string[] = []

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn(),
    POST: vi.fn().mockImplementation((path: string) => {
      calls.push(path)
      if (!nextOk) {
        return Promise.resolve({ error: { message: 'sweep failed' }, response: new Response(null, { status: 500 }) })
      }
      return Promise.resolve({ data: { started: true }, error: null, response: new Response(null, { status: 202 }) })
    }),
    PATCH: vi.fn(),
    DELETE: vi.fn(),
    PUT: vi.fn(),
    use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

describe('useLibraryMaintenance', () => {
  beforeEach(() => { nextOk = true; calls.length = 0 })

  it('POSTs the dedup endpoint and sets the started message', async () => {
    const { dedupAllProviders, dedupAllMessage, dedupAllBusy } = useLibraryMaintenance()
    await dedupAllProviders()
    expect(calls).toContain('/api/library/dedup-providers')
    expect(dedupAllMessage.value).toBeTruthy()
    expect(dedupAllBusy.value).toBe(false)
  })

  it('surfaces a failure in dedupAllError', async () => {
    nextOk = false
    const { dedupAllProviders, dedupAllError } = useLibraryMaintenance()
    await dedupAllProviders()
    expect(dedupAllError.value).toBeTruthy()
  })
})
