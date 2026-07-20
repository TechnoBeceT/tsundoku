/**
 * useEngineStatus — data layer for the live engine source-status strip on the
 * Downloads screen.
 *
 * Polls GET /api/engine/sources on a short interval (default 4s) so the strip
 * reflects which sources are downloading / cooling right now. The endpoint is a
 * pure DB + circuit-breaker-snapshot read (no engine call), so polling it is cheap.
 *
 * LIFECYCLE (the polite poll):
 *   - fetches once immediately (unless immediate:false);
 *   - polls only while the document is VISIBLE — when the tab is hidden the
 *     interval is torn down (no background churn) and re-armed (with an immediate
 *     freshening fetch) when the tab is shown again;
 *   - cleans up on scope dispose (component unmount, or effectScope stop in tests):
 *     the interval is cleared and the visibilitychange listener removed.
 *
 * Cleanup uses onScopeDispose (like useNow), so the composable works both inside a
 * component (disposed on unmount) and inside a bare effectScope (disposed on stop),
 * with no reliance on onMounted/onUnmounted.
 *
 * The DTO → screen SourceStatus projection is 1:1 (documented in
 * sourceStatus.types.ts) — it decouples the strip from the generated wire type.
 * A failed fetch surfaces in `error` (never swallowed) but leaves the last-known
 * list in place so a transient blip doesn't blank the strip.
 */
import { ref, onScopeDispose } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import type { SourceStatus } from '~/components/downloads/sourceStatus.types'

type SourceStatusDTO = components['schemas']['SourceStatus']

/** Map the wire SourceStatus DTO → the screen SourceStatus (1:1, documented). */
function mapSource(dto: SourceStatusDTO): SourceStatus {
  return {
    sourceKey: dto.sourceKey,
    state: dto.state,
    activeCount: dto.activeCount,
    cap: dto.cap,
    cooldownRemainingSeconds: dto.cooldownRemainingSeconds,
    reason: dto.reason,
    consecutiveFailures: dto.consecutiveFailures,
    lastError: dto.lastError,
  }
}

export function useEngineStatus(options: { intervalMs?: number, immediate?: boolean } = {}) {
  const { intervalMs = 4000, immediate = true } = options

  const sources = ref<SourceStatus[]>([])
  const pending = ref(false)
  const error = ref<string | null>(null)

  let timer: ReturnType<typeof setInterval> | null = null

  /** Fetch the current strip; keeps the last list on failure (surfaced in `error`). */
  async function refetch(): Promise<void> {
    pending.value = true
    error.value = null
    try {
      const res = await apiClient.GET('/api/engine/sources')
      if (res.error || !res.data) throw new Error('Failed to load engine sources')
      sources.value = res.data.map(mapSource)
    }
    catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to load engine sources'
    }
    finally {
      pending.value = false
    }
  }

  function startPolling(): void {
    if (timer != null) return
    timer = setInterval(() => { void refetch() }, intervalMs)
  }

  function stopPolling(): void {
    if (timer != null) {
      clearInterval(timer)
      timer = null
    }
  }

  const isHidden = (): boolean =>
    typeof document !== 'undefined' && document.visibilityState === 'hidden'

  // Arm the interval only when the tab is visible; a return to visibility refetches
  // immediately (so the strip is never stale on re-focus) and re-arms polling.
  function syncPolling(): void {
    if (isHidden()) {
      stopPolling()
      return
    }
    void refetch()
    startPolling()
  }

  function onVisibility(): void {
    syncPolling()
  }

  if (typeof document !== 'undefined') {
    document.addEventListener('visibilitychange', onVisibility)
  }
  if (immediate && !isHidden()) void refetch()
  if (!isHidden()) startPolling()

  onScopeDispose(() => {
    stopPolling()
    if (typeof document !== 'undefined') {
      document.removeEventListener('visibilitychange', onVisibility)
    }
  })

  return { sources, pending, error, refetch }
}
