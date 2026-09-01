import { beforeEach, describe, expect, it, vi } from 'vitest'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import {
  comicAsuraSourceConfiguration,
  fullyInheritedSourceConfiguration,
  hiveProxySourceConfiguration,
  pendingSourceException,
} from '~/fixtures/settings'
import { useSourceEffectiveConfiguration } from './useSourceEffectiveConfiguration'

type SourceEffectiveConfiguration = components['schemas']['SourceEffectiveConfiguration']
type SourceExceptionSummary = components['schemas']['SourceExceptionSummary']

const SOURCE_ID = '1998416842837112832'
const OTHER_SOURCE_ID = fullyInheritedSourceConfiguration.source.sourceId

let detailBySource: Record<string, SourceEffectiveConfiguration>
let summariesResponse: SourceExceptionSummary[]

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn(),
    PATCH: vi.fn(),
    PUT: vi.fn(),
    DELETE: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

function installDefaultResponses(): void {
  vi.mocked(apiClient.GET).mockImplementation((path, options) => {
    if (path === '/api/sources/exceptions') {
      return Promise.resolve({ data: summariesResponse, response: new Response() }) as never
    }
    if (path === '/api/sources/{sourceId}/effective-configuration') {
      const sourceId = (options as { params: { path: { sourceId: string } } }).params.path.sourceId
      return Promise.resolve({ data: detailBySource[sourceId], response: new Response() }) as never
    }
    return Promise.resolve({ error: { message: 'Unexpected GET' }, response: new Response() }) as never
  })
  vi.mocked(apiClient.PATCH).mockImplementation((path) => {
    if (path === '/api/sources/{sourceId}/throughput') {
      return Promise.resolve({
        data: {
          sourceId: SOURCE_ID,
          downloadConcurrency: { override: 2, effective: 2 },
          imageRequestDelay: { override: null, effective: '500ms' },
        },
        response: new Response(),
      })
    }
    return Promise.resolve({
      data: {
        configuration: detailBySource[SOURCE_ID]!,
        runtime: detailBySource[SOURCE_ID]!.runtime,
      },
      response: new Response(),
    })
  })
  vi.mocked(apiClient.PUT).mockResolvedValue({
    data: {
      configuration: comicAsuraSourceConfiguration,
      runtime: comicAsuraSourceConfiguration.runtime,
    },
    response: new Response(),
  })
  vi.mocked(apiClient.DELETE).mockResolvedValue({
    data: {
      configuration: fullyInheritedSourceConfiguration,
      runtime: fullyInheritedSourceConfiguration.runtime,
    },
    response: new Response(),
  })
}

