/**
 * useSourceless — data layer for the library-wide Sourceless screen.
 *
 * Owns two things the screen (and its per-series cleanup dialog) drive:
 *   1. the LIST — GET /api/library/sourceless, unwrapped + mapped to
 *      SeriesSourceless[];
 *   2. the per-series CLEANUP — GET/POST /api/series/{id}/sourceless-cleanup
 *      (preview + remove).
 *
 * Unlike useFractionals there is no whole-series ignore-policy toggle: a
 * sourceless chapter has no remaining carrier to flag, so there is nothing to
 * ignore — the removal endpoints are the whole surface.
 *
 * State refs mirror useHealth: `pending` gates the initial skeleton; `refreshing`
 * gates the manual re-poll (keeps cards visible). A successful removal re-polls
 * the list so the count reflects the change.
 *
 * §16: the removal is an owner-driven mutation, so its failure is surfaced
 * (`removeError`), never swallowed. The preview is a BACKGROUND read the owner
 * never triggered (it fills the dialog on open), so it resolves null on failure
 * rather than shouting.
 */
import { ref } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import type { SeriesSourceless, SourcelessCleanupPreview } from '~/components/screens/sourceless.types'

type SeriesSourcelessDTO = components['schemas']['SeriesSourcelessRow']

/** Map one backend SeriesSourcelessRow DTO onto the screen's SeriesSourceless. */
function mapRow(dto: SeriesSourcelessDTO): SeriesSourceless {
  return {
    seriesId: dto.seriesId,
    title: dto.title,
    displayName: dto.displayName,
    category: dto.category,
    coverUrl: dto.coverUrl,
    sourcelessCount: dto.sourcelessCount,
  }
}

export function useSourceless() {
  const series = ref<SeriesSourceless[]>([])
  const pending = ref(false)
  const refreshing = ref(false)
  const error = ref<string | null>(null)

  // Per-series cleanup removal in flight (only one dialog is open at a time).
  const removeBusy = ref(false)
  const removeError = ref<string | null>(null)

  /** Shared list fetch; isRefresh=true toggles refreshing instead of pending. */
  async function load(isRefresh: boolean): Promise<void> {
    if (isRefresh) refreshing.value = true
    else pending.value = true
    error.value = null
    try {
      const res = await apiClient.GET('/api/library/sourceless')
      if (res.error || !res.data) throw new Error('Failed to load sourceless chapters')
      series.value = res.data.series.map(mapRow)
    }
    catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to load sourceless chapters'
    }
    finally {
      if (isRefresh) refreshing.value = false
      else pending.value = false
    }
  }

  /** Manual re-poll — keeps existing cards visible; toggles refreshing, not pending. */
  function refresh(): void {
    void load(true)
  }

  /**
   * Load a series' removable-sourceless preview for the cleanup dialog. Guards the
   * response SHAPE (a body without a `chapters` array reads as "nothing to clean"),
   * and resolves null on failure — a background read must never throw at the owner.
   */
  async function fetchPreview(seriesId: string): Promise<SourcelessCleanupPreview | null> {
    const res = await apiClient.GET('/api/series/{id}/sourceless-cleanup', { params: { path: { id: seriesId } } })
    if (res.error || !res.data || !Array.isArray(res.data.chapters)) return null
    return {
      chapters: res.data.chapters.map((c) => ({
        chapterId: c.chapterId,
        number: c.number ?? null,
        pageCount: c.pageCount,
        provider: c.provider,
        filename: c.filename,
      })),
    }
  }

  /**
   * Remove the ticked sourceless chapters (files + rows) for a series, then re-poll
   * the list so the count updates. `chapterIds` is a SELECTION, never an
   * authorisation — the backend re-computes the removable set and rejects any id
   * outside it. Resolves true/false; a failure keeps the dialog open with
   * `removeError` shown inside it (§16).
   */
  async function removeSourceless(seriesId: string, chapterIds: string[]): Promise<boolean> {
    removeBusy.value = true
    removeError.value = null
    try {
      const res = await apiClient.POST('/api/series/{id}/sourceless-cleanup', {
        params: { path: { id: seriesId } },
        body: { chapterIds },
      })
      if (res.error || !res.data) throw new Error(res.error ? res.error.message : 'Failed to remove files')
      await load(true)
      return true
    }
    catch (e) {
      removeError.value = e instanceof Error ? e.message : 'Failed to remove files'
      return false
    }
    finally {
      removeBusy.value = false
    }
  }

  // Kick off the initial load immediately (mirrors useHealth).
  void load(false)

  return {
    series,
    pending,
    refreshing,
    error,
    removeBusy,
    removeError,
    refresh,
    fetchPreview,
    removeSourceless,
  }
}
