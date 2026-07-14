<script setup lang="ts">
import type { ProviderRef } from '~/composables/useSourceConfigure'
import type { ReaderChapter } from '~/composables/useReader'
import type { CoverCandidate, FractionalCleanupPreview, MetadataCandidate, UpdateTrackPatch } from '~/components/screens/seriesDetail.types'

/**
 * Series detail page — route /series/:id.
 *
 * Delegates all data fetching, mutation state, and §16 error handling to
 * useSeriesDetail(id). Two DISTINCT dialogs both search across sources:
 *   - "Add a source" (the inverse of remove-source) is backed by its OWN
 *     composable, useMatchSource(id) — searching sources is orthogonal to the
 *     series-detail state useSeriesDetail owns. Slice P rebuilt it onto the
 *     shared Adopt-wizard Configure powers (multi-select tray, per-scanlator
 *     coverage, importance ranking) — see MatchSourceDialog's own doc comment.
 *   - "Match to source" (the no-re-download Match for an UNLINKED disk-origin
 *     group) is backed by useMatchDiskProvider() the same way, but its actual
 *     link mutation is useSeriesDetail.matchDiskProvider — that action
 *     reseeds `series` DIRECTLY from the response (no extra refetch, §16).
 * SeriesDetail, MatchSourceDialog, and MatchDiskProviderDialog are
 * auto-imported from app/components/. navigateTo is a Nuxt auto-import.
 *
 * Confirm dialogs whose lifetime depends on a mutation OUTCOME live HERE, not in
 * the screen: an emit is fire-and-forget, so the screen can never learn whether
 * the mutation succeeded. The page awaits the composable's true/false result and
 * closes the dialog ONLY on success — a failure keeps it open with the error
 * visible inside it (§16, never a silent failure). That covers `RemoveSourceDialog`
 * (the screen only emits `requestRemoveSource` when the row's Remove is pressed),
 * `MatchSourceDialog`, and `MatchDiskProviderDialog`.
 *
 * Prop wiring:
 *   :series            — the mapped SeriesDetail (null while loading)
 *   :category-options  — string[] of category names for the recategorize select
 *   :saving            — true while an inline mutation is in flight
 *   :delete-busy       — true while the delete request is in flight
 *   :error             — latest mutation error message (null when none)
 *   :dedup-busy        — true while a dedup-providers request is in flight
 *   :dedupe-files-busy — true while a dedupe-files request is in flight
 *   :dedup-message     — transient dedup/dedupe-files result message
 *   :resume-label      — "Start"/"Continue" for the floating resume button, or
 *                         null to hide it (no downloaded chapters to resume)
 *
 * Emit wiring (every emit the screen declares, per the SFC defineEmits contract):
 *   @change-category        → setCategory(name)
 *   @toggle-monitored       → setMonitored(bool)
 *   @toggle-completed       → setCompleted(bool)
 *   @reorder-providers      → reorderProviders(list)
 *   @request-remove-source  → opens RemoveSourceDialog for that provider
 *   @match-provider         → opens MatchDiskProviderDialog for that provider
 *   @choose-metadata-source → chooseMetadataSource(providerId)   (M10 per-source display pin — distinct from Identify below)
 *   @request-identify       → opens MetadataIdentifyModal (identifyOpen = true)
 *   @request-cover-picker   → opens CoverPickerModal (coverPickerOpen = true)
 *   @delete-series          → deleteSeries(deleteFiles)   (navigates to / on success)
 *   @add-source             → opens MatchSourceDialog (matchOpen = true)
 *   @dismiss-error          → dismissError()
 *   @dedup-providers        → dedupProviders()   (merges drifted disk/live twins)
 *   @dedupe-files           → dedupeFiles()      (sweeps orphan/duplicate CBZs)
 *   @request-fractional-cleanup → opens FractionalCleanupDialog (see below)
 *   @resume                 → onResume() (resolves the resume target via
 *                             useReadingProgress.resumeTarget and navigates to
 *                             the reader — see the "Resume FAB" section below)
 *
 * Native metadata engine (useMetadata(id), Slice D): two more page-owned
 * dialogs, same reasoning as the ones above — only the page learns whether the
 * mutation succeeded, so both close ONLY on a truthy result and reseed `series`
 * directly from the returned DTO (§16 mutate-reseeds-from-response, same shape
 * as `matchDiskProvider`/`batchAddProviders`):
 *   - MetadataIdentifyModal: `search` → `metadata.search(query)`;
 *     `confirm` (one or more MULTI-SELECT picks, in pick order) →
 *     `metadata.identify(candidates.map(c => ({provider: c.providerKey, remoteId: c.remoteId})))`,
 *     closes + reseeds on success, stays open with the error shown on failure.
 *   - CoverPickerModal: opening it (`coverPickerOpen` watcher) →
 *     `metadata.loadCovers()`; `confirm` → `metadata.setCover(candidate.sourceKind,
 *     candidate.sourceRef, candidate.coverUrl)`, closes + reseeds on success.
 *   `currentCoverId` marks the series' existing cover pick (from
 *   `series.coverSource`) so the gallery preselects + labels it "Current".
 *
 * Add-source wiring (Slice P): MatchSourceDialog's `search`/`loadBreakdowns`/
 * `confirm` emits drive useMatchSource's `search`/`loadBreakdowns`/
 * `batchAddProviders`. On a successful `batchAddProviders` the dialog closes
 * and the response's authoritative SeriesDetail is applied directly via
 * `useSeriesDetail.reseed` — no extra `GET /api/series/{id}` round-trip
 * (§16 mutate-reseeds-from-response, same shape as `matchDiskProvider`).
 *
 * Match-to-source wiring: `matchTargetId` (set by @match-provider) picks the
 * unlinked provider out of `series.providers` to prefill the dialog's
 * providerLabel/chapterCount/defaultImportance copy. MatchDiskProviderDialog's
 * `search`/`pickCandidate`/`confirm` emits drive useMatchDiskProvider's
 * `search`/`loadBreakdown` and useSeriesDetail's `matchDiskProvider`. A
 * successful match closes the dialog — `series` is already reseeded by
 * `matchDiskProvider` itself, no separate refresh() needed.
 *
 * Sources-panel coverage needs NO wiring here: each source's offering
 * (`Provider.feedCount` / `feedRanges`) rides the series-detail response, so the
 * panel shows it with no click and — deliberately — no live call to the source
 * (anti-IP-block politeness: we already store the feed).
 *
 * §16: pending true during the initial fetch; ErrorBanner shown on hard fetch
 * failure. Mutation errors are surfaced via the :error prop (dismissible banner
 * inside SeriesDetail).
 *
 * Trackers (`TrackingDialog`): `useSeriesTracking(id)` owns this series'
 * bindings + search/bind/unbind/refresh/updateTrack/syncNow; `useTrackers()`
 * supplies the connected-tracker list the "Add tracker" picker filters
 * against (same composable the Settings → Trackers pane uses — this is an
 * independent instance, so it re-fetches its own fresh connect status on open
 * rather than assuming the Settings page happens to be live in another tab).
 * Opened by `@request-tracking` (bubbled from `RichSeriesCard`'s additive
 * `openTrackers` button via `SeriesDetail`); `search`/`bind`/`unbind`/
 * `refresh`/`update`/`sync` drive the matching `useSeriesTracking` method
 * directly (no dialog-close-on-success gating like the other page-owned
 * dialogs — each mutation applies its own result into `bindings` and the
 * dialog just keeps reflecting it, §16 mutate-reseeds-from-response; the
 * per-row edit form's own open/close is TrackingDialog's own local state, not
 * the page's).
 */
