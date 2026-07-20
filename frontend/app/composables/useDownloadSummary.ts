/**
 * useDownloadSummary — the data layer for the always-visible nav download counter.
 *
 * A module-singleton (same shape as useNow / useProgressStream) so EVERY consumer
 * shares one poll + one set of reactive counts. It surfaces the three persistent
 * nav-badge counts from `GET /api/downloads/summary`:
 *   - downloading — chapters currently fetching (blue)
 *   - queued      — chapters waiting to download (yellow)
 *   - failed      — chapters needing attention (red)
 *
 * Two refresh drivers, reconciled:
 *   1. A ~10s background poll (the reliable backbone, so the counter stays honest
 *      even if an SSE event is missed on a flaky connection).
 *   2. The live SSE stream — a refetch is coalesced (leading + trailing, 800ms) on
 *      every download.start/done/fail and cycle.start/done so the counts move the
 *      instant something happens, not up to 10s later.
 *
 * The interval + SSE subscriptions are created lazily on the first consumer and
 * torn down when the last one's scope disposes (refcount, like useNow) — so a
 * fully-unmounted app leaves no timer running.
 */
import { ref, onScopeDispose } from 'vue'
import { apiClient } from '~/utils/api/client'

/** Background poll cadence — the reliable backbone under the live SSE refetches. */
export const POLL_MS = 10_000

// Coalesce a burst of SSE events into at most one refetch per window (leading +
// trailing), so a cycle firing dozens of download.* events triggers ~1 fetch.
const COALESCE_MS = 800

const downloading = ref(0)
const queued = ref(0)
const failed = ref(0)

let timer: ReturnType<typeof setInterval> | null = null
let coalesceTimer: ReturnType<typeof setTimeout> | null = null
let unsubs: (() => void)[] = []
let subscribers = 0

async function fetchSummary(): Promise<void> {
  const res = await apiClient.GET('/api/downloads/summary')
  if (res.error || !res.data) return // best-effort: a transient failure keeps the last counts
  downloading.value = res.data.downloading
  queued.value = res.data.queued
  failed.value = res.data.failed
}

// Leading + trailing coalesced refetch: fire once now, then once more after the
// window if further events arrived — so a state change is reflected immediately
// AND the settled count is correct.
function scheduleRefetch(): void {
  if (coalesceTimer) return
  void fetchSummary()
  coalesceTimer = setTimeout(() => {
    coalesceTimer = null
    void fetchSummary()
  }, COALESCE_MS)
}

function start(): void {
  const { on } = useProgressStream()
  void fetchSummary()
  timer = setInterval(() => void fetchSummary(), POLL_MS)
  for (const ev of ['download.start', 'download.done', 'download.fail', 'cycle.start', 'cycle.done']) {
    unsubs.push(on(ev, scheduleRefetch))
  }
}

function stop(): void {
  if (timer) { clearInterval(timer); timer = null }
  if (coalesceTimer) { clearTimeout(coalesceTimer); coalesceTimer = null }
  unsubs.forEach((u) => u())
  unsubs = []
}

export function useDownloadSummary() {
  subscribers++
  if (subscribers === 1) start()

  onScopeDispose(() => {
    subscribers--
    if (subscribers <= 0) {
      subscribers = 0
      stop()
    }
  })

  return { downloading, queued, failed, refresh: fetchSummary }
}
