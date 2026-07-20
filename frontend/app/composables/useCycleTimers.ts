/**
 * useCycleTimers — the two live header countdowns on the Downloads screen: the
 * time until the next download cycle and until the next refresh sweep.
 *
 * Derived ENTIRELY on the client from signals that already exist — NO new backend
 * endpoint:
 *   - the cycle/refresh boundaries come from the SSE stream (useProgressStream):
 *     `cycleActive` / `syncing` flag whether one is running right now, and the
 *     `cycle.done` / `refresh.done` events mark when the next one is scheduled
 *     (next fire = now + interval);
 *   - the two intervals come from GET /api/settings (`jobs.download_interval` /
 *     `jobs.refresh_interval`, Go duration strings parsed via parseGoDuration).
 *
 * Self-correcting: if an early cycle fires (e.g. a manual "Download now"), the
 * next `cycle.done` re-seeds the countdown from that moment.
 *
 * First-mount edge (no SSE seen yet, so the last-cycle time is unknown): once the
 * intervals load, the next-fire is SEEDED to `now + interval` so a plausible
 * countdown shows immediately instead of a blank or a wrong-looking negative. It
 * self-corrects on the first real `cycle.done` / `refresh.done`.
 *
 * A local 1-second ticker drives the seconds-precision display (useNow's shared
 * 30s clock is too coarse for the "0:43" download countdown); it is torn down when
 * the consumer's scope disposes.
 *
 * Returns the raw signals (running flags + remaining ms, null when unknown); the
 * presentational CycleTimers.vue formats them. `downloadRemainingMs` /
 * `refreshRemainingMs` are clamped to ≥0.
 */
import { ref, computed, onScopeDispose } from 'vue'
import { apiClient } from '~/utils/api/client'
import { parseGoDuration } from './useSettings'
import { useProgressStream } from './useProgressStream'

// Poll cadence of the seconds-precision countdown clock.
const TICK_MS = 1000

/** Convert a Go duration string ("1m0s", "2h") to milliseconds (0 when unparseable). */
function goDurationToMs(raw: string): number {
  const { value, unit } = parseGoDuration(raw)
  const unitMs = unit === 'h' ? 3_600_000 : unit === 'm' ? 60_000 : 1000
  return value * unitMs
}

export function useCycleTimers() {
  const stream = useProgressStream()
  stream.connect()

  // The two configured intervals (ms). null until GET /api/settings resolves.
  const downloadIntervalMs = ref<number | null>(null)
  const refreshIntervalMs = ref<number | null>(null)

  // Epoch ms of the next scheduled fire, re-seeded on each *.done event. null when
  // still unknown (intervals not loaded and no cycle seen yet).
  const nextDownloadAt = ref<number | null>(null)
  const nextRefreshAt = ref<number | null>(null)

  // Local seconds clock for the live countdown.
  const now = ref(Date.now())
  const timer = setInterval(() => { now.value = Date.now() }, TICK_MS)

  /**
   * Load the two intervals from settings and seed the initial next-fire times so a
   * countdown shows on first mount before any SSE boundary arrives. Failures are
   * swallowed (the timers just stay "waiting…") — this is passive header
   * decoration, not a user action, so it never surfaces an error banner.
   */
  async function loadIntervals(): Promise<void> {
    try {
      const res = await apiClient.GET('/api/settings')
      if (res.error || !res.data) return
      for (const s of res.data) {
        if (s.key === 'jobs.download_interval') downloadIntervalMs.value = goDurationToMs(s.value)
        if (s.key === 'jobs.refresh_interval') refreshIntervalMs.value = goDurationToMs(s.value)
      }
      if (nextDownloadAt.value == null && downloadIntervalMs.value != null) {
        nextDownloadAt.value = Date.now() + downloadIntervalMs.value
      }
      if (nextRefreshAt.value == null && refreshIntervalMs.value != null) {
        nextRefreshAt.value = Date.now() + refreshIntervalMs.value
      }
    }
    catch { /* passive decoration — never surfaced */ }
  }

  // Re-seed the next-fire the moment a cycle/refresh completes: next = now + interval.
  const offCycle = stream.on('cycle.done', () => {
    if (downloadIntervalMs.value != null) nextDownloadAt.value = Date.now() + downloadIntervalMs.value
  })
  const offRefresh = stream.on('refresh.done', () => {
    if (refreshIntervalMs.value != null) nextRefreshAt.value = Date.now() + refreshIntervalMs.value
  })

  onScopeDispose(() => {
    clearInterval(timer)
    offCycle()
    offRefresh()
  })

  void loadIntervals()

  const remaining = (at: number | null): number | null =>
    at == null ? null : Math.max(0, at - now.value)

  return {
    // Running flags, forwarded from the SSE stream.
    downloadRunning: stream.cycleActive,
    refreshRunning: stream.syncing,
    // Live remaining milliseconds (null when unknown), clamped ≥0.
    downloadRemainingMs: computed(() => remaining(nextDownloadAt.value)),
    refreshRemainingMs: computed(() => remaining(nextRefreshAt.value)),
  }
}