const route = useRoute()
const id = route.params.id as string

const {
  series,
  categoryOptions,
  pending,
  error,
  saving,
  deleteBusy,
  removeBusy,
  matchBusy,
  setMonitored,
  setCompleted,
  setCategory,
  reorderProviders,
  removeSource,
  setIgnoreFractional,
  chooseMetadataSource,
  deleteSeries,
  matchDiskProvider,
  dismissError,
  reseed,
  dedupBusy,
  dedupeFilesBusy,
  fractionalBusy,
  dedupMessage,
  dedupProviders,
  dedupeFiles,
  fetchFractionalCleanup,
  removeFractionalChapters,
} = useSeriesDetail(id)

const {
  candidates: metadataCandidates,
  searching: metadataSearching,
  searchError: metadataSearchError,
  identifying: metadataIdentifying,
  identifyError,
  coverCandidates,
  coversLoading,
  coversError,
  settingCover,
  setCoverError,
  search: searchMetadata,
  identify: identifySeries,
  loadCovers,
  setCover,
} = useMetadata(id)

const {
  sources: matchSources,
  groups: matchGroups,
  breakdowns: matchBreakdowns,
  searching: matchSearching,
  saving: matchSaving,
  error: matchError,
  loadSources: matchLoadSources,
  search: matchSearch,
  loadBreakdowns: matchLoadBreakdowns,
  batchAddProviders,
} = useMatchSource(id)

