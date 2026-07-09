/**
 * useLibraryMaintenance — one-shot library maintenance actions triggered from
 * Settings → Sources. Currently: the library-wide provider dedup sweep.
 *
 * dedupAllProviders() POSTs /api/library/dedup-providers, which returns 202
 * {started:true} IMMEDIATELY — the sweep runs detached on the server (it can
 * touch every series), so this surfaces a "started" message rather than an
 * aggregate; per-series results appear as each series is next viewed. §16 trio:
 * `dedupAllBusy` (in flight), `dedupAllMessage` (started), `dedupAllError`
 * (failure, never swallowed). Mirrors useSourceMetrics.warmNow's shape.
 */
import { ref } from 'vue'
import { apiClient } from '~/utils/api/client'

export function useLibraryMaintenance() {
  const dedupAllBusy = ref(false)
  const dedupAllMessage = ref<string | null>(null)
  const dedupAllError = ref<string | null>(null)

  async function dedupAllProviders(): Promise<void> {
    dedupAllBusy.value = true
    dedupAllMessage.value = null
    dedupAllError.value = null
    try {
      const res = await apiClient.POST('/api/library/dedup-providers')
      if (res.error) throw new Error(res.error.message)
      dedupAllMessage.value = 'Dedup started — duplicate sources merge in the background; results appear as you revisit each series'
    }
    catch (e) {
      dedupAllError.value = e instanceof Error ? e.message : 'Dedup failed'
    }
    finally {
      dedupAllBusy.value = false
    }
  }

  return { dedupAllBusy, dedupAllMessage, dedupAllError, dedupAllProviders }
}
