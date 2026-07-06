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
 * Prop wiring:
 *   :series            — the mapped SeriesDetail (null while loading)
 *   :category-options  — string[] of category names for the recategorize select
 *   :saving            — true while an inline mutation is in flight
 *   :delete-busy       — true while the delete request is in flight
 *   :remove-busy       — true while a remove-source request is in flight
 *   :error             — latest mutation error message (null when none)
 *
 * Emit wiring (every emit the screen declares, per the SFC defineEmits contract):
 *   @change-category        → setCategory(name)
 *   @toggle-monitored       → setMonitored(bool)
 *   @toggle-completed       → setCompleted(bool)
 *   @reorder-providers      → reorderProviders(list)
 *   @remove-source          → removeSource(providerId)
 *   @match-provider         → opens MatchDiskProviderDialog for that provider
 *   @choose-metadata-source → chooseMetadataSource(providerId)
 *   @delete-series          → deleteSeries(deleteFiles)   (navigates to / on success)
 *   @add-source             → opens MatchSourceDialog (matchOpen = true)
 *   @dismiss-error          → dismissError()
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
 * Coverage wiring (Sources panel, LAZY): `providerCoverage` is passed straight
 * through to `SeriesDetail`/`SourcesPanel`/`ProviderRow`. `@load-coverage`
 * (a row's "Show coverage" click, carrying the SeriesProvider id) resolves the
 * full `Provider` from `series.providers` and calls
 * `useSeriesDetail.loadProviderCoverage(provider)` — the ONLY call site for
 * that fetch anywhere in the app; it is never invoked from `onMounted` or any
 * part of the initial series load, so no per-source coverage traffic fires
 * until the owner explicitly asks for one row's coverage (anti-IP-block
 * politeness, decision D).
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
  providerCoverage,
  loadProviderCoverage,
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
} = useSeriesDetail(id)

const {
  groups: matchGroups,
  breakdowns: matchBreakdowns,
  searching: matchSearching,
  saving: matchSaving,
  error: matchError,
  search: matchSearch,
  loadBreakdowns: matchLoadBreakdowns,
  batchAddProviders,
} = useMatchSource(id)

const matchOpen = ref(false)

async function onMatchConfirm(providers: ProviderRef[]): Promise<void> {
  const detail = await batchAddProviders(providers)
  if (detail) {
    matchOpen.value = false
    reseed(detail)
  }
}

// ---- Match to source (no-re-download link of an unlinked disk-origin group) ----
const {
  groups: linkGroups,
  searching: linkSearching,
  breakdown: linkBreakdown,
  breakdownLoading: linkBreakdownLoading,
  error: linkSearchError,
  search: linkSearch,
  loadBreakdown: linkLoadBreakdown,
} = useMatchDiskProvider()

const matchProviderOpen = ref(false)
const matchTargetId = ref<string | null>(null)

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

// ---- Sources panel: lazy per-source coverage --------------------------------
// The SOLE call site for loadProviderCoverage — fired only by a row's own
// "Show coverage" click (never onMounted/the initial series load).
function onLoadCoverage(providerId: string): void {
  const provider = series.value?.providers.find((p) => p.id === providerId)
  if (provider) void loadProviderCoverage(provider)
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
      :remove-busy="removeBusy"
      :error="error"
      :provider-coverage="providerCoverage"
      @change-category="setCategory"
      @toggle-monitored="setMonitored"
      @toggle-completed="setCompleted"
      @reorder-providers="reorderProviders"
      @remove-source="removeSource"
      @match-provider="openMatchProvider"
      @choose-metadata-source="chooseMetadataSource"
      @delete-series="deleteSeries"
      @add-source="matchOpen = true"
      @load-coverage="onLoadCoverage"
      @dismiss-error="dismissError"
    />

    <MatchSourceDialog
      v-if="series"
      v-model:open="matchOpen"
      :series-title="series.title"
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