const matchOpen = ref(false)

// Lazily load the source-filter list the first time the "Add a source" dialog
// opens (useMatchSource.loadSources is guarded to fetch at most once).
watch(matchOpen, (isOpen) => {
  if (isOpen) void matchLoadSources()
})

async function onMatchConfirm(providers: ProviderRef[]): Promise<void> {
  const detail = await batchAddProviders(providers)
  if (detail) {
    matchOpen.value = false
    reseed(detail)
  }
}

// ---- Remove source (confirm dialog) ----------------------------------------
// The page owns this dialog because only the page learns whether the removal
// actually succeeded: `removeSource` resolves true/false, and the dialog closes
// ONLY on true. On false it stays open with the error shown inside it (§16) —
// the screen could never do this itself (an emit is fire-and-forget).
const removeOpen = ref(false)
const removeTargetId = ref<string | null>(null)
const removeTarget = computed(() => series.value?.providers.find((p) => p.id === removeTargetId.value) ?? null)

function openRemove(providerId: string): void {
  removeTargetId.value = providerId
  removeOpen.value = true
}

async function onConfirmRemove(): Promise<void> {
  if (!removeTargetId.value) return
  const ok = await removeSource(removeTargetId.value)
  if (ok) removeOpen.value = false
}

// ---- Fractional cleanup (the owner-triggered half of "ignore fractional") ----
// The toggle stops NEW fractional downloads and deletes nothing; the files
// already on disk need this explicit, per-chapter, confirmed removal.
//
// The preview is loaded up front because it decides whether the Sources panel
// offers the button AT ALL (empty set → no button — no dead control), and it
// fills the dialog. It is re-loaded after a successful removal so the button
// disappears once the last removable file is gone. Like the other confirm
// dialogs, this one lives on the PAGE: only the page learns whether the removal
// succeeded, and it closes the dialog ONLY on success (§16).
const fractionalPreview = ref<FractionalCleanupPreview | null>(null)
const fractionalOpen = ref(false)
const fractionalCount = computed(() => fractionalPreview.value?.chapters.length ?? 0)

async function loadFractionalPreview(): Promise<void> {
  fractionalPreview.value = await fetchFractionalCleanup()
}
void loadFractionalPreview()

async function onConfirmFractionalCleanup(chapterIds: string[]): Promise<void> {
  const ok = await removeFractionalChapters(chapterIds)
  if (!ok) return
  fractionalOpen.value = false
  await loadFractionalPreview()
}

// ---- Match to source (no-re-download link of an unlinked disk-origin group) ----
const {
  sources: linkSources,
  groups: linkGroups,
  searching: linkSearching,
  breakdown: linkBreakdown,
  breakdownLoading: linkBreakdownLoading,
  error: linkSearchError,
  loadSources: linkLoadSources,
  search: linkSearch,
  loadBreakdown: linkLoadBreakdown,
} = useMatchDiskProvider()

const matchProviderOpen = ref(false)
const matchTargetId = ref<string | null>(null)

// Lazily load the source-filter list the first time the "Match to source"
// dialog opens (useMatchDiskProvider.loadSources is guarded to fetch at most once).
watch(matchProviderOpen, (isOpen) => {
  if (isOpen) void linkLoadSources()
})

const matchTarget = computed(() => series.value?.providers.find((p) => p.id === matchTargetId.value) ?? null)
// Either the dialog's own search/breakdown error or the matchDiskProvider mutation error — only one is ever set at a time.
const matchProviderError = computed(() => linkSearchError.value ?? error.value)

function openMatchProvider(providerId: string): void {
  matchTargetId.value = providerId
  matchProviderOpen.value = true
}

