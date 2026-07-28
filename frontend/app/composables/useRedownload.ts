/**
 * useRedownload — the owner-triggered BULK re-download, previewed then applied
 * (Settings → Sources). It re-queues already-downloaded chapters so the engine
 * fetches them again, for when a source's stored bytes turn out to be wrong but
 * every state field says the chapters are fine.
 *
 * 🔴 It re-downloads BLIND, and that is deliberate. It cannot tell which files
 * are actually damaged: image scrambling is a permutation of tiles, so it
 * preserves every pixel, the histogram, the dimensions and the edge count —
 * every cheap image statistic is permutation-invariant and cannot see it even in
 * principle. Filtering by source + a written-since cutoff and re-fetching the lot
 * is the honest remedy.
 *
 * 🔴 Nothing is deleted. Each chapter keeps its existing CBZ until the
 * replacement lands, so a failed re-download leaves the old file readable rather
 * than nothing — which is why this is a re-queue, not a destructive action.
 *
 * Two-step by design: `loadPreview` reports what the filter WOULD re-queue and
 * what it would cost (in download cycles against that one source) without
 * touching anything, and only `apply` mutates. The server RE-COMPUTES the
 * matching set on apply, so the preview is advice, never a promise.
 *
 * §16 trios, kept separate so a preview failure can never be mistaken for an
 * apply failure: `previewBusy`/`previewError` for the read, and
 * `applying`/`applyMessage`/`applyError` for the write.
 *
 * Throughput is untouched on purpose — the re-queued chapters drain at the
 * engine's normal per-source batch, which is the anti-ban throttle.
 */
import { ref } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components, operations } from '~/utils/api/schema.d.ts'

/**
 * RedownloadFilter — what the owner is asking to re-download.
 *
 * `scanlator` is PRESENCE-BASED, matching the API: null omits the parameter
 * entirely (every scanlator of the source), while a string — the empty string
 * included — narrows to that exact scanlator. A provider is a (source,
 * scanlator) pair, so "the source's all-scanlators provider" and "all of the
 * source's providers" are genuinely different sets and a plain empty string
 * could not express both.
 */
export interface RedownloadFilter {
  /** The canonical source name, exactly as Source Health lists it. */
  source: string
  /** Narrow to one scanlator, or null to match every scanlator of the source. */
  scanlator: string | null
  /** RFC 3339 instant — match chapters whose CBZ was WRITTEN at or after it. */
  since: string
}

/**
 * RedownloadPreview — the server's answer to "what would this filter do?".
 * Taken straight from the generated contract, never mirrored by hand, so a spec
 * change breaks the build instead of silently drifting.
 */
export type RedownloadPreview = components['schemas']['RedownloadPreview']

/** The query shape both endpoints take, likewise generated. */
type RedownloadQuery = operations['previewRedownload']['parameters']['query']

/**
 * Renders the filter as the query params both endpoints take. The scanlator key
 * is OMITTED rather than emptied when the owner is not narrowing — the API reads
 * presence, so an empty string would mean something different (see
 * RedownloadFilter.scanlator).
 */
function queryFor(filter: RedownloadFilter): RedownloadQuery {
  if (filter.scanlator === null) return { source: filter.source, since: filter.since }
  return { source: filter.source, scanlator: filter.scanlator, since: filter.since }
}

export function useRedownload() {
  const preview = ref<RedownloadPreview | null>(null)
  const previewBusy = ref(false)
  const previewError = ref<string | null>(null)
  const applying = ref(false)
  const applyMessage = ref<string | null>(null)
  const applyError = ref<string | null>(null)

  /** Discards any previous preview + outcome, so a changed filter never shows a stale count. */
  function reset(): void {
    preview.value = null
    previewError.value = null
    applyMessage.value = null
    applyError.value = null
  }

  /** Loads what the filter would re-queue. Mutates nothing on the server. */
  async function loadPreview(filter: RedownloadFilter): Promise<void> {
    previewBusy.value = true
    previewError.value = null
    applyMessage.value = null
    applyError.value = null
    preview.value = null
    try {
      const res = await apiClient.GET('/api/downloads/redownload', { params: { query: queryFor(filter) } })
      if (res.error || !res.data) throw new Error(res.error ? res.error.message : 'Could not preview the re-download')
      preview.value = res.data
    }
    catch (e) {
      previewError.value = e instanceof Error ? e.message : 'Could not preview the re-download'
    }
    finally {
      previewBusy.value = false
    }
  }

  /**
   * Applies the filter, re-queueing every chapter it matches. Resolves true on
   * success so the caller's confirm gate closes only when the sweep really ran.
   */
  async function apply(filter: RedownloadFilter): Promise<boolean> {
    applying.value = true
    applyMessage.value = null
    applyError.value = null
    try {
      const res = await apiClient.POST('/api/downloads/redownload', { params: { query: queryFor(filter) } })
      if (res.error || !res.data) throw new Error(res.error ? res.error.message : 'Re-download failed')
      const n = res.data.requeued
      applyMessage.value = `${n} chapter${n === 1 ? '' : 's'} re-queued. Existing files stay on disk until each replacement lands.`
      preview.value = null
      return true
    }
    catch (e) {
      applyError.value = e instanceof Error ? e.message : 'Re-download failed'
      return false
    }
    finally {
      applying.value = false
    }
  }

  return { preview, previewBusy, previewError, applying, applyMessage, applyError, loadPreview, apply, reset }
}
