/**
 * useFractionals — data layer for the library-wide Fractionals screen.
 *
 * Owns three things the screen (and its per-series cleanup dialog) drive:
 *   1. the LIST — GET /api/library/fractionals, unwrapped + mapped to
 *      SeriesFractionals[];
 *   2. the whole-series ignore-policy TOGGLE — PATCH /api/series/{id}/
 *      ignore-fractional, which flips ignore_fractional on every source at once;
 *   3. the per-series CLEANUP — GET/POST /api/series/{id}/fractional-cleanup
 *      (preview + remove), the SAME endpoints the Series-Detail page uses, so the
 *      reused FractionalCleanupDialog behaves identically here.
 *
 * State refs mirror useHealth: `pending` gates the initial skeleton; `refreshing`
 * gates the manual re-poll (keeps cards visible). A successful toggle or removal
 * re-polls the list so both counts (fractional / removable) reflect the change.
 *
 * §16: the toggle and the removal are owner-driven mutations, so their failures
 * are surfaced (`toggleError` / `removeError`), never swallowed. The preview is a
 * BACKGROUND read the owner never triggered (it fills the dialog on open), so it
 * resolves null on failure rather than shouting.
 */
import { ref } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import type { SeriesFractionals } from '~/components/screens/fractionals.types'
import type { FractionalCleanupPreview } from '~/components/screens/seriesDetail.types'

type SeriesFractionalsDTO = components['schemas']['SeriesFractionals']

/** Map one backend SeriesFractionals DTO onto the screen's SeriesFractionals. */
function mapRow(dto: SeriesFractionalsDTO): SeriesFractionals {
  return {
    seriesId: dto.seriesId,
    title: dto.title,
    displayName: dto.displayName,
    category: dto.category,
    coverUrl: dto.coverUrl,
    fractionalCount: dto.fractionalCount,
    removableCount: dto.removableCount,
    providersTotal: dto.providersTotal,
    providersIgnoring: dto.providersIgnoring,
    allProvidersIgnoring: dto.allProvidersIgnoring,
  }
}

export function useFractionals() {
  const series = ref<SeriesFractionals[]>([])
  const pending = ref(false)
  const refreshing = ref(false)
  const error = ref<string | null>(null)

  // Per-series ignore-toggle in flight — the screen dims that one card's toggle.
  const togglingIds = ref<string[]>([])
  const toggleError = ref<string | null>(null)

  // Per-series cleanup removal in flight (only one dialog is open at a time).
  const removeBusy = ref(false)
  const removeError = ref<string | null>(null)

  /** Shared list fetch; isRefresh=true toggles refreshing instead of pending. */
  async function load(isRefresh: boolean): Promise<void> {
    if (isRefresh) refreshing.value = true
    else pending.value = true
    error.value = null
    try {
      const res = await apiClient.GET('/api/library/fractionals')
      if (res.error || !res.data) throw new Error('Failed to load fractionals')
      series.value = res.data.series.map(mapRow)
    }
    catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to load fractionals'
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
   * Flip the whole-series ignore-fractional policy (every source at once) then
   * re-poll so the removable count reflects the new policy. Surfaces failures in
   * `toggleError` (§16); resolves true/false so the caller knows the outcome.
   */
  async function setIgnoreForSeries(seriesId: string, ignore: boolean): Promise<boolean> {
    if (togglingIds.value.includes(seriesId)) return false
    togglingIds.value = [...togglingIds.value, seriesId]
    toggleError.value = null
    try {
      const res = await apiClient.PATCH('/api/series/{id}/ignore-fractional', {
        params: { path: { id: seriesId } },
        body: { ignoreFractional: ignore },
      })
      if (res.error || !res.data) throw new Error(res.error ? res.error.message : 'Failed to update policy')
      await load(true)
      return true
    }
    catch (e) {
      toggleError.value = e instanceof Error ? e.message : 'Failed to update policy'
      return false
    }
    finally {
      togglingIds.value = togglingIds.value.filter((id) => id !== seriesId)
    }
  }

  /**
   * Load a series' removable-fractional preview for the cleanup dialog. Guards the
   * response SHAPE (a body without a `chapters` array reads as "nothing to clean"),
   * and resolves null on failure — a background read must never throw at the owner.
   */
  async function fetchPreview(seriesId: string): Promise<FractionalCleanupPreview | null> {
    const res = await apiClient.GET('/api/series/{id}/fractional-cleanup', { params: { path: { id: seriesId } } })
    if (res.error || !res.data || !Array.isArray(res.data.chapters)) return null
    return {
      typicalPageCount: res.data.typicalPageCount ?? 0,
      chapters: res.data.chapters.map((c) => ({
        chapterId: c.chapterId,
        number: c.number,
        pageCount: c.pageCount,
        provider: c.provider,
        filename: c.filename,
      })),
    }
  }

  /**
   * Remove the ticked fractional chapters (files + rows) for a series, then re-poll
   * the list so the counts update. `chapterIds` is a SELECTION, never an
   * authorisation — the backend re-computes the removable set and rejects any id
   * outside it. Resolves true/false; a failure keeps the dialog open with
   * `removeError` shown inside it (§16).
   */
  async function removeFractionals(seriesId: string, chapterIds: string[]): Promise<boolean> {
    removeBusy.value = true
    removeError.value = null
    try {
      const res = await apiClient.POST('/api/series/{id}/fractional-cleanup', {
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
    togglingIds,
    toggleError,
    removeBusy,
    removeError,
    refresh,
    setIgnoreForSeries,
    fetchPreview,
    removeFractionals,
  }
}
