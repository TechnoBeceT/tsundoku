/**
 * useSeriesTracking — data layer for ONE series' tracker bindings (the
 * Series-Detail "Trackers" panel). Phase 3d: connect + bind + show. Phase 4
 * adds the manual edit sheet (`updateTrack`) + the pull/converge "Sync now"
 * action (`syncNow`).
 *
 * `loadBindings(opts?)` maps the spec's "bindings()" read (GET /api/series/{id}/
 * tracking) — renamed to avoid colliding with the `bindings` state ref itself
 * (a composable can't return two keys named `bindings`, one data one function).
 * `opts.silent` skips touching `pending`/`error` — used for the BACKGROUND
 * reconciliation refetch below, so a mutation's own optimistic update is never
 * masked by a skeleton flash. `refresh(recordId)` is kept EXACTLY as named in
 * the spec (re-pulls one binding's remote entry) since a binding-scoped
 * "refresh one" name reads best for both the API surface and its caller.
 *
 * §16 mutations, each owning its own busy/error state (never a single shared
 * flag — search vs. bind vs. unbind vs. refresh vs. update vs. sync must never
 * fight over one spinner, mirrors useMetadata):
 *   search(trackerId, q)          — GET    /api/trackers/{id}/search
 *   bind(trackerId, remoteId)     — POST   /api/series/{id}/tracking
 *   unbind(recordId, deleteRemote)— DELETE /api/series/{id}/tracking/{recordId}
 *   refresh(recordId)             — POST   /api/series/{id}/tracking/{recordId}/refresh
 *   updateTrack(recordId, patch)  — POST   /api/series/{id}/tracking/{recordId}/update
 *   syncNow()                     — POST   /api/series/{id}/tracking/sync
 * `bind`/`refresh`/`updateTrack` apply the returned, authoritative `TrackBinding`
 * directly into `bindings` (§16 mutate-reseeds-from-response); `unbind` removes
 * the row locally on a successful 204; `syncNow` REPLACES the whole `bindings`
 * list with the server's converged set (the sync endpoint returns every
 * binding, not just one, so no extra refetch is needed there).
 *
 * §17-adjacent robustness: SSE is NOT relied on for tracker state (the proxy
 * drops it unreliably). `bind`/`unbind`/`updateTrack` each already apply their
 * OWN response optimistically, but also fire a SILENT background
 * `loadBindings({ silent: true })` afterwards (best-effort, log-free — a failed
 * reconciliation GET never clobbers the already-applied optimistic state) so
 * server-side side effects the single-row response can't carry (e.g. a
 * convergence touching a DIFFERENT binding) still reach the screen without a
 * manual page refresh.
 *
 * Per-tracker search scoping (the "results leak across trackers" bug):
 * `search(trackerId, q)` clears `searchResults`/`searchError` SYNCHRONOUSLY,
 * before the request even goes out, whenever `trackerId` differs from the
 * PREVIOUS search's tracker — so a slow response for tracker A can never land
 * after the owner has already switched to tracker B's row. `clearSearch()` is
 * the immediate UI-level counterpart: `TrackersSection` calls it (via the
 * `clearSearch` emit) the moment the EXPANDED "Add tracking" row changes, so a
 * newly-opened row starts empty even before any search is run for it — it also
 * clears `bindError`, since a failed bind on tracker A must not appear to be a
 * failed bind on tracker B once the owner moves on.
 */
import { ref } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import type { TrackBinding, TrackSearchResult, UpdateTrackPatch } from '~/components/screens/seriesDetail.types'

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
    scoreFormat: dto.scoreFormat,
    startDate: dto.startDate,
    finishDate: dto.finishDate,
    private: dto.private,
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
    type: dto.type,
    startDate: dto.startDate,
    score: dto.score,
    description: dto.description,
  }
}

