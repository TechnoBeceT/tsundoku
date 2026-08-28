import { ref } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import type { SourceOption } from '~/components/screens/settings.types'

export type SourceThroughputPolicy = components['schemas']['SourceThroughputPolicy']
type UpdateRequest = components['schemas']['SourceThroughputUpdateRequest']

export function composeSourceThroughputPolicies(
  sources: SourceOption[],
  storedPolicies: SourceThroughputPolicy[],
  globals: { downloadConcurrency: number, imageRequestDelay: string },
): SourceThroughputPolicy[] {
  return sources.map((source) => {
    const stored = storedPolicies.find(policy => policy.sourceId === source.id)
    if (!stored) {
      return {
        sourceId: source.id,
        downloadConcurrency: { override: null, effective: globals.downloadConcurrency },
        imageRequestDelay: { override: null, effective: globals.imageRequestDelay },
      }
    }
    return {
      ...stored,
      downloadConcurrency: stored.downloadConcurrency.override === null
        ? { override: null, effective: globals.downloadConcurrency }
        : stored.downloadConcurrency,
      imageRequestDelay: stored.imageRequestDelay.override === null
        ? { override: null, effective: globals.imageRequestDelay }
        : stored.imageRequestDelay,
    }
  })
}

function isWholeMillisecondDuration(value: string): boolean {
  if (value === '0') return true
  const matches = [...value.matchAll(/(\d+(?:\.\d+)?)(ms|s|m|h)/g)]
  if (matches.length === 0 || matches.map(match => match[0]).join('') !== value) return false
  const factors: Record<string, number> = { ms: 1, s: 1_000, m: 60_000, h: 3_600_000 }
  const milliseconds = matches.reduce((total, match) => total + Number(match[1]) * factors[match[2]!]!, 0)
  return Number.isInteger(milliseconds)
}

export function useSourceThroughput() {
  const policies = ref<SourceThroughputPolicy[] | null>(null)
  const loading = ref(false)
  const savingSourceId = ref<string | null>(null)
  const error = ref<string | null>(null)

  function replacePolicy(policy: SourceThroughputPolicy): void {
    const current = policies.value ?? []
    const index = current.findIndex(item => item.sourceId === policy.sourceId)
    if (index === -1) policies.value = [...current, policy]
    else {
      current.splice(index, 1, policy)
      policies.value = current
    }
  }

  async function load(): Promise<void> {
    loading.value = true
    policies.value = null
    error.value = null
    try {
      const result = await apiClient.GET('/api/sources/throughput')
      if (result.error || !result.data) throw new Error((result.error as { message?: string } | undefined)?.message ?? 'Source throughput settings could not be loaded')
      policies.value = result.data.sources
    }
    catch (cause) {
      error.value = cause instanceof Error ? cause.message : 'Source throughput settings could not be loaded'
    }
    finally { loading.value = false }
  }

  async function update(sourceId: string, body: UpdateRequest): Promise<void> {
    if (savingSourceId.value !== null) return
    savingSourceId.value = sourceId
    error.value = null
    try {
      const result = await apiClient.PATCH('/api/sources/{sourceId}/throughput', { params: { path: { sourceId } }, body })
      if (result.error || !result.data) throw new Error((result.error as { message?: string } | undefined)?.message ?? 'Source throughput setting could not be saved')
      replacePolicy(result.data)
    }
    catch (cause) {
      error.value = cause instanceof Error ? cause.message : 'Source throughput setting could not be saved'
    }
    finally { savingSourceId.value = null }
  }

  async function saveConcurrencyOverride(sourceId: string, value: number): Promise<void> {
    if (savingSourceId.value !== null) return
    if (!Number.isInteger(value) || value < 1 || value > 32) {
      error.value = 'Download concurrency must be a whole number between 1 and 32.'
      return
    }
    await update(sourceId, { downloadConcurrency: { mode: 'override', value } })
  }
  const inheritConcurrency = (sourceId: string) => update(sourceId, { downloadConcurrency: { mode: 'inherit' } })

  async function saveImageDelayOverride(sourceId: string, value: string): Promise<void> {
    if (savingSourceId.value !== null) return
    if (!isWholeMillisecondDuration(value.trim())) {
      error.value = 'Image delay must be a non-negative duration in whole milliseconds, such as 750ms, 2s, or 0s.'
      return
    }
    await update(sourceId, { imageRequestDelay: { mode: 'override', value: value.trim() } })
  }
  const inheritImageDelay = (sourceId: string) => update(sourceId, { imageRequestDelay: { mode: 'inherit' } })

  return { policies, loading, savingSourceId, error, load, saveConcurrencyOverride, inheritConcurrency, saveImageDelayOverride, inheritImageDelay }
}
