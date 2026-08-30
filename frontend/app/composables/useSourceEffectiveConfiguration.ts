import { ref } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'

type ErrorResponse = components['schemas']['ErrorResponse']
type SourceExceptionSummary = components['schemas']['SourceExceptionSummary']
type SourceEffectiveConfiguration = components['schemas']['SourceEffectiveConfiguration']
type SourceMutationResponse = components['schemas']['SourceMutationResponse']
type SourceTransportPolicyUpdate = components['schemas']['SourceTransportPolicyUpdate']
type BooleanPolicyPatch = components['schemas']['BooleanPolicyPatch']
type ImageConnectionPolicyPatch = components['schemas']['ImageConnectionPolicyPatch']
type SourceThroughputUpdateRequest = components['schemas']['SourceThroughputUpdateRequest']
type SourceThroughputConcurrencyPatch = components['schemas']['SourceThroughputConcurrencyPatch']
type SourceThroughputDelayPatch = components['schemas']['SourceThroughputDelayPatch']
type SourceThroughputPolicy = components['schemas']['SourceThroughputPolicy']
type SourceNetworkBindingUpdate = components['schemas']['SourceNetworkBindingUpdate']

export type SourceConfigurationActionKey =
  | keyof SourceTransportPolicyUpdate
  | keyof SourceThroughputUpdateRequest
  | 'imageProxy'
  | 'routing'

export interface SourceConfigurationAction {
  sourceId: string | null
  key: SourceConfigurationActionKey | null
  saving: boolean
  error: string | null
}

interface ApiResult<T> {
  data?: T
  error?: ErrorResponse
}

const DEFAULT_ACTION: SourceConfigurationAction = {
  sourceId: null,
  key: null,
  saving: false,
  error: null,
}

function messageOf(error: ErrorResponse | undefined, fallback: string): string {
  return error?.message ?? fallback
}

/**
 * Owns the confirmed effective configuration shown by the source-exceptions
 * editor. All effective values come from the composed server response; narrow
 * writes only describe intent and are followed by fresh detail and summary
 * reads.
 */