function onPickCandidate(payload: { source: string, mangaId: number }): void {
  void linkLoadBreakdown(payload.source, payload.mangaId)
}

async function onMatchProviderConfirm(payload: { source: string, mangaId: number, scanlator: string, importance: number }): Promise<void> {
  if (!matchTargetId.value) return
  const ok = await matchDiskProvider(matchTargetId.value, payload)
  if (ok) matchProviderOpen.value = false
}

// ---- Identify (native metadata engine "Identify" match) --------------------
// Only the page learns whether identify() succeeded, so it owns the dialog
// (same reasoning as RemoveSourceDialog/MatchSourceDialog above) — it closes
// ONLY on success and reseeds `series` directly from the response (§16).
const identifyOpen = ref(false)
// Either the search failure or the confirm (identify) failure — only one is ever set at a time.
const identifyModalError = computed(() => metadataSearchError.value ?? identifyError.value)

async function onIdentifySearch(query: string): Promise<void> {
  await searchMetadata(query)
}

async function onIdentifyConfirm(candidates: MetadataCandidate[]): Promise<void> {
  const detail = await identifySeries(candidates.map((c) => ({ provider: c.providerKey, remoteId: c.remoteId })))
  if (detail) {
    identifyOpen.value = false
    reseed(detail)
  }
}

// ---- Choose cover (native metadata engine cover picker) --------------------
// The gallery loads on OPEN (no owner-visible "load" trigger inside the modal
// itself), and the series' current cover pick (series.coverSource) preselects
// + marks the "Current" tile. Same page-owned-dialog reasoning as Identify.
const coverPickerOpen = ref(false)
// Either the gallery-load failure or the confirm (setCover) failure.
const coverPickerError = computed(() => coversError.value ?? setCoverError.value)

watch(coverPickerOpen, (isOpen) => {
  if (isOpen) void loadCovers()
})

// Mirrors useMetadata's mapCoverCandidate id scheme exactly
// (`${sourceKind}:${sourceRef}:${coverUrl}`) so the gallery tile the series
// is CURRENTLY using is the one — and only one — marked "Current"/preselected.
// `coverSource.remoteUrl` is what SetCover recorded coverUrl AS (both the
// metadata- and source-kind branches persist it verbatim — see
// metadatasvc.finalizeCover), so it reconstructs the exact same id a fresh
// `loadCovers()` gallery hit would carry for that pick, with no extra field
// needed on the DTO.
const currentCoverId = computed(() => {
  const src = series.value?.coverSource
  return src ? `${src.kind}:${src.ref}:${src.remoteUrl}` : undefined
})

async function onCoverConfirm(candidate: CoverCandidate): Promise<void> {
  const detail = await setCover(candidate.sourceKind, candidate.sourceRef, candidate.coverUrl)
  if (detail) {
    coverPickerOpen.value = false
    reseed(detail)
  }
}

// Open a downloaded chapter in the long-strip reader (a ChapterRow "Read" click).
function openReader(chapterId: string): void {
  void navigateTo(`/series/${id}/read/${chapterId}`)
}

// ---- Trackers (bind/unbind, Phase 3d) --------------------------------------
const {
  bindings: trackBindings,
  pending: trackBindingsPending,
  error: trackBindingsError,
  searchResults: trackSearchResults,
  searching: trackSearching,
  searchError: trackSearchError,
  binding: trackBinding,
  bindError: trackBindError,
  unbindBusyId: trackUnbindBusyId,
  refreshBusyId: trackRefreshBusyId,
  updateBusyId: trackUpdateBusyId,
  updateError: trackUpdateError,
  syncing: trackSyncing,
  syncError: trackSyncError,
  loadBindings: loadTrackBindings,
  search: searchTracker,
  bind: bindTracker,
  unbind: unbindTracker,
  refresh: refreshTracker,
  updateTrack,
  syncNow,
} = useSeriesTracking(id)

const { trackers: connectedTrackers, list: listTrackers } = useTrackers()

const trackingOpen = ref(false)

// Re-fetch both the connect status and this series' bindings fresh every time
// the panel opens — the owner may have connected a NEW tracker in Settings (a
// different tab/session) since this page loaded.
watch(trackingOpen, (isOpen) => {
  if (!isOpen) return
  void listTrackers()
  void loadTrackBindings()
})

