/**
 * useSourceMetrics — data layer for the Settings → Source Metrics pane.
 *
 * Fetches GET /api/sources/metrics (already sorted slowest-first by the backend)
 * and maps the generated SourceMetric DTO → screen SourceMetric with the usual
 * mapper RENAMES:
 *   sourceId      → id
 *   sourceName    → name
 *   ewmaLatencyMs → avgLatencyMs
 * The three optional timestamps normalise undefined → null (matching the other
 * composable mappers, e.g. UnhealthySourceRow's `string | null`).
 *
 * warmNow() POSTs /api/sources/warmup — the manual "warm everything now" pass
 * that kicks a warm-up across anti-bot sources whose sessions have gone cold. The
 * endpoint returns 202 {started:true} IMMEDIATELY: the pass runs in the background
 * (it takes minutes over slow anti-bot sources), so warmNow surfaces a "started"
 * message rather than a done-count, then schedules a single delayed refetch so any
 * fast sources' fresh timestamps land without implying the whole pass is done.
 * Exposes the §16 trio for that action: `warming` (in flight), `warmMessage`
 * (success), `warmError` (failure). An empty metrics list ([]) is handled
 * gracefully — `metrics` simply stays empty and the pane renders its empty state.
 *
 * resetBreaker(id) POSTs /api/sources/{id}/reset-breaker — the owner "reset
 * source" action that force-clears a source's tripped anti-ban circuit-breaker,
 * then refetches so the row's cooling-down state clears. Its §16 state:
 * `resetting` (the source id in flight) + `resetError` (failure).
 *
 * By default the initial load fires on creation (`immediate: true`). Pass
 * `{ immediate: false }` to defer it — the Source Health tab does this so the
 * metrics only load when the tab is first shown (LAZY tab data), then triggers
 * the load itself via `refetch()`.
 */
import { ref } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import type { SourceBreaker, SourceMetric } from '~/components/screens/sourceHealth.types'

type SourceMetricDTO = components['schemas']['SourceMetric']
type SourceBreakerDTO = components['schemas']['SourceBreaker']

// ── DTO mapper ────────────────────────────────────────────────────────────────

/** Map the optional breaker DTO → screen SourceBreaker (undefined → null). */
function mapBreaker(dto: SourceBreakerDTO | undefined): SourceBreaker | null {
  if (dto == null) return null
  return {
    consecutiveFailures: dto.consecutiveFailures,
    cooldownUntil: dto.cooldownUntil ?? null,
    lastError: dto.lastError,
    isCoolingDown: dto.isCoolingDown,
  }
}

function mapMetric(dto: SourceMetricDTO): SourceMetric {
  return {
    id: dto.sourceId,
    name: dto.sourceName,
    avgLatencyMs: dto.ewmaLatencyMs,
    lastLatencyMs: dto.lastLatencyMs,
    searchCount: dto.searchCount,
    successCount: dto.successCount,
    failCount: dto.failCount,
    lastError: dto.lastError,
    lastErrorAt: dto.lastErrorAt ?? null,
    lastSuccessAt: dto.lastSuccessAt ?? null,
    lastWarmedAt: dto.lastWarmedAt ?? null,
    updatedAt: dto.updatedAt,
    isSlow: dto.isSlow,
    breaker: mapBreaker(dto.breaker),
  }
}

// ── Composable ────────────────────────────────────────────────────────────────

export function useSourceMetrics(options: { immediate?: boolean } = {}) {
  const { immediate = true } = options

  const metrics = ref<SourceMetric[]>([])

  const pending = ref(false)
  const error = ref<string | null>(null)

  // §16 state of the manual "Warm now" action.
  const warming = ref(false)
  const warmMessage = ref<string | null>(null)
  const warmError = ref<string | null>(null)

  // §16 state of the per-source "Reset" (breaker) action. `resetting` holds the
  // source id whose reset is in flight (so only that row's button spins);
  // `resetError` surfaces a failure.
  const resetting = ref<string | null>(null)
  const resetError = ref<string | null>(null)

  /** Load (or reload) the per-source metrics list. */
  async function refetch(): Promise<void> {
    pending.value = true
    error.value = null
    try {
      const res = await apiClient.GET('/api/sources/metrics')
      if (res.error || !res.data) throw new Error('Failed to load source metrics')
      metrics.value = res.data.map(mapMetric)
    }
    catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to load source metrics'
    }
    finally {
      pending.value = false
    }
  }

  /**
   * Kick off a manual warm-up pass across all sources. The endpoint returns 202
   * immediately (the pass runs in the background for minutes), so this surfaces a
   * "started" message rather than a completion count, and schedules ONE delayed
   * refetch so any fast sources' fresh timestamps land without implying the whole
   * pass is done. A failure lands in `warmError` (never swallowed, §16).
   */
  async function warmNow(): Promise<void> {
    warming.value = true
    warmMessage.value = null
    warmError.value = null
    try {
      const res = await apiClient.POST('/api/sources/warmup')
      if (res.error) throw new Error(res.error.message)
      warmMessage.value = 'Warm-up started — sources warm in the background (this can take a few minutes)'
      // One delayed reload so fast sources' fresh timestamps show; the slow ones
      // keep warming in the background well after this fires.
      setTimeout(() => { void refetch() }, 4000)
    }
    catch (e) {
      warmError.value = e instanceof Error ? e.message : 'Warm-up failed'
    }
    finally {
      warming.value = false
    }
  }

  /**
   * Reset one source's tripped anti-ban circuit-breaker (POST
   * /api/sources/{id}/reset-breaker), clearing its cooldown so it is immediately
   * usable again, then refetch so the row's cooling-down state disappears. `id`
   * is the source id (SourceMetric.id). A failure lands in `resetError` (never
   * swallowed, §16); the in-flight row is tracked by `resetting`.
   */
  async function resetBreaker(id: string): Promise<void> {
    resetting.value = id
    resetError.value = null
    try {
      const res = await apiClient.POST('/api/sources/{sourceId}/reset-breaker', {
        params: { path: { sourceId: id } },
      })
      if (res.error) throw new Error(res.error.message)
      await refetch()
    }
    catch (e) {
      resetError.value = e instanceof Error ? e.message : 'Failed to reset source'
    }
    finally {
      resetting.value = null
    }
  }

  if (immediate) void refetch()

  return {
    metrics,
    pending,
    error,
    warming,
    warmMessage,
    warmError,
    resetting,
    resetError,
    refetch,
    warmNow,
    resetBreaker,
  }
}