export function useSourceEffectiveConfiguration() {
  const summaries = ref<SourceExceptionSummary[]>([])
  const selected = ref<SourceEffectiveConfiguration | null>(null)
  const selectedSourceId = ref<string | null>(null)
  const summariesPending = ref(false)
  const selectedPending = ref(false)
  const summariesError = ref<string | null>(null)
  const selectedError = ref<string | null>(null)
  const action = ref<SourceConfigurationAction>({ ...DEFAULT_ACTION })

  let summariesRequest = 0
  let detailRequest = 0
  let mutationRunning = false
  const mutationQueue: (() => Promise<void>)[] = []

  async function fetchDetail(sourceId: string): Promise<SourceEffectiveConfiguration> {
    const result = await apiClient.GET('/api/sources/{sourceId}/effective-configuration', {
      params: { path: { sourceId } },
    })
    if (result.error || !result.data) {
      throw new Error(messageOf(result.error, 'Source configuration could not be loaded'))
    }
    return result.data
  }

  async function fetchSummaries(): Promise<SourceExceptionSummary[]> {
    const result = await apiClient.GET('/api/sources/exceptions')
    if (result.error || !result.data) {
      throw new Error(messageOf(result.error, 'Source exceptions could not be loaded'))
    }
    return result.data
  }

  async function loadSummaries(): Promise<void> {
    const request = ++summariesRequest
    summariesPending.value = true
    summariesError.value = null
    try {
      const result = await fetchSummaries()
      if (request === summariesRequest) summaries.value = result
    }
    catch (cause) {
      if (request === summariesRequest) {
        summariesError.value = cause instanceof Error ? cause.message : 'Source exceptions could not be loaded'
      }
    }
    finally {
      if (request === summariesRequest) summariesPending.value = false
    }
  }

  async function loadDetail(sourceId: string): Promise<void> {
    const request = ++detailRequest
    selectedPending.value = true
    selectedError.value = null
    try {
      const configuration = await fetchDetail(sourceId)
      if (request === detailRequest && selectedSourceId.value === sourceId) selected.value = configuration
    }
    catch (cause) {
      if (request === detailRequest && selectedSourceId.value === sourceId) {
        selectedError.value = cause instanceof Error ? cause.message : 'Source configuration could not be loaded'
      }
    }
    finally {
      if (request === detailRequest && selectedSourceId.value === sourceId) selectedPending.value = false
    }
  }

  async function selectSource(sourceId: string): Promise<void> {
    selectedSourceId.value = sourceId
    await loadDetail(sourceId)
  }

  async function confirmDetailAfterMutation(sourceId: string): Promise<void> {
    const request = selectedSourceId.value === sourceId ? ++detailRequest : null
    const configuration = await fetchDetail(sourceId)
    if (request === null || request !== detailRequest || selectedSourceId.value !== sourceId) return

    selected.value = configuration
    selectedError.value = null
  }

  async function confirmSummariesAfterMutation(): Promise<void> {
    const request = ++summariesRequest
    try {
      const result = await fetchSummaries()
      if (request !== summariesRequest) return
      summaries.value = result
      summariesError.value = null
    }
    catch (cause) {
      if (request === summariesRequest) {
        summariesError.value = cause instanceof Error ? cause.message : 'Source exceptions could not be loaded'
      }
      throw cause
    }
  }

  /** Refreshes every projection that includes global source behavior. */
  async function refreshAfterGlobalChange(): Promise<void> {
    const sourceId = selectedSourceId.value
    await Promise.all([
      sourceId === null ? Promise.resolve() : loadDetail(sourceId),
      loadSummaries(),
    ])
  }

  async function executeMutation<T>(
    sourceId: string,
    key: SourceConfigurationActionKey,
    write: () => Promise<ApiResult<T>>,
    configurationOf: (data: T) => SourceEffectiveConfiguration | null,
  ): Promise<void> {
    action.value = { sourceId, key, saving: true, error: null }
    try {
      const result = await write()
      if (result.error || !result.data) throw new Error(messageOf(result.error, 'Source configuration could not be saved'))

      const confirmed = configurationOf(result.data)
      if (confirmed && selectedSourceId.value === sourceId) {
        // Invalidate a detail request started before this mutation completed.
        ++detailRequest
        selected.value = confirmed
        selectedPending.value = false
        selectedError.value = null
      }

      await Promise.all([
        confirmDetailAfterMutation(sourceId),
        confirmSummariesAfterMutation(),
      ])

      action.value = { sourceId, key, saving: false, error: null }
    }
    catch (cause) {
      action.value = {
        sourceId,
        key,
        saving: false,
        error: cause instanceof Error ? cause.message : 'Source configuration could not be saved',
      }
    }
  }

  function runMutation<T>(
    sourceId: string,
    key: SourceConfigurationActionKey,
    write: () => Promise<ApiResult<T>>,
    configurationOf: (data: T) => SourceEffectiveConfiguration | null,
  ): Promise<void> {
    const queued = new Promise<void>((resolve, reject) => {
      mutationQueue.push(async () => {
        try {
          await executeMutation(sourceId, key, write, configurationOf)
          resolve()
        }
        catch (cause) {
          reject(cause instanceof Error ? cause : new Error('Source configuration could not be saved'))
        }
      })
    })
    drainMutationQueue()
    return queued
  }

  function drainMutationQueue(): void {
    if (mutationRunning) return
    const next = mutationQueue.shift()
    if (!next) return

    mutationRunning = true
    void next().finally(() => {
      mutationRunning = false
      drainMutationQueue()
    })
  }

  function setTransport(
    sourceId: string,
    key: 'reuseBypassSession',
    patch: BooleanPolicyPatch,
  ): Promise<void>
  function setTransport(
    sourceId: string,
    key: 'imageConnectionMode',
    patch: ImageConnectionPolicyPatch,
  ): Promise<void>
  function setTransport(
    sourceId: string,
    key: keyof SourceTransportPolicyUpdate,
    patch: BooleanPolicyPatch | ImageConnectionPolicyPatch,
  ): Promise<void> {
    const body: SourceTransportPolicyUpdate = key === 'reuseBypassSession'
      ? { reuseBypassSession: patch as BooleanPolicyPatch }
      : { imageConnectionMode: patch as ImageConnectionPolicyPatch }
    return runMutation<SourceMutationResponse>(sourceId, key, async () => {
      return apiClient.PATCH('/api/sources/{sourceId}/transport', {
        params: { path: { sourceId } },
        body,
      })
    }, data => data.configuration)
  }

  function setThroughput(
    sourceId: string,
    key: 'downloadConcurrency',
    patch: SourceThroughputConcurrencyPatch,
  ): Promise<void>
  function setThroughput(
    sourceId: string,
    key: 'imageRequestDelay',
    patch: SourceThroughputDelayPatch,
  ): Promise<void>
  function setThroughput(
    sourceId: string,
    key: keyof SourceThroughputUpdateRequest,
    patch: SourceThroughputConcurrencyPatch | SourceThroughputDelayPatch,
  ): Promise<void> {
    const body: SourceThroughputUpdateRequest = key === 'downloadConcurrency'
      ? { downloadConcurrency: patch as SourceThroughputConcurrencyPatch }
      : { imageRequestDelay: patch as SourceThroughputDelayPatch }
    return runMutation<SourceThroughputPolicy>(sourceId, key, async () => {
      return apiClient.PATCH('/api/sources/{sourceId}/throughput', {
        params: { path: { sourceId } },
        body,
      })
    }, () => null)
  }

  function setProxy(sourceId: string, enabled: boolean): Promise<void> {
    return runMutation<SourceMutationResponse>(sourceId, 'imageProxy', async () => {
      return apiClient.PUT('/api/sources/{sourceId}/image-proxy', {
        params: { path: { sourceId } },
        body: { enabled },
      })
    }, data => data.configuration)
  }

  function setBinding(sourceId: string, update: SourceNetworkBindingUpdate | null): Promise<void> {
    return runMutation<SourceMutationResponse>(sourceId, 'routing', async () => {
      if (update === null) {
        return apiClient.DELETE('/api/network/bindings/{sourceId}', {
          params: { path: { sourceId } },
        })
      }
      return apiClient.PUT('/api/network/bindings/{sourceId}', {
        params: { path: { sourceId } },
        body: update,
      })
    }, data => data.configuration)
  }

  return {
    summaries,
    selected,
    selectedSourceId,
    summariesPending,
    selectedPending,
    summariesError,
    selectedError,
    action,
    loadSummaries,
    selectSource,
    refreshAfterGlobalChange,
    setTransport,
    setThroughput,
    setProxy,
    setBinding,
  }
}