describe('useSourceEffectiveConfiguration', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    detailBySource = {
      [SOURCE_ID]: comicAsuraSourceConfiguration,
      [OTHER_SOURCE_ID]: fullyInheritedSourceConfiguration,
    }
    summariesResponse = [pendingSourceException]
    installDefaultResponses()
  })

  it('loads typed exception summaries and one server-composed detail using lossless string source IDs', async () => {
    const state = useSourceEffectiveConfiguration()

    await state.loadSummaries()
    await state.selectSource(SOURCE_ID)

    expect(state.summaries.value).toEqual([pendingSourceException])
    expect(state.selected.value).toEqual(comicAsuraSourceConfiguration)
    expect(apiClient.GET).toHaveBeenCalledWith('/api/sources/exceptions')
    expect(apiClient.GET).toHaveBeenCalledWith('/api/sources/{sourceId}/effective-configuration', {
      params: { path: { sourceId: SOURCE_ID } },
    })
  })

  it('sends every narrow generated write without coercing the source ID', async () => {
    const state = useSourceEffectiveConfiguration()
    await state.selectSource(SOURCE_ID)

    await state.setTransport(SOURCE_ID, 'reuseBypassSession', { mode: 'override', value: false })
    expect(apiClient.PATCH).toHaveBeenCalledWith('/api/sources/{sourceId}/transport', {
      params: { path: { sourceId: SOURCE_ID } },
      body: { reuseBypassSession: { mode: 'override', value: false } },
    })

    await state.setTransport(SOURCE_ID, 'kcefPolicy', { mode: 'override', value: 'required' })
    expect(apiClient.PATCH).toHaveBeenCalledWith('/api/sources/{sourceId}/transport', {
      params: { path: { sourceId: SOURCE_ID } },
      body: { kcefPolicy: { mode: 'override', value: 'required' } },
    })

    await state.setThroughput(SOURCE_ID, 'downloadConcurrency', { mode: 'override', value: 2 })
    expect(apiClient.PATCH).toHaveBeenCalledWith('/api/sources/{sourceId}/throughput', {
      params: { path: { sourceId: SOURCE_ID } },
      body: { downloadConcurrency: { mode: 'override', value: 2 } },
    })

    await state.setProxy(SOURCE_ID, true)
    expect(apiClient.PUT).toHaveBeenCalledWith('/api/sources/{sourceId}/image-proxy', {
      params: { path: { sourceId: SOURCE_ID } },
      body: { enabled: true },
    })

    const binding = { socksEndpointId: null, flareMode: 'endpoint' as const, flareEndpointId: 'ep-flare' }
    await state.setBinding(SOURCE_ID, binding)
    expect(apiClient.PUT).toHaveBeenCalledWith('/api/network/bindings/{sourceId}', {
      params: { path: { sourceId: SOURCE_ID } },
      body: binding,
    })

    await state.setBinding(SOURCE_ID, null)
    expect(apiClient.DELETE).toHaveBeenCalledWith('/api/network/bindings/{sourceId}', {
      params: { path: { sourceId: SOURCE_ID } },
    })
  })

  it('sends KCEF inherit intent in the generated transport patch', async () => {
    const state = useSourceEffectiveConfiguration()
    await state.selectSource(SOURCE_ID)

    await state.setTransport(SOURCE_ID, 'kcefPolicy', { mode: 'inherit' })

    expect(apiClient.PATCH).toHaveBeenCalledWith('/api/sources/{sourceId}/transport', {
      params: { path: { sourceId: SOURCE_ID } },
      body: { kcefPolicy: { mode: 'inherit' } },
    })
  })

  it('adopts server-confirmed mutation data and refetches detail plus summaries after success', async () => {
    const state = useSourceEffectiveConfiguration()
    await state.selectSource(SOURCE_ID)
    vi.clearAllMocks()
    installDefaultResponses()

    const serverConfirmed = {
      ...hiveProxySourceConfiguration,
      source: comicAsuraSourceConfiguration.source,
      protection: { ...hiveProxySourceConfiguration.protection, failureThreshold: 17 },
    } satisfies SourceEffectiveConfiguration
    detailBySource[SOURCE_ID] = serverConfirmed

    await state.setThroughput(SOURCE_ID, 'imageRequestDelay', { mode: 'override', value: '750ms' })

    expect(apiClient.PATCH).toHaveBeenCalledWith('/api/sources/{sourceId}/throughput', {
      params: { path: { sourceId: SOURCE_ID } },
      body: { imageRequestDelay: { mode: 'override', value: '750ms' } },
    })
    expect(state.selected.value).toEqual(serverConfirmed)
    expect(state.selected.value?.protection.failureThreshold).toBe(17)
    expect(apiClient.GET).toHaveBeenCalledWith('/api/sources/{sourceId}/effective-configuration', {
      params: { path: { sourceId: SOURCE_ID } },
    })
    expect(apiClient.GET).toHaveBeenCalledWith('/api/sources/exceptions')
  })

  it('keeps the last confirmed detail and attaches failure state only to the active row', async () => {
    let finish!: () => void
    vi.mocked(apiClient.PATCH).mockReturnValueOnce(new Promise(resolve => {
      finish = () => resolve({ error: { message: 'Concurrency was rejected' }, response: new Response() })
    }) as never)
    const state = useSourceEffectiveConfiguration()
    await state.selectSource(SOURCE_ID)

    const save = state.setThroughput(SOURCE_ID, 'downloadConcurrency', { mode: 'override', value: 2 })
    expect(state.action.value).toEqual({
      sourceId: SOURCE_ID,
      key: 'downloadConcurrency',
      saving: true,
      error: null,
    })
    expect(state.selected.value).toEqual(comicAsuraSourceConfiguration)

    finish()
    await save

    expect(state.selected.value).toEqual(comicAsuraSourceConfiguration)
    expect(state.action.value).toEqual({
      sourceId: SOURCE_ID,
      key: 'downloadConcurrency',
      saving: false,
      error: 'Concurrency was rejected',
    })
    expect(apiClient.GET).not.toHaveBeenCalledWith('/api/sources/exceptions')
  })

  it('does not let an older detail request overwrite a newer selection', async () => {
    let resolveFirst!: (value: unknown) => void
    vi.mocked(apiClient.GET).mockImplementation((path, options) => {
      if (path !== '/api/sources/{sourceId}/effective-configuration') {
        return Promise.resolve({ data: summariesResponse, response: new Response() }) as never
      }
      const sourceId = (options as { params: { path: { sourceId: string } } }).params.path.sourceId
      if (sourceId === SOURCE_ID) return new Promise(resolve => { resolveFirst = resolve }) as never
      return Promise.resolve({ data: fullyInheritedSourceConfiguration, response: new Response() }) as never
    })
    const state = useSourceEffectiveConfiguration()

    const first = state.selectSource(SOURCE_ID)
    await state.selectSource(OTHER_SOURCE_ID)
    resolveFirst({ data: comicAsuraSourceConfiguration, response: new Response() })
    await first

    expect(state.selected.value).toEqual(fullyInheritedSourceConfiguration)
    expect(state.selectedPending.value).toBe(false)
  })

  it('does not let a mutation for the previous selection replace the current source', async () => {
    let finish!: () => void
    vi.mocked(apiClient.PUT).mockReturnValueOnce(new Promise(resolve => {
      finish = () => resolve({
        data: {
          configuration: hiveProxySourceConfiguration,
          runtime: hiveProxySourceConfiguration.runtime,
        },
        response: new Response(),
      })
    }) as never)
    const state = useSourceEffectiveConfiguration()
    await state.selectSource(SOURCE_ID)

    const save = state.setProxy(SOURCE_ID, true)
    await state.selectSource(OTHER_SOURCE_ID)
    finish()
    await save

    expect(state.selected.value).toEqual(fullyInheritedSourceConfiguration)
  })

  it('runs overlapping rows FIFO and resolves each call only after its own request completes', async () => {
    let finishFirst!: () => void
    let finishSecond!: () => void
    vi.mocked(apiClient.PATCH)
      .mockReturnValueOnce(new Promise(resolve => {
        finishFirst = () => resolve({
          data: {
            sourceId: SOURCE_ID,
            downloadConcurrency: { override: 2, effective: 2 },
            imageRequestDelay: { override: null, effective: '500ms' },
          },
          response: new Response(),
        })
      }) as never)
      .mockReturnValueOnce(new Promise(resolve => {
        finishSecond = () => resolve({
          data: {
            configuration: comicAsuraSourceConfiguration,
            runtime: comicAsuraSourceConfiguration.runtime,
          },
          response: new Response(),
        })
      }) as never)
    const state = useSourceEffectiveConfiguration()

    const first = state.setThroughput(SOURCE_ID, 'downloadConcurrency', { mode: 'override', value: 2 })
    const second = state.setTransport(SOURCE_ID, 'imageConnectionMode', { mode: 'override', value: 'fresh' })
    let secondCompleted = false
    void second.then(() => { secondCompleted = true })

    expect(apiClient.PATCH).toHaveBeenCalledTimes(1)
    expect(state.action.value).toEqual({
      sourceId: SOURCE_ID,
      key: 'downloadConcurrency',
      saving: true,
      error: null,
    })

    finishFirst()
    await first
    await vi.waitFor(() => expect(apiClient.PATCH).toHaveBeenCalledTimes(2))

    expect(apiClient.PATCH).toHaveBeenNthCalledWith(2, '/api/sources/{sourceId}/transport', {
      params: { path: { sourceId: SOURCE_ID } },
      body: { imageConnectionMode: { mode: 'override', value: 'fresh' } },
    })
    expect(state.action.value).toEqual({
      sourceId: SOURCE_ID,
      key: 'imageConnectionMode',
      saving: true,
      error: null,
    })
    expect(secondCompleted).toBe(false)

    finishSecond()
    await second
    expect(secondCompleted).toBe(true)
  })

  it('continues with the next queued row after the first write fails', async () => {
    let failFirst!: () => void
    let finishSecond!: () => void
    vi.mocked(apiClient.PATCH)
      .mockReturnValueOnce(new Promise(resolve => {
        failFirst = () => resolve({ error: { message: 'Concurrency was rejected' }, response: new Response() })
      }) as never)
      .mockReturnValueOnce(new Promise(resolve => {
        finishSecond = () => resolve({
          data: {
            configuration: comicAsuraSourceConfiguration,
            runtime: comicAsuraSourceConfiguration.runtime,
          },
          response: new Response(),
        })
      }) as never)
    const state = useSourceEffectiveConfiguration()

    const first = state.setThroughput(SOURCE_ID, 'downloadConcurrency', { mode: 'override', value: 2 })
    const second = state.setTransport(SOURCE_ID, 'reuseBypassSession', { mode: 'inherit' })

    expect(apiClient.PATCH).toHaveBeenCalledTimes(1)
    failFirst()
    await first
    await vi.waitFor(() => expect(apiClient.PATCH).toHaveBeenCalledTimes(2))
    expect(state.action.value.key).toBe('reuseBypassSession')

    finishSecond()
    await second
    expect(state.action.value).toEqual({
      sourceId: SOURCE_ID,
      key: 'reuseBypassSession',
      saving: false,
      error: null,
    })
  })

  it('does not deadlock later writes when a successful mutation confirmation read rejects', async () => {
    let detailReads = 0
    vi.mocked(apiClient.GET).mockImplementation((path, options) => {
      if (path === '/api/sources/exceptions') {
        return Promise.resolve({ data: summariesResponse, response: new Response() }) as never
      }
      if (path === '/api/sources/{sourceId}/effective-configuration') {
        detailReads += 1
        if (detailReads === 1) return Promise.reject(new Error('Confirmation read failed')) as never
        const sourceId = (options as { params: { path: { sourceId: string } } }).params.path.sourceId
        return Promise.resolve({ data: detailBySource[sourceId], response: new Response() }) as never
      }
      return Promise.resolve({ error: { message: 'Unexpected GET' }, response: new Response() }) as never
    })
    const state = useSourceEffectiveConfiguration()

    const first = state.setTransport(SOURCE_ID, 'reuseBypassSession', { mode: 'inherit' })
    const second = state.setThroughput(SOURCE_ID, 'downloadConcurrency', { mode: 'override', value: 2 })

    await first
    await second
    expect(apiClient.PATCH).toHaveBeenCalledTimes(2)
    expect(state.action.value).toEqual({
      sourceId: SOURCE_ID,
      key: 'downloadConcurrency',
      saving: false,
      error: null,
    })
  })
})
