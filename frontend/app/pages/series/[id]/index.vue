<script setup lang="ts">
import type { ProviderRef } from '~/composables/useSourceConfigure'

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
 *
 * Emit wiring (every emit the screen declares, per the SFC defineEmits contract):
 *   @change-category        → setCategory(name)
 *   @toggle-monitored       → setMonitored(bool)
 *   @toggle-completed       → setCompleted(bool)
 *   @reorder-providers      → reorderProviders(list)
 *   @request-remove-source  → opens RemoveSourceDialog for that provider
 *   @match-provider         → opens MatchDiskProviderDialog for that provider
 *   @choose-metadata-source → chooseMetadataSource(providerId)
 *   @delete-series          → deleteSeries(deleteFiles)   (navigates to / on success)
 *   @add-source             → opens MatchSourceDialog (matchOpen = true)
 *   @dismiss-error          → dismissError()
 *   @dedup-providers        → dedupProviders()   (merges drifted disk/live twins)
 *   @dedupe-files           → dedupeFiles()      (sweeps orphan/duplicate CBZs)
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
  chooseMetadataSource,
  deleteSeries,
  matchDiskProvider,
  dismissError,
  reseed,
  dedupBusy,
  dedupeFilesBusy,
  dedupMessage,
  dedupProviders,
  dedupeFiles,
} = useSeriesDetail(id)

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

// Open a downloaded chapter in the long-strip reader (a ChapterRow "Read" click).
function openReader(chapterId: string): void {
  void navigateTo(`/series/${id}/read/${chapterId}`)
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
      :dedup-message="dedupMessage"
      @change-category="setCategory"
      @toggle-monitored="setMonitored"
      @toggle-completed="setCompleted"
      @reorder-providers="reorderProviders"
      @request-remove-source="openRemove"
      @match-provider="openMatchProvider"
      @choose-metadata-source="chooseMetadataSource"
      @delete-series="deleteSeries"
      @add-source="matchOpen = true"
      @dismiss-error="dismissError"
      @dedup-providers="dedupProviders"
      @dedupe-files="dedupeFiles"
      @read="openReader"
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