export function useSeriesTracking(seriesId: string) {
  const bindings = ref<TrackBinding[]>([])
  const pending = ref(false)
  const error = ref<string | null>(null)

  const searchResults = ref<TrackSearchResult[]>([])
  const searching = ref(false)
  const searchError = ref<string | null>(null)
  // The trackerId the CURRENT searchResults/searchError belong to — lets
  // search() detect a tracker switch and drop stale results synchronously.
  const lastSearchTrackerId = ref<number | null>(null)

  const binding = ref(false)
  const bindError = ref<string | null>(null)

  const unbindBusyId = ref<string | null>(null)
  const unbindError = ref<string | null>(null)
  // The TrackBinding id unbindError belongs to, so a per-row banner (TrackersSection)
  // never attaches a FAILED unbind's message to a DIFFERENT, unrelated row.
  const unbindErrorId = ref<string | null>(null)

  const refreshBusyId = ref<string | null>(null)
  const refreshError = ref<string | null>(null)
  // The TrackBinding id refreshError belongs to (same reasoning as unbindErrorId).
  const refreshErrorId = ref<string | null>(null)

  const updateBusyId = ref<string | null>(null)
  const updateError = ref<string | null>(null)

  const syncing = ref(false)
  const syncError = ref<string | null>(null)

  /**
   * Loads this series' tracker bindings (GET /api/series/{id}/tracking).
   * `{ silent: true }` (used as the post-mutation reconciliation refetch) skips
   * touching `pending`/`error` — a background best-effort GET must never flash
   * the loading skeleton over a list a mutation just optimistically updated,
   * nor overwrite that state with a hard error banner on a transient failure.
   */
  async function loadBindings(opts?: { silent?: boolean }): Promise<void> {
    const silent = opts?.silent ?? false
    if (!silent) {
      pending.value = true
      error.value = null
    }
    try {
      const res = await apiClient.GET('/api/series/{id}/tracking', { params: { path: { id: seriesId } } })
      if (res.error || !res.data) throw new Error('Failed to load tracker bindings')
      bindings.value = res.data.map(mapBinding)
    }
    catch (err) {
      if (!silent) error.value = err instanceof Error ? err.message : 'Failed to load tracker bindings'
    }
    finally {
      if (!silent) pending.value = false
    }
  }

  /**
   * Authed search of trackerId's own catalog (GET /api/trackers/{id}/search) —
   * the candidate list the owner picks from to bind. A failure clears
   * `searchResults` (never leaves a stale grid from a previous tracker/query).
   *
   * BUG-1 FIX: when `trackerId` differs from the tracker the CURRENT
   * `searchResults` belong to, the stale results/error are dropped
   * SYNCHRONOUSLY (before the request is even sent) — so switching from
   * AniList's "Add tracking" row to MyAnimeList's can never keep showing
   * AniList's results, and a slow AniList response arriving after the switch
   * can't resurrect them either.
   */
  async function search(trackerId: number, q: string): Promise<void> {
    if (trackerId !== lastSearchTrackerId.value) {
      searchResults.value = []
      searchError.value = null
    }
    lastSearchTrackerId.value = trackerId
    searching.value = true
    searchError.value = null
    try {
      const res = await apiClient.GET('/api/trackers/{id}/search', {
        params: { path: { id: trackerId }, query: { q } },
      })
      // A NEWER search for a different tracker started while this one was in
      // flight — this response is stale, drop it rather than resurrect it
      // over whatever the owner is now looking at.
      if (trackerId !== lastSearchTrackerId.value) return
      if (res.error || !res.data) throw new Error(res.error ? res.error.message : 'Search failed')
      searchResults.value = res.data.map(mapSearchResult)
    }
    catch (err) {
      if (trackerId !== lastSearchTrackerId.value) return
      searchError.value = err instanceof Error ? err.message : 'Search failed'
      searchResults.value = []
    }
    finally {
      // Only this call's own tracker still owns `searching` — a superseded
      // call must not flip it false out from under the newer, in-flight one.
      if (trackerId === lastSearchTrackerId.value) searching.value = false
    }
  }

  /**
   * Clears the "Add tracking" search state (results/error) AND any stale
   * `bindError` — called by `TrackersSection` the moment the owner switches
   * which tracker's row is expanded, so the newly-opened row starts empty
   * even before a search has run for it (the immediate UI-level half of the
   * BUG-1 fix; `search()` above is the synchronous data-layer half).
   */
  function clearSearch(): void {
    searchResults.value = []
    searchError.value = null
    bindError.value = null
  }

  /**
   * Binds this series to trackerId's remoteId entry (POST /api/series/{id}/
   * tracking). `private` (optional, default false) marks a FRESHLY-created
   * remote entry private on trackers that support it (see
   * `TrackerStatus.supportsPrivate`) — silently ignored by the backend on a
   * tracker that doesn't support it, and a no-op when the manga was already
   * tracked. Resolves true/false; on success the returned TrackBinding is
   * applied directly into `bindings` (appended, or replacing an existing row
   * for the same tracker — re-binding re-points it per the backend contract),
   * then a SILENT background `loadBindings` reconciles the whole list (BUG-3
   * fix — a bind can also converge/touch OTHER bindings server-side, which the
   * single returned row can't carry).
   */
  async function bind(trackerId: number, remoteId: string, isPrivate?: boolean): Promise<boolean> {
    binding.value = true
    bindError.value = null
    try {
      const res = await apiClient.POST('/api/series/{id}/tracking', {
        params: { path: { id: seriesId } },
        body: isPrivate ? { trackerId, remoteId, private: isPrivate } : { trackerId, remoteId },
      })
      if (res.error || !res.data) throw new Error(res.error ? res.error.message : 'Bind failed')
      const mapped = mapBinding(res.data)
      const idx = bindings.value.findIndex((b) => b.trackerId === mapped.trackerId)
      bindings.value = idx === -1
        ? [...bindings.value, mapped]
        : bindings.value.map((b, i) => (i === idx ? mapped : b))
      void loadBindings({ silent: true })
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
   * account. Resolves true/false; on success the row is removed locally, then
   * a SILENT background `loadBindings` reconciles the list (BUG-3 fix). On
   * failure `unbindErrorId` records WHICH row the error belongs to, so a
   * per-row banner never attaches it to a different binding.
   */
  async function unbind(recordId: string, deleteRemote: boolean): Promise<boolean> {
    unbindBusyId.value = recordId
    unbindError.value = null
    unbindErrorId.value = null
    try {
      const res = await apiClient.DELETE('/api/series/{id}/tracking/{recordId}', {
        params: { path: { id: seriesId, recordId }, query: { deleteRemote } },
      })
      if (res.error) throw new Error(res.error.message)
      bindings.value = bindings.value.filter((b) => b.id !== recordId)
      void loadBindings({ silent: true })
      return true
    }
    catch (err) {
      unbindError.value = err instanceof Error ? err.message : 'Unbind failed'
      unbindErrorId.value = recordId
      return false
    }
    finally {
      unbindBusyId.value = null
    }
  }

  /**
   * Re-pulls one binding's remote entry (POST /api/series/{id}/tracking/
   * {recordId}/refresh). Resolves true/false; on success the returned,
   * authoritative TrackBinding replaces the row directly (§16). On failure
   * `refreshErrorId` records WHICH row the error belongs to (same reasoning
   * as `unbindErrorId`).
   */
  async function refresh(recordId: string): Promise<boolean> {
    refreshBusyId.value = recordId
    refreshError.value = null
    refreshErrorId.value = null
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
      refreshErrorId.value = recordId
      return false
    }
    finally {
      refreshBusyId.value = null
    }
  }

  /**
   * Applies the owner's manual tracking-sheet edit (POST /api/series/{id}/
   * tracking/{recordId}/update) — only the CHANGED fields belong in `patch`
   * (the backend leaves an omitted field unchanged on the binding). Resolves
   * true/false; on success the returned, authoritative TrackBinding replaces
   * the row directly (§16, same shape as `refresh`), then a SILENT background
   * `loadBindings` reconciles the list (BUG-3 fix).
   */
  async function updateTrack(recordId: string, patch: UpdateTrackPatch): Promise<boolean> {
    updateBusyId.value = recordId
    updateError.value = null
    try {
      const res = await apiClient.POST('/api/series/{id}/tracking/{recordId}/update', {
        params: { path: { id: seriesId, recordId } },
        body: patch,
      })
      if (res.error || !res.data) throw new Error(res.error ? res.error.message : 'Update failed')
      const mapped = mapBinding(res.data)
      bindings.value = bindings.value.map((b) => (b.id === mapped.id ? mapped : b))
      void loadBindings({ silent: true })
      return true
    }
    catch (err) {
      updateError.value = err instanceof Error ? err.message : 'Update failed'
      return false
    }
    finally {
      updateBusyId.value = null
    }
  }

  /**
   * Pulls + converges every one of this series' tracker bindings (POST
   * /api/series/{id}/tracking/sync — spec §2 "conflict = MAX wins BOTH
   * directions"). Resolves true/false; on success the WHOLE `bindings` list is
   * replaced with the server's refreshed set (unlike `bind`/`refresh`/
   * `updateTrack`, this endpoint returns every binding, not one).
   */
  async function syncNow(): Promise<boolean> {
    syncing.value = true
    syncError.value = null
    try {
      const res = await apiClient.POST('/api/series/{id}/tracking/sync', {
        params: { path: { id: seriesId } },
      })
      if (res.error || !res.data) throw new Error(res.error ? res.error.message : 'Sync failed')
      bindings.value = res.data.map(mapBinding)
      return true
    }
    catch (err) {
      syncError.value = err instanceof Error ? err.message : 'Sync failed'
      return false
    }
    finally {
      syncing.value = false
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
    unbindErrorId,
    refreshBusyId,
    refreshError,
    refreshErrorId,
    updateBusyId,
    updateError,
    syncing,
    syncError,
    loadBindings,
    search,
    clearSearch,
    bind,
    unbind,
    refresh,
    updateTrack,
    syncNow,
  }
}
