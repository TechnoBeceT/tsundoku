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
 */
import { ref } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import type { SourceMetric } from '~/components/screens/settings.types'

type SourceMetricDTO = components['schemas']['SourceMetric']

// ── DTO mapper ────────────────────────────────────────────────────────────────

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
  }
}

// ── Composable ────────────────────────────────────────────────────────────────

export function useSourceMetrics() {
  const metrics = ref<SourceMetric[]>([])

  const pending = ref(false)
  const error = ref<string | null>(null)

  // §16 state of the manual "Warm now" action.
  const warming = ref(false)
  const warmMessage = ref<string | null>(null)
  const warmError = ref<string | null>(null)

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

  void refetch()

  return { metrics, pending, error, warming, warmMessage, warmError, refetch, warmNow }
}
