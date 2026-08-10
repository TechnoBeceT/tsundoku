/**
 * useSourceSeries — data layer for the per-source "what depends on this source"
 * impact view (QCAT-513), read from GET /api/sources/{sourceId}/series.
 *
 * It is a SINGLE-SLOT loader: `load(sourceId)` fetches one source's dependent
 * series and `reset()` clears them. The Configure dialog reveals at most one
 * source's impact panel at a time, so one instance in the owning pane suffices —
 * mirroring how the Source Health report loads the timeline for its one expanded
 * source. This is a library-wide scan, so nothing fetches until a panel is
 * actually revealed (there is no immediate/eager load — there is no source id to
 * load until the owner opens a panel).
 *
 * §16: the fetch drives `pending` (the panel's loading state) and surfaces any
 * failure in `error` (never swallowed). `summary` is the headline count line the
 * panel renders above the list.
 *
 * Public surface:
 *   rows           — the dependent series (reactive)
 *   pending        — a load is in flight
 *   error          — a load failure message (or null)
 *   activeSourceId — the source id whose series are currently loaded (or null)
 *   summary        — { total, dark } derived from rows
 *   load(sourceId) — fetch one source's dependent series
 *   reset()        — clear all state (on panel close)
 */
import { computed, ref } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import type { SourceSeriesRow, SourceSeriesSummary } from '~/components/settings/sourceSeries.types'

type SourceSeriesDTO = components['schemas']['SourceSeries']

/** Map one backend SourceSeries DTO onto the panel's SourceSeriesRow (§11). */
function mapRow(dto: SourceSeriesDTO): SourceSeriesRow {
  return {
    seriesId: dto.seriesId,
    title: dto.title,
    alternativeCount: dto.alternativeCount,
    goesDark: dto.goesDark,
    topAlternative: dto.topAlternative,
  }
}

export function useSourceSeries() {
  const rows = ref<SourceSeriesRow[]>([])
  const pending = ref(false)
  const error = ref<string | null>(null)
  const activeSourceId = ref<string | null>(null)

  const summary = computed<SourceSeriesSummary>(() => ({
    total: rows.value.length,
    dark: rows.value.filter(r => r.goesDark).length,
  }))

  /**
   * Load one source's dependent series. Marks the source active, drives `pending`,
   * and surfaces any failure in `error` (§16). A failed load leaves `rows` empty
   * so the panel shows its error state rather than stale rows from another source.
   */
  async function load(sourceId: string): Promise<void> {
    activeSourceId.value = sourceId
    pending.value = true
    error.value = null
    try {
      const res = await apiClient.GET('/api/sources/{sourceId}/series', {
        params: { path: { sourceId } },
      })
      if (res.error || !res.data) throw new Error(res.error?.message ?? 'Failed to load affected series')
      rows.value = res.data.map(mapRow)
    }
    catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to load affected series'
      rows.value = []
    }
    finally {
      pending.value = false
    }
  }

  /** Clear all state — call when the impact panel closes. */
  function reset(): void {
    rows.value = []
    pending.value = false
    error.value = null
    activeSourceId.value = null
  }

  return { rows, pending, error, activeSourceId, summary, load, reset }
}
