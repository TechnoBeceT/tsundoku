/**
 * useSeriesDetail — data layer for the series detail screen.
 *
 * Fetches GET /api/series/{id} and GET /api/categories in parallel, maps the
 * generated backend DTOs onto the screen's SeriesDetail type and a
 * categoryOptions string[] (category NAMES for the recategorize select), and
 * exposes all mutations with the §16 busy/error/refetch pattern.
 *
 * Category name→id resolution: the PATCH /api/series/{id}/category body needs
 * a UUID, but the screen emits a NAME. A categoryMap (name→id) is built from
 * the /api/categories response and consulted by setCategory — unknown names set
 * error and bail early.
 *
 * DTO→screen field notes:
 *   title         ← dto.displayName        (same as useLibrary)
 *   coverUrl      ← dto.coverUrl           (already the proxy path; pass-through)
 *   metadataProviderId ← providers.find(isMetadataSource)?.id ?? null
 *   Chapter.state is typed as string in the DTO → cast to ChapterState
 *   Chapter.read / .lastReadPage / .readAt ← passed straight through (the
 *     in-app reader's persisted progress; Task 7 surfaces them on the row)
 *   Provider.newestChapterAt / lastSyncedAt are optional in DTO → ?? null
 *   Provider.feedCount / feedRanges ← the source's STORED ProviderChapter feed
 *     (what it offers) — distinct from chapterCount (what it currently supplies).
 *     Both ride the series-detail response, so the Sources panel needs NO extra
 *     request and — crucially — no live call to the source to show coverage.
 *
 * Native-metadata-engine rich fields (RichSeriesCard, Slice D):
 *   status/genres/tags/year/links ← pass-through; year 0 (unidentified) → undefined
 *     so RichSeriesCard's `v-if="series.year !== undefined"` badge hides correctly.
 *   altTitles ← dto.altTitles.map(name)   (the card renders names only, not type/lang)
 *   authors   ← dto.authors.map(name)     (the card renders names only, not role)
 *   metadataSource / coverSource ← pass-through (null until identified/cover-picked)
 *   metadataLocked ← pass-through (true once the owner hand-curates via Identify/
 *     IdentifyMerge; the background auto-identify pass never overwrites a locked series)
 *   description ← dto.description || undefined (RichSeriesMeta field is optional;
 *     "" on an unidentified series collapses to undefined so RichSeriesCard's
 *     `v-if="series.description"` hides the synopsis block cleanly)
 */
import { ref, watch, onUnmounted } from 'vue'
import type { Ref } from 'vue'
import { apiClient } from '~/utils/api/client'
import { useProgressStream } from '~/composables/useProgressStream'
import type { components } from '~/utils/api/schema.d.ts'
import type { SeriesDetail, Chapter, ChapterState, Provider, FractionalCleanupPreview, DedupePlan } from '~/components/screens/seriesDetail.types'

type SeriesDetailDTO = components['schemas']['SeriesDetail']
type ChapterDTO = components['schemas']['Chapter']
type ProviderDTO = components['schemas']['Provider']

/**
 * MatchDiskProviderPayload — the body of `POST
 * /api/series/{id}/providers/{providerId}/match` (the `AddProviderRequest`
 * shape): the real Suwayomi source/scanlator to attribute an unlinked
 * disk-origin provider's existing chapters to, plus the importance to assign
 * the newly-linked provider.
 */
export interface MatchDiskProviderPayload {
  /** Suwayomi source ID to attach. */
  source: string
  /** DEPRECATED, unused by the backend — see SearchCandidate.mangaId. */
  mangaId: number
  /** Source-relative manga URL the backend addresses this manga by (P2 Suwayomi-removal). */
  url: string
  /** Priority to assign the newly-linked provider (higher = preferred). */
  importance: number
  /** Scanlation group to track; "" (or omitted) = all chapters from the source. */
  scanlator?: string
}

