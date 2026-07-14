/**
 * useSeriesTracking — data layer for ONE series' tracker bindings (the
 * Series-Detail "Trackers" panel, Phase 3d: connect + bind + show — the edit
 * sheet for status/score/dates is Phase 4).
 *
 * `loadBindings()` maps the spec's "bindings()" read (GET /api/series/{id}/
 * tracking) — renamed to avoid colliding with the `bindings` state ref itself
 * (a composable can't return two keys named `bindings`, one data one function).
 * `refresh(recordId)` is kept EXACTLY as named in the spec (re-pulls one
 * binding's remote entry) since a binding-scoped "refresh one" name reads best
 * for both the API surface and its caller.
 *
 * §16 mutations, each owning its own busy/error state (never a single shared
 * flag — search vs. bind vs. unbind vs. refresh must never fight over one
 * spinner, mirrors useMetadata):
 *   search(trackerId, q)          — GET    /api/trackers/{id}/search
 *   bind(trackerId, remoteId)     — POST   /api/series/{id}/tracking
 *   unbind(recordId, deleteRemote)— DELETE /api/series/{id}/tracking/{recordId}
 *   refresh(recordId)             — POST   /api/series/{id}/tracking/{recordId}/refresh
 * `bind`/`refresh` apply the returned, authoritative `TrackBinding` directly
 * into `bindings` (§16 mutate-reseeds-from-response — no extra list round-trip);
 * `unbind` removes the row locally on a successful 204.
 */
import { ref } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import type { TrackBinding, TrackSearchResult } from '~/components/screens/seriesDetail.types'

type TrackBindingDTO = components['schemas']['TrackBinding']
type TrackSearchResultDTO = components['schemas']['TrackSearchResult']

function mapBinding(dto: TrackBindingDTO): TrackBinding {
  return {
    id: dto.id,
    trackerId: dto.trackerId,
    trackerName: dto.trackerName,
    remoteId: dto.remoteId,
    remoteUrl: dto.remoteUrl,
    title: dto.title,
    status: dto.status,
    lastChapterRead: dto.lastChapterRead,
    totalChapters: dto.totalChapters,
    score: dto.score,
  }
}

function mapSearchResult(dto: TrackSearchResultDTO): TrackSearchResult {
  return {
    remoteId: dto.remoteId,
    title: dto.title,
    url: dto.url,
    coverUrl: dto.coverUrl,
    status: dto.status,
    totalChapters: dto.totalChapters,
  }
}

export function useSeriesTracking(seriesId: string) {
  const bindings = ref<TrackBinding[]>([])
  const pending = ref(false)
  const error = ref<string | null>(null)

  const searchResults = ref<TrackSearchResult[]>([])
  const searching = ref(false)
  const searchError = ref<string | null>(null)

  const binding = ref(false)
  const bindError = ref<string | null>(null)

  const unbindBusyId = ref<string | null>(null)
  const unbindError = ref<string | null>(null)

  const refreshBusyId = ref<string | null>(null)
  const refreshError = ref<string | null>(null)

  /** Loads this series' tracker bindings (GET /api/series/{id}/tracking). */
  async function loadBindings(): Promise<void> {
    pending.value = true
    error.value = null
    try {
      const res = await apiClient.GET('/api/series/{id}/tracking', { params: { path: { id: seriesId } } })
      if (res.error || !res.data) throw new Error('Failed to load tracker bindings')
      bindings.value = res.data.map(mapBinding)
    }
    catch (err) {
      error.value = err instanceof Error ? err.message : 'Failed to load tracker bindings'
    }
    finally {
      pending.value = false
    }
  }

  /**
   * Authed search of trackerId's own catalog (GET /api/trackers/{id}/search) —
   * the candidate list the owner picks from to bind. A failure clears
   * `searchResults` (never leaves a stale grid from a previous tracker/query).
   */
  async function search(trackerId: number, q: string): Promise<void> {
    searching.value = true
    searchError.value = null
    try {
      const res = await apiClient.GET('/api/trackers/{id}/search', {
        params: { path: { id: trackerId }, query: { q } },
      })
      if (res.error || !res.data) throw new Error(res.error ? res.error.message : 'Search failed')
      searchResults.value = res.data.map(mapSearchResult)
    }
    catch (err) {
      searchError.value = err instanceof Error ? err.message : 'Search failed'
      searchResults.value = []
    }
    finally {
      searching.value = false
    }
  }

  /**
   * Binds this series to trackerId's remoteId entry (POST /api/series/{id}/
   * tracking). Resolves true/false; on success the returned TrackBinding is
   * applied directly into `bindings` (appended, or replacing an existing row
   * for the same tracker — re-binding re-points it per the backend contract).
   */
  async function bind(trackerId: number, remoteId: string): Promise<boolean> {
    binding.value = true
    bindError.value = null
    try {
      const res = await apiClient.POST('/api/series/{id}/tracking', {
        params: { path: { id: seriesId } },
        body: { trackerId, remoteId },
      })
      if (res.error || !res.data) throw new Error(res.error ? res.error.message : 'Bind failed')
      const mapped = mapBinding(res.data)
      const idx = bindings.value.findIndex((b) => b.trackerId === mapped.trackerId)
      bindings.value = idx === -1
        ? [...bindings.value, mapped]
        : bindings.value.map((b, i) => (i === idx ? mapped : b))
      return true
    }
    catch (err) {
      bindError.value = err instanceof Error ? err.message : 'Bind failed'
      return false
    }
    finally {
      binding.value = false
    }
  }

  /**
   * Removes a binding (DELETE /api/series/{id}/tracking/{recordId}); when
   * `deleteRemote` the remote entry is also deleted from the tracker's own
   * account. Resolves true/false; on success the row is removed locally.
   */
  async function unbind(recordId: string, deleteRemote: boolean): Promise<boolean> {
    unbindBusyId.value = recordId
    unbindError.value = null
    try {
      const res = await apiClient.DELETE('/api/series/{id}/tracking/{recordId}', {
        params: { path: { id: seriesId, recordId }, query: { deleteRemote } },
      })
      if (res.error) throw new Error(res.error.message)
      bindings.value = bindings.value.filter((b) => b.id !== recordId)
      return true
    }
    catch (err) {
      unbindError.value = err instanceof Error ? err.message : 'Unbind failed'
      return false
    }
    finally {
      unbindBusyId.value = null
    }
  }

  /**
   * Re-pulls one binding's remote entry (POST /api/series/{id}/tracking/
   * {recordId}/refresh). Resolves true/false; on success the returned,
   * authoritative TrackBinding replaces the row directly (§16).
   */
  async function refresh(recordId: string): Promise<boolean> {
    refreshBusyId.value = recordId
    refreshError.value = null
    try {
      const res = await apiClient.POST('/api/series/{id}/tracking/{recordId}/refresh', {
        params: { path: { id: seriesId, recordId } },
      })
      if (res.error || !res.data) throw new Error(res.error ? res.error.message : 'Refresh failed')
      const mapped = mapBinding(res.data)
      bindings.value = bindings.value.map((b) => (b.id === mapped.id ? mapped : b))
      return true
    }
    catch (err) {
      refreshError.value = err instanceof Error ? err.message : 'Refresh failed'
      return false
    }
    finally {
      refreshBusyId.value = null
    }
  }

  void loadBindings()

  return {
    bindings,
    pending,
    error,
    searchResults,
    searching,
    searchError,
    binding,
    bindError,
    unbindBusyId,
    unbindError,
    refreshBusyId,
    refreshError,
    loadBindings,
    search,
    bind,
    unbind,
    refresh,
  }
}