function onSearchTracker(payload: { trackerId: number, q: string }): void {
  void searchTracker(payload.trackerId, payload.q)
}

function onBindTracker(payload: { trackerId: number, remoteId: string }): void {
  void bindTracker(payload.trackerId, payload.remoteId)
}

function onUnbindTracker(recordId: string): void {
  // Local-only unbind from this dialog (see TrackingDialog's own doc comment);
  // "also remove from the tracker's account" is a Phase-4 nicety.
  void unbindTracker(recordId, false)
}

function onRefreshTracker(recordId: string): void {
  void refreshTracker(recordId)
}

function onUpdateTracker(payload: { recordId: string, patch: UpdateTrackPatch }): void {
  void updateTrack(payload.recordId, payload.patch)
}

function onSyncTracker(): void {
  void syncNow()
}

// ---- Resume FAB (Komikku-style "continue reading" button) -----------------
// Reuses useReadingProgress.resumeTarget — the SAME resume-point logic the
// reader route itself runs on open — instead of reimplementing it. This page
// has no "explicitly opened" chapter, so startChapterId is '' (never matches
// a real chapter id), which makes resumeTarget always fall past its "started"
// branch to: the FIRST not-read chapter (number-ascending) at its saved
// lastReadPage, or — once every chapter is read — the LAST chapter at page 0
// (start over; see resumeTarget's own doc comment) — exactly what a resume
// button needs. The chapters ref it closes over is unused by resumeTarget
// itself (only record/markRead read it), so an empty ref is fine.
const { resumeTarget } = useReadingProgress(ref<ReaderChapter[]>([]), '')

// Downloaded chapters only, ascending by number (mirrors the reader's own
// ordering) — the FAB's candidate list. Chapter.pageCount is nullable on the
// screen type; ReaderChapter wants a real number, so an unset count reads 0
// (matches useReader's own mapReaderChapter fallback).
const downloadedChapters = computed<ReaderChapter[]>(() =>
  (series.value?.chapters ?? [])
    .filter((c) => c.state === 'downloaded')
    .map((c) => ({ id: c.id, number: c.number, name: c.name, pageCount: c.pageCount ?? 0, read: c.read, lastReadPage: c.lastReadPage }))
    .sort((a, b) => (a.number ?? Number.POSITIVE_INFINITY) - (b.number ?? Number.POSITIVE_INFINITY)),
)

// Nothing downloaded → no FAB (nothing to resume). Otherwise "Continue" once
// any downloaded chapter shows progress, else "Start" (never opened).
const resumeLabel = computed<string | null>(() => {
  if (downloadedChapters.value.length === 0) return null
  const hasProgress = downloadedChapters.value.some((c) => c.read || c.lastReadPage > 0)
  return hasProgress ? 'Continue' : 'Start'
})

/** The FAB was clicked — resolve the resume target and open the reader
 *  there, carrying the DECIDED page via `?page=`. The chapter id alone is
 *  NOT enough: the reader route re-resolves via `resumeTarget` too, but
 *  matches `startChapterId` against the loaded list first — its "started"
 *  branch — which always wins over the "all chapters read" fallback this
 *  function's own `target.page` may have come from. When every chapter is
 *  read, `resumeTarget` deliberately opens the LAST chapter at page 0 (start
 *  it over, don't reopen something finished); re-deriving via the "started"
 *  branch instead lands on that chapter's saved `lastReadPage`, its FINAL
 *  page — so the page must be carried explicitly, not recomputed. */
function onResume(): void {
  const target = resumeTarget(downloadedChapters.value)
  if (!target.chapterId) return
  void navigateTo(`/series/${id}/read/${target.chapterId}?page=${target.page}`)
}
</script>

