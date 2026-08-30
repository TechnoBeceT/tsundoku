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
  let mutationRequest = 0

  async function loadSummaries(): Promise<void> {
    const request = ++summariesRequest
    summariesPending.value = true
    summariesError.value = null
    try {
      const result = await apiClient.GET('/api/sources/exceptions')
      if (result.error || !result.data) {
        throw new Error(messageOf(result.error, 'Source exceptions could not be loaded'))
      }
      if (request === summariesRequest) summaries.value = result.data
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
      const result = await apiClient.GET('/api/sources/{sourceId}/effective-configuration', {
        params: { path: { sourceId } },
      })
      if (result.error || !result.data) {
        throw new Error(messageOf(result.error, 'Source configuration could not be loaded'))
      }
      if (request === detailRequest && selectedSourceId.value === sourceId) selected.value = result.data
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

  async function refetchDetailAfterMutation(sourceId: string): Promise<void> {
    if (selectedSourceId.value === sourceId) {
      await loadDetail(sourceId)
      return
    }

    // The owner may select another source while a save is in flight. The write
    // still earns its confirmation read, but that older source cannot replace
    // the newly selected detail.
    await apiClient.GET('/api/sources/{sourceId}/effective-configuration', {
      params: { path: { sourceId } },
    })
  }

  async function runMutation<T>(
    sourceId: string,
    key: SourceConfigurationActionKey,
    write: () => Promise<ApiResult<T>>,
    configurationOf: (data: T) => SourceEffectiveConfiguration | null,
  ): Promise<void> {
    if (action.value.saving) return

    const request = ++mutationRequest
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
        refetchDetailAfterMutation(sourceId),
        loadSummaries(),
      ])

      if (request === mutationRequest) action.value = { sourceId, key, saving: false, error: null }
    }
    catch (cause) {
      if (request === mutationRequest) {
        action.value = {
          sourceId,
          key,
          saving: false,
          error: cause instanceof Error ? cause.message : 'Source configuration could not be saved',
        }
      }
    }
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
    setTransport,
    setThroughput,
    setProxy,
    setBinding,
  }
}
