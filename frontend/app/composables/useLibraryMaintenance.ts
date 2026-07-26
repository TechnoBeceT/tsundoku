/**
 * useLibraryMaintenance — one-shot library maintenance actions triggered from
 * Settings → Sources. Currently: the library-wide provider dedup sweep.
 *
 * dedupAllProviders() POSTs /api/library/dedup-providers, which returns 202
 * {started:true} IMMEDIATELY — the sweep runs DETACHED on the server (it can
 * touch every series), so the response can never carry the result. The outcome
 * arrives later on the `library.dedup.done` SSE event, which this composable
 * subscribes to and folds into the same §16 trio, so the dialog goes
 * "starting… → started → what actually happened" instead of stopping at
 * "started" and leaving the operation silent.
 *
 * §16 trio: `dedupAllBusy` (in flight), `dedupAllMessage` (started, then the
 * final summary), `dedupAllError` (failure, never swallowed). Alongside them
 * `dedupAllSkippedBusy` counts the series the sweep had to skip because another
 * merge was running on them — its own number, because the owner acts on it
 * differently (just run the clean-up again), and folding it into the summary
 * sentence would hide it.
 */
import { ref, onUnmounted } from 'vue'
import { apiClient } from '~/utils/api/client'
import { useProgressStream } from '~/composables/useProgressStream'
import { readDedupSweepEvent } from '~/utils/dedupSweepSummary'

export function useLibraryMaintenance() {
  const dedupAllBusy = ref(false)
  const dedupAllMessage = ref<string | null>(null)
  const dedupAllError = ref<string | null>(null)
  const dedupAllSkippedBusy = ref(0)

  // The sweep is detached: this event is the only report of what it did.
  const { on } = useProgressStream()
  const unsubSweep = on('library.dedup.done', (data) => {
    const summary = readDedupSweepEvent(data)
    dedupAllMessage.value = summary.message
    dedupAllError.value = summary.error
    dedupAllSkippedBusy.value = summary.busy
  })
  onUnmounted(unsubSweep)

  async function dedupAllProviders(): Promise<void> {
    dedupAllBusy.value = true
    dedupAllMessage.value = null
    dedupAllError.value = null
    dedupAllSkippedBusy.value = 0
    try {
      const res = await apiClient.POST('/api/library/dedup-providers')
      if (res.error) throw new Error(res.error.message)
      dedupAllMessage.value = 'Dedup started — duplicate sources merge in the background; the result appears here when it finishes'
    }
    catch (e) {
      dedupAllError.value = e instanceof Error ? e.message : 'Dedup failed'
    }
    finally {
      dedupAllBusy.value = false
    }
  }

  return { dedupAllBusy, dedupAllMessage, dedupAllError, dedupAllSkippedBusy, dedupAllProviders }
}