<template>
  <div class="page-series-detail">
    <div v-if="pending && !series" class="page-series-detail__loading">
      Loading…
    </div>
    <ErrorBanner v-else-if="error && !series" :message="error" />
    <SeriesDetail
      v-else-if="series"
      :series="series"
      :category-options="categoryOptions"
      :saving="saving"
      :delete-busy="deleteBusy"
      :error="error"
      :dedup-busy="dedupBusy"
      :dedupe-files-busy="dedupeFilesBusy"
      :fractional-cleanup-count="fractionalCount"
      :dedup-message="dedupMessage"
      :resume-label="resumeLabel"
      @change-category="setCategory"
      @toggle-monitored="setMonitored"
      @toggle-completed="setCompleted"
      @reorder-providers="reorderProviders"
      @request-remove-source="openRemove"
      @match-provider="openMatchProvider"
      @toggle-ignore-fractional="setIgnoreFractional"
      @choose-metadata-source="chooseMetadataSource"
      @request-identify="identifyOpen = true"
      @request-cover-picker="coverPickerOpen = true"
      @request-tracking="trackingOpen = true"
      @delete-series="deleteSeries"
      @add-source="matchOpen = true"
      @dismiss-error="dismissError"
      @dedup-providers="dedupProviders"
      @dedupe-files="dedupeFiles"
      @request-fractional-cleanup="fractionalOpen = true"
      @read="openReader"
      @resume="onResume"
    />

    <FractionalCleanupDialog
      v-if="series"
      v-model:open="fractionalOpen"
      :chapters="fractionalPreview?.chapters ?? []"
      :typical-page-count="fractionalPreview?.typicalPageCount ?? 0"
      :busy="fractionalBusy"
      :error="error"
      @confirm="onConfirmFractionalCleanup"
    />

    <RemoveSourceDialog
      v-if="series"
      v-model:open="removeOpen"
      :busy="removeBusy"
      :source-name="removeTarget?.provider ?? ''"
      :error="error"
      @confirm="onConfirmRemove"
    />

    <MatchSourceDialog
      v-if="series"
      v-model:open="matchOpen"
      :series-title="series.title"
      :sources="matchSources"
      :groups="matchGroups"
      :breakdowns="matchBreakdowns"
      :searching="matchSearching"
      :saving="matchSaving"
      :error="matchError"
      @search="matchSearch"
      @load-breakdowns="matchLoadBreakdowns"
      @confirm="onMatchConfirm"
    />

    <MatchDiskProviderDialog
      v-if="series"
      v-model:open="matchProviderOpen"
      :series-title="series.title"
      :sources="linkSources"
      :provider-label="matchTarget?.providerName ?? ''"
      :chapter-count="matchTarget?.chapterCount ?? 0"
      :default-importance="matchTarget?.importance ?? 2"
      :groups="linkGroups"
      :searching="linkSearching"
      :breakdown="linkBreakdown"
      :breakdown-loading="linkBreakdownLoading"
      :saving="matchBusy"
      :error="matchProviderError"
      @search="linkSearch"
      @pick-candidate="onPickCandidate"
      @confirm="onMatchProviderConfirm"
    />

    <MetadataIdentifyModal
      v-if="series"
      v-model:open="identifyOpen"
      :title="series.title"
      :candidates="metadataCandidates"
      :loading="metadataSearching || metadataIdentifying"
      :error="identifyModalError"
      @search="onIdentifySearch"
      @confirm="onIdentifyConfirm"
    />

    <CoverPickerModal
      v-if="series"
      v-model:open="coverPickerOpen"
      :candidates="coverCandidates"
      :current-id="currentCoverId"
      :loading="coversLoading || settingCover"
      :error="coverPickerError"
      @confirm="onCoverConfirm"
    />

    <TrackingDialog
      v-if="series"
      v-model:open="trackingOpen"
      :bindings="trackBindings"
      :trackers="connectedTrackers"
      :pending="trackBindingsPending"
      :error="trackBindingsError"
      :search-results="trackSearchResults"
      :searching="trackSearching"
      :search-error="trackSearchError"
      :binding="trackBinding"
      :bind-error="trackBindError"
      :unbind-busy-id="trackUnbindBusyId"
      :refresh-busy-id="trackRefreshBusyId"
      :update-busy-id="trackUpdateBusyId"
      :update-error="trackUpdateError"
      :syncing="trackSyncing"
      :sync-error="trackSyncError"
      @search="onSearchTracker"
      @bind="onBindTracker"
      @unbind="onUnbindTracker"
      @refresh="onRefreshTracker"
      @update="onUpdateTracker"
      @sync="onSyncTracker"
    />
  </div>
</template>

<style scoped>
.page-series-detail {
  min-height: 100%;
}

.page-series-detail__loading {
  padding: 40px;
  color: var(--text-muted);
  text-align: center;
}
</style>