function mapChapter(dto: ChapterDTO): Chapter {
  return {
    id: dto.id,
    chapterKey: dto.chapterKey,
    number: dto.number,
    name: dto.name,
    state: dto.state as ChapterState,
    filename: dto.filename,
    pageCount: dto.pageCount,
    read: dto.read,
    lastReadPage: dto.lastReadPage,
    readAt: dto.readAt,
    releaseDate: dto.releaseDate,
  }
}

function mapProvider(dto: ProviderDTO): Provider {
  return {
    id: dto.id,
    provider: dto.provider,
    providerName: dto.providerName,
    linked: dto.linked,
    mangaId: dto.mangaId,
    chapterCount: dto.chapterCount,
    feedCount: dto.feedCount,
    feedRanges: dto.feedRanges,
    hasFeed: dto.hasFeed,
    fractionalCount: dto.fractionalCount,
    fractionalChapters: dto.fractionalChapters,
    ignoreFractional: dto.ignoreFractional,
    scanlator: dto.scanlator,
    language: dto.language,
    importance: dto.importance,
    health: dto.health,
    chaptersBehind: dto.chaptersBehind,
    newestChapterAt: dto.newestChapterAt ?? null,
    lastSyncedAt: dto.lastSyncedAt ?? null,
    lastError: dto.lastError,
  }
}

function mapDetail(dto: SeriesDetailDTO): SeriesDetail {
  return {
    id: dto.id,
    title: dto.displayName,
    slug: dto.slug,
    category: dto.category,
    coverUrl: dto.coverUrl,
    monitored: dto.monitored,
    completed: dto.completed,
    needsSource: dto.needsSource,
    chapterCounts: {
      total: dto.chapterCounts.total,
      downloaded: dto.chapterCounts.downloaded,
      wanted: dto.chapterCounts.wanted,
      failed: dto.chapterCounts.failed,
      unread: dto.chapterCounts.unread,
    },
    createdAt: dto.createdAt,
    lastChapterDownloadedAt: dto.lastChapterDownloadedAt,
    latestChapterAt: dto.latestChapterAt,
    isStalled: dto.isStalled,
    chapters: dto.chapters.map(mapChapter),
    providers: dto.providers.map(mapProvider),
    metadataProviderId: dto.providers.find((p) => p.isMetadataSource)?.id ?? null,
    description: dto.description || undefined,
    status: dto.status || undefined,
    genres: dto.genres,
    tags: dto.tags,
    altTitles: dto.altTitles.map((a) => a.name),
    authors: dto.authors.map((a) => a.name),
    year: dto.year > 0 ? dto.year : undefined,
    links: dto.links,
    metadataSource: dto.metadataSource,
    coverSource: dto.coverSource,
    metadataLocked: dto.metadataLocked,
  }
}

