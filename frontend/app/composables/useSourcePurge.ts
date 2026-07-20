/**
 * useSourcePurge — the data layer for the per-source PURGE action in the Source
 * Health "Sources" tab. Purging removes ALL of Tsundoku's DB state for one
 * physical source (its dangling SeriesProviders + feeds, its metric row, its
 * circuit-breaker row) and honestly un-pins any chapter left sourceless — WITHOUT
 * deleting a single downloaded CBZ file. The owner used to clean this up
 * series-by-series by hand after uninstalling a source's extension.
 *
 * Because it is destructive it is a two-step flow:
 *   1. `start(source)` opens the confirm modal and loads a dry-run PREVIEW
 *      (GET /api/engine/purge-source/preview) so the owner sees the blast radius
 *      (how many series / providers / chapters / metric+breaker rows) before
 *      committing.
 *   2. `confirm()` runs the cascade (POST /api/engine/purge-source) and resolves
 *      true on success so the caller can refetch the metrics list (the purged
 *      source's row then vanishes — the §16 visible confirmation).
 *
 * Exposes the §16 trio for both phases: `previewing`/`purging` (in flight) and
 * `error` (a failure, never swallowed). One purge is in flight at a time.
 */
import { ref } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'

type SourcePurgePreviewDTO = components['schemas']['SourcePurgePreview']

/** The identity of the source to purge (both halves are required server-side). */
export interface PurgeTarget {
  id: string
  name: string
}

export function useSourcePurge() {
  const open = ref(false)
  const target = ref<PurgeTarget | null>(null)
  const preview = ref<SourcePurgePreviewDTO | null>(null)
  const previewing = ref(false)
  const purging = ref(false)
  const error = ref<string | null>(null)

  /** Open the confirm modal for `source` and load its dry-run preview counts. */
  async function start(source: PurgeTarget): Promise<void> {
    target.value = source
    preview.value = null
    error.value = null
    open.value = true
    previewing.value = true
    try {
      const res = await apiClient.GET('/api/engine/purge-source/preview', {
        params: { query: { sourceId: source.id, sourceName: source.name } },
      })
      if (res.error || !res.data) throw new Error(res.error?.message ?? 'Failed to load purge preview')
      preview.value = res.data
    }
    catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to load purge preview'
    }
    finally {
      previewing.value = false
    }
  }

  /** Close the modal and reset all transient state. */
  function close(): void {
    open.value = false
    target.value = null
    preview.value = null
    error.value = null
  }

  /**
   * Run the purge cascade for the current target. Resolves true on success (the
   * modal closes; the caller should refetch metrics) or false on failure (the
   * modal stays open with `error` set so the owner can retry or cancel).
   */
  async function confirm(): Promise<boolean> {
    if (target.value == null) return false
    purging.value = true
    error.value = null
    try {
      const res = await apiClient.POST('/api/engine/purge-source', {
        body: { sourceId: target.value.id, sourceName: target.value.name },
      })
      if (res.error) throw new Error(res.error.message)
      close()
      return true
    }
    catch (e) {
      error.value = e instanceof Error ? e.message : 'Purge failed'
      return false
    }
    finally {
      purging.value = false
    }
  }

  return { open, target, preview, previewing, purging, error, start, confirm, close }
}
