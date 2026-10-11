import { ref, onUnmounted } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'

type Snapshot = components['schemas']['DownloadPrioritySnapshot']

/** Fixed snapshot status includes dormant members so cancellation stays available. */
export function useDownloadPriority() {
  const snapshots = ref<Snapshot[]>([])
  const busyIds = ref<string[]>([])
  const error = ref('')
  const { on } = useProgressStream()

  async function refresh(): Promise<void> {
    try {
      const result = await apiClient.GET('/api/downloads/priorities')
      if (result.error) throw new Error(result.error.message ?? 'Failed to load download priorities')
      snapshots.value = result.data ?? []
    }
    catch (cause) {
      error.value = cause instanceof Error ? cause.message : 'Failed to load download priorities'
    }
  }

  async function setPriority(id: string, enabled: boolean): Promise<void> {
    if (busyIds.value.includes(id)) return
    busyIds.value = [...busyIds.value, id]
    error.value = ''
    try {
      const options = { params: { path: { id } } }
      const result = enabled
        ? await apiClient.POST('/api/series/{id}/download-priority', options)
        : await apiClient.DELETE('/api/series/{id}/download-priority', options)
      if (result.error) throw new Error(result.error.message ?? 'Failed to update download priority')
      await refresh()
    }
    catch (cause) {
      error.value = cause instanceof Error ? cause.message : 'Failed to update download priority'
    }
    finally {
      busyIds.value = busyIds.value.filter(busyId => busyId !== id)
    }
  }

  const unsubscribe = ['cycle.done', 'download.done', 'download.fail', 'upgrade.done', 'upgrade.fail']
    .map(event => on(event, () => void refresh()))
  onUnmounted(() => unsubscribe.forEach(stop => stop()))
  void refresh()
  return { snapshots, busyIds, error, setPriority, refresh }
}