export function useSeriesDetail(id: string) {
  const series = ref<SeriesDetail | null>(null)
  const categoryOptions = ref<string[]>([])
  const categoryMap = ref(new Map<string, string>())
  const pending = ref(false)
  const error = ref<string | null>(null)
  const saving = ref(false)
  const deleteBusy = ref(false)
  const removeBusy = ref(false)
  const matchBusy = ref(false)
  const dedupBusy = ref(false)
  const dedupePreviewBusy = ref(false)
  const dedupeFilesBusy = ref(false)
  const fractionalBusy = ref(false)
  const dedupMessage = ref<string | null>(null)
  const settingProgress = ref(false)
  const progressError = ref<string | null>(null)

  async function refresh(): Promise<void> {
    pending.value = true
    error.value = null
    try {
      const [s, c] = await Promise.all([
        apiClient.GET('/api/series/{id}', { params: { path: { id } } }),
        apiClient.GET('/api/categories'),
      ])
      if (s.error || !s.data) throw new Error('Failed to load series')
      series.value = mapDetail(s.data)
      if (c.data) {
        const map = new Map<string, string>()
        const names: string[] = []
        for (const cat of c.data) {
          map.set(cat.name, cat.id)
          names.push(cat.name)
        }
        categoryMap.value = map
        categoryOptions.value = names
      }
    }
    catch (err) {
      error.value = err instanceof Error ? err.message : 'Failed to load series'
    }
    finally {
      pending.value = false
    }
  }

  /**
   * Shared mutation wrapper: set busy, clear error, call fn, check result.error,
   * then call onSuccess (default: refresh). Any throw sets error.value.
   * busyRef defaults to `saving`; pass deleteBusy/removeBusy for those actions.
   * onSuccess defaults to refresh; pass a nav callback for deleteSeries.
   *
   * Resolves TRUE when the mutation succeeded and FALSE when it failed (the
   * failure is surfaced via `error`, never swallowed). Callers that own a
   * dialog use that outcome to close ONLY on success — a failed mutation keeps
   * the dialog open with its error visible (§16). Callers that don't care may
   * ignore the returned value.
   */
  async function mutate(
    fn: () => Promise<{ error?: unknown }>,
    busyRef: Ref<boolean> = saving,
    onSuccess: () => void | Promise<void> = refresh,
  ): Promise<boolean> {
    busyRef.value = true
    error.value = null
    try {
      const result = await fn()
      if (result.error) throw new Error('Update failed')
      await onSuccess()
      return true
    }
    catch (err) {
      error.value = err instanceof Error ? err.message : 'Update failed'
      return false
    }
    finally {
      busyRef.value = false
    }
  }

  const setMonitored = (monitored: boolean): Promise<boolean> =>
    mutate(() => apiClient.PATCH('/api/series/{id}/monitored', { params: { path: { id } }, body: { monitored } }))

  const setCompleted = (completed: boolean): Promise<boolean> =>
    mutate(() => apiClient.PATCH('/api/series/{id}/completed', { params: { path: { id } }, body: { completed } }))

  const setCategory = async (name: string): Promise<boolean> => {
    const categoryId = categoryMap.value.get(name)
    if (!categoryId) {
      error.value = `Unknown category: ${name}`
      return false
    }
    return mutate(() => apiClient.PATCH('/api/series/{id}/category', { params: { path: { id } }, body: { categoryId } }))
  }

  const reorderProviders = (providers: { id: string, importance: number }[]): Promise<boolean> =>
    mutate(() => apiClient.PATCH('/api/series/{id}/providers', { params: { path: { id } }, body: { providers } }))

  /**
   * Removes ONE source feed from the series (the downloaded CBZs + chapters are
   * kept). Resolves true on success / false on failure, so the owner of the
   * remove-source confirm dialog closes it only when the source really went
   * away — a failed removal keeps the dialog open with the error shown (§16).
   */
  const removeSource = (providerId: string): Promise<boolean> =>
    mutate(
      () => apiClient.DELETE('/api/series/{id}/providers/{providerId}', { params: { path: { id, providerId } } }),
      removeBusy,
    )

  /**
   * Flags ONE source as a fractional re-uploader for this series (or clears the
   * flag): the source stops contributing fractional-numbered chapters (5.1,
   * 5.5 …) — they are dropped at ingest and excluded from candidacy.
   *
   * It deletes NOTHING: already-downloaded files and existing chapters stay,
   * and un-ticking restores the source immediately. Resolves true on success
   * (the row re-renders from the refreshed detail), false on failure (surfaced
   * via `error`, never swallowed).
   */
  const setIgnoreFractional = (providerId: string, ignoreFractional: boolean): Promise<boolean> =>
    mutate(() => apiClient.PATCH('/api/series/{id}/providers/{providerId}/ignore-fractional', {
      params: { path: { id, providerId } },
      body: { ignoreFractional },
    }))

  const chooseMetadataSource = (providerId: string): Promise<boolean> =>
    mutate(() => apiClient.PATCH('/api/series/{id}/metadata-source', { params: { path: { id } }, body: { providerId } }))

  const deleteSeries = (deleteFiles: boolean): Promise<boolean> =>
    mutate(
      () => apiClient.DELETE('/api/series/{id}', { params: { path: { id }, query: { deleteFiles } } }),
      deleteBusy,
      () => { void navigateTo('/') },
    )

  /**
   * Matches a disk-origin (unlinked) provider to a real Suwayomi source: the
   * backend attaches the source, re-points every chapter it already satisfies
   * onto it (no re-download), and drops the now-redundant disk-origin row.
   *
   * ASYNC (GAP-096): this merge legitimately runs for MINUTES for a large
   * provider (a slow source fetch + rewriting every CBZ over NFS), so the
   * endpoint no longer returns the refreshed detail inline — it returns 202
   * immediately (or 409 if one is already in flight for this series+provider),
   * runs the merge detached (disconnect-proof), and emits a `provider.merged`
   * SSE event on completion. So this only KICKS OFF the merge: it keeps the
   * "matching…" busy state (surfaced via `dedupMessage` on the Sources panel),
   * resolves true so the dialog closes, and the SSE listener below clears the
   * busy state + refetches (or surfaces the error) when the merge lands. A hard
   * failure to even start (not 202/409) is surfaced via `error` (§16), never
   * swallowed, and resolves false so the dialog stays open.
   */
  const matchDiskProvider = async (providerId: string, payload: MatchDiskProviderPayload): Promise<boolean> => {
    matchBusy.value = true
    error.value = null
    dedupMessage.value = null
    try {
      const res = await apiClient.POST('/api/series/{id}/providers/{providerId}/match', {
        params: { path: { id, providerId } },
        body: payload,
      })
      // 202 = launched; 409 = one already in flight for this series+provider — in
      // both cases the merge runs in the background and completes via the
      // provider.merged SSE event, so keep the busy state and let the dialog close.
      if (res.response.status === 202 || res.response.status === 409) {
        matchingProviderId.value = providerId
        dedupMessage.value = 'Matching in progress…'
        return true
      }
      throw new Error(res.error && 'message' in res.error ? String(res.error.message) : 'Match failed')
    }
    catch (err) {
      matchBusy.value = false
      error.value = err instanceof Error ? err.message : 'Match failed'
      return false
    }
  }

  // The disk provider a match is currently folding away (set while a match is in
  // flight, cleared on completion) — the reconnect reconcile below checks whether
  // it has been drained yet. For a multi-provider consolidation it holds ONE of
  // the folded ids (all fold away), so the same "still present?" reconcile applies.
  const matchingProviderId = ref<string | null>(null)

  /**
   * ConsolidateTarget — the survivor a consolidation folds into: EITHER an
   * existing provider on the series (existingProviderId) OR a match-to-real-source
   * spec. Exactly one arm is set (mirrors the backend ConsolidateProvidersRequest).
   */
  type ConsolidateTarget =
    | { existingProviderId: string }
    | { source: { source: string, url: string, scanlator?: string, importance: number } }

  /**
   * Folds a SET of the series' providers into ONE survivor WITHOUT re-downloading
   * (QCAT-295 Part B, POST /api/series/{id}/providers/consolidate). Mirrors
   * matchDiskProvider's ASYNC UX exactly: the endpoint returns 202 (or 409 when a
   * consolidation for this series is already in flight), runs the merge detached
   * (disconnect-proof), and emits the SAME `provider.merged` SSE event on
   * completion — so this only KICKS OFF the merge, keeps the "merging…" busy state
   * (matchBusy), resolves true so the target dialog closes, and the shared
   * provider.merged listener below clears the busy state + refetches (or surfaces
   * the error) when it lands. A hard failure to even start is surfaced via `error`
   * (§16) and resolves false so the dialog stays open.
   */
  const consolidateProviders = async (providerIds: string[], target: ConsolidateTarget): Promise<boolean> => {
    matchBusy.value = true
    error.value = null
    dedupMessage.value = null
    try {
      const res = await apiClient.POST('/api/series/{id}/providers/consolidate', {
        params: { path: { id } },
        body: { providerIds, target },
      })
      if (res.response.status === 202 || res.response.status === 409) {
        // Track ONE of the folded ids so the reconnect-reconcile can detect
        // completion (it disappears from the provider list once merged).
        matchingProviderId.value = providerIds[0] ?? null
        dedupMessage.value = 'Merging sources…'
        return true
      }
      throw new Error(res.error && 'message' in res.error ? String(res.error.message) : 'Merge failed')
    }
    catch (err) {
      matchBusy.value = false
      error.value = err instanceof Error ? err.message : 'Merge failed'
      return false
    }
  }

  const { on: onProgress, connected } = useProgressStream()

  // provider.merged fires when an async match/merge finishes (see
  // StartMatchDiskProvider). When it names THIS series, clear the busy state and
  // either surface the failure (§16 — never swallowed) or refetch the now-merged
  // detail. Cleaned up on unmount.
  const unsubMerged = onProgress('provider.merged', (data) => {
    const payload = data as { seriesId?: string, error?: string }
    if (payload.seriesId !== id) return
    matchBusy.value = false
    matchingProviderId.value = null
    if (payload.error) {
      dedupMessage.value = null
      error.value = payload.error
      return
    }
    dedupMessage.value = 'Match complete'
    void refresh()
  })

  // Reconnect reconcile: a match can run for MINUTES, so if the SSE stream
  // drops+reconnects mid-merge (laptop sleep / network blip) the provider.merged
  // event is MISSED and the "Matching…" indicator would stick forever. On every
  // (re)connect while a match is in flight, refetch: if the matched disk provider
  // is gone the merge finished → clear the indicator; if it is still there the
  // merge is still running (or failed) → stay busy and wait for the next event.
  const stopReconnectWatch = watch(connected, async (isConnected) => {
    if (!isConnected || !matchBusy.value) return
    await refresh()
    const stillPresent = matchingProviderId.value != null
      && !!series.value?.providers.some((p) => p.id === matchingProviderId.value)
    if (!stillPresent) {
      matchBusy.value = false
      matchingProviderId.value = null
      dedupMessage.value = 'Match complete'
    }
  })

  onUnmounted(() => { unsubMerged(); stopReconnectWatch() })

  /**
   * Folds every already-drifted disk/live duplicate source pair on this series
   * into one row (no re-download) via POST /api/series/{id}/providers/dedup.
   * Reseeds `series` DIRECTLY from the authoritative response (§16, like
   * matchDiskProvider), and surfaces the merged/skipped counts in dedupMessage.
   * Errors set `error`, never swallowed; `series` is left untouched on failure.
   */
  const dedupProviders = async (): Promise<void> => {
    dedupBusy.value = true
    error.value = null
    dedupMessage.value = null
    try {
      const res = await apiClient.POST('/api/series/{id}/providers/dedup', { params: { path: { id } } })
      if (res.error || !res.data) throw new Error(res.error ? res.error.message : 'Dedup failed')
      series.value = mapDetail(res.data.series)
      const { merged, skipped } = res.data
      dedupMessage.value = skipped > 0
        ? `Merged ${merged} duplicate source${merged === 1 ? '' : 's'}, skipped ${skipped}`
        : `Merged ${merged} duplicate source${merged === 1 ? '' : 's'}`
    }
    catch (err) {
      error.value = err instanceof Error ? err.message : 'Dedup failed'
    }
    finally {
      dedupBusy.value = false
    }
  }

  /**
   * Loads the DRY-RUN for the duplicate-file sweep (GET /api/series/{id}/
   * dedupe-files): the exact set of files (and duplicate/ignored chapter rows) a
   * subsequent POST would delete, grouped by reason. This is what the confirm
   * dialog lists BEFORE the destructive call, so the owner sees what will go.
   *
   * Unlike the fractional preview (a background read on mount), THIS is an
   * owner-triggered fetch (they clicked "Remove duplicate files"), so a failure is
   * surfaced via `error` (§16 — not swallowed) and resolves null. `dedupePreviewBusy`
   * spins the button while it loads. A body without an `items` array is treated as a
   * hard error, never as an empty (successful) plan.
   */
  const fetchDedupePreview = async (): Promise<DedupePlan | null> => {
    dedupePreviewBusy.value = true
    error.value = null
    dedupMessage.value = null
    try {
      const res = await apiClient.GET('/api/series/{id}/dedupe-files', { params: { path: { id } } })
      if (res.error || !res.data || !Array.isArray(res.data.items)) {
        throw new Error(res.error ? res.error.message : 'Could not load the duplicate-file preview')
      }
      return {
        total: res.data.total ?? res.data.items.length,
        items: res.data.items.map((it) => ({
          reason: it.reason,
          number: it.number ?? null,
          filename: it.filename,
        })),
      }
    }
    catch (err) {
      error.value = err instanceof Error ? err.message : 'Could not load the duplicate-file preview'
      return null
    }
    finally {
      dedupePreviewBusy.value = false
    }
  }

  /**
   * Sweeps this series' duplicates via POST /api/series/{id}/dedupe-files: it
   * removes orphan/duplicate CBZ files (any .cbz that is not a chapter's current
   * winning filename) AND merges engine-switch duplicate chapter rows (the
   * Suwayomi "-1" vs Rensaio "name:epilogue" twin, matched by source URL). The
   * merge DELETES chapter rows, so on success it refreshes the series to drop the
   * removed rows from the view, then reports how many duplicates were resolved in
   * dedupMessage. Errors set `error`.
   *
   * Resolves true on success / false on failure so the confirm dialog closes ONLY
   * on success and stays open with the error shown inside it on failure (§16).
   */
  const dedupeFiles = async (): Promise<boolean> => {
    dedupeFilesBusy.value = true
    error.value = null
    dedupMessage.value = null
    try {
      const res = await apiClient.POST('/api/series/{id}/dedupe-files', { params: { path: { id } } })
      if (res.error || !res.data) throw new Error(res.error ? res.error.message : 'Dedupe files failed')
      const { removed } = res.data
      // The merge pass may have deleted chapter rows — refresh so the detail view
      // reflects the removals before showing the result message.
      await refresh()
      dedupMessage.value = `Removed ${removed} duplicate${removed === 1 ? '' : 's'}`
      return true
    }
    catch (err) {
      error.value = err instanceof Error ? err.message : 'Dedupe files failed'
      return false
    }
    finally {
      dedupeFilesBusy.value = false
    }
  }

  /**
   * Loads the series' removable FRACTIONAL chapters (GET /api/series/{id}/
   * fractional-cleanup) — the already-downloaded files the "Ignore fractional
   * chapters" switch leaves behind (it stops NEW fractional downloads and
   * deletes nothing). A plain read: it drives BOTH whether the Sources panel
   * offers the "Remove fractional files" button at all (empty set → no button,
   * no dead control) and the cleanup dialog's contents.
   *
   * Resolves null on failure — the button simply stays hidden. This is a
   * BACKGROUND read the owner never asked for, so a failure must not shout at
   * him with a page-level error banner; the removal POST itself is the
   * owner-driven action, and THAT surfaces its errors (§16).
   */
  const fetchFractionalCleanup = async (): Promise<FractionalCleanupPreview | null> => {
    const res = await apiClient.GET('/api/series/{id}/fractional-cleanup', { params: { path: { id } } })
    // Guard the response SHAPE, not just the status: a body without a `chapters`
    // array (a partial/garbled payload) reads as "nothing to clean" — it must
    // never throw from a background read the owner never triggered.
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
   * Removes the fractional chapters the owner TICKED in the cleanup dialog
   * (POST /api/series/{id}/fractional-cleanup): each one's CBZ file and its
   * Chapter row go; the source's ProviderChapter feed is KEPT, so un-ticking
   * "Ignore fractional chapters" later restores them.
   *
   * `chapterIds` is a SELECTION, never an authorisation — the backend
   * re-computes the removable set and rejects (400, nothing deleted) any id
   * outside it. Runs through the shared `mutate` wrapper, so it refreshes the
   * series on success (the chapter list loses the removed rows) and resolves
   * true/false: the dialog closes ONLY on true, a failure keeps it open with
   * the error shown inside it (§16).
   */
  const removeFractionalChapters = (chapterIds: string[]): Promise<boolean> =>
    mutate(
      () => apiClient.POST('/api/series/{id}/fractional-cleanup', {
        params: { path: { id } },
        body: { chapterIds },
      }),
      fractionalBusy,
    )

  /**
   * Sets the series' reading progress to `chapter` (QCAT-242, `POST
   * /api/series/{id}/reading-progress`) — the "re-read from start" (chapter
   * 0) or "jump to chapter N" action. The backend resets local chapters
   * (<= chapter read, > chapter unread) AND force-sets every bound tracker to
   * the same target, then returns the refreshed `SeriesDetail`.
   *
   * On success `series` is reseeded DIRECTLY from the response (§16
   * mutate-reseeds-from-response) — the chapter list reflects the new
   * read/unread split with no extra GET. It does NOT refresh tracker
   * bindings: those live in a SEPARATE composable (`useSeriesTracking`), so
   * the page additionally calls that composable's `loadBindings({ silent:
   * true })` after a successful call, the same way it reconciles bindings
   * after any other tracker-affecting mutation.
   *
   * Resolves true/false; a failure sets `progressError` to the backend's own
   * message (never swallowed, never generic) so the calling dialog can show
   * exactly why the reset was rejected. Uses its OWN busy/error refs (not the
   * shared `mutate` wrapper) so it never fights the Trackers section's or the
   * chapter row's own in-flight state with a shared flag.
   */
  const setReadingProgress = async (chapter: number): Promise<boolean> => {
    settingProgress.value = true
    progressError.value = null
    try {
      const res = await apiClient.POST('/api/series/{id}/reading-progress', {
        params: { path: { id } },
        body: { chapter },
      })
      if (res.error || !res.data) throw new Error(res.error ? res.error.message : 'Failed to set reading progress')
      series.value = mapDetail(res.data)
      return true
    }
    catch (err) {
      progressError.value = err instanceof Error ? err.message : 'Failed to set reading progress'
      return false
    }
    finally {
      settingProgress.value = false
    }
  }

  const dismissError = (): void => { error.value = null }

  /**
   * Reseeds `series` directly from an authoritative SeriesDetail returned by
   * a mutation the PARENT drove through its own composable — e.g.
   * `useMatchSource.batchAddProviders` (Slice P's "Add a source" dialog).
   * Mirrors `matchDiskProvider`'s own direct reseed: the endpoint already
   * returns the fresh, fully-refreshed SeriesDetail, so a second
   * `GET /api/series/{id}` round-trip would be wasted (§16
   * mutate-reseeds-from-response).
   */
  const reseed = (dto: SeriesDetailDTO): void => {
    series.value = mapDetail(dto)
  }

  void refresh()

  return {
    series,
    categoryOptions,
    pending,
    error,
    saving,
    deleteBusy,
    removeBusy,
    matchBusy,
    dedupBusy,
    dedupePreviewBusy,
    dedupeFilesBusy,
    fractionalBusy,
    dedupMessage,
    settingProgress,
    progressError,
    setMonitored,
    setCompleted,
    setCategory,
    reorderProviders,
    removeSource,
    setIgnoreFractional,
    chooseMetadataSource,
    deleteSeries,
    matchDiskProvider,
    consolidateProviders,
    dedupProviders,
    fetchDedupePreview,
    dedupeFiles,
    fetchFractionalCleanup,
    removeFractionalChapters,
    setReadingProgress,
    dismissError,
    refresh,
    reseed,
  }
}
