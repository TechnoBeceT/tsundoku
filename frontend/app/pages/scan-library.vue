<script setup lang="ts">
/**
 * Scan Library page — route "/scan-library".
 *
 * Delegates all data fetching and mutation state to useScanLibrary()
 * (auto-imported). ScanLibrary is auto-imported from app/components/screens/.
 *
 * The composable tracks busy/error PER STAGED-ENTRY PATH via `busy(path)` /
 * `error(path)` accessor functions rather than a plain array/record (see
 * useScanLibrary.ts). ScanLibrary/StagingTable, like every other screen in
 * this app, are pure props+emits components (no function props — see
 * Downloads.vue's `retryingIds: string[]` convention), so this page derives
 * the two lookups it actually needs — which of the CURRENTLY LOADED entries
 * are busy, and their error messages — as plain computed values. Paths not in
 * the visible page are irrelevant to what's on screen, so this stays cheap
 * even while draining a 1000+-series library.
 *
 * The Match sub-panel (Task 7) needs one extra piece of page-owned state the
 * composable itself doesn't track: WHICH entry is currently being matched
 * (`matchTarget`, null when the panel is closed) — `matching`/`matchError`/
 * `matchGroups` (the search's own loading/error/results, INCLUDING the
 * stale-response guard — see useScanLibrary.ts) already come straight from
 * the composable, and the CONFIRM mutation's busy/error reuse the SAME
 * `busy(path)`/`error(path)` lookups every other row mutation uses.
 *
 * `onMatch` deliberately does NOT assign the composable's `match(path)`
 * return value anywhere — it just awaits the call and lets the template
 * read the composable's own `matchGroups` ref. Assigning the return value
 * into a page-local ref would reintroduce the exact race the composable's
 * generation-counter guard closes (an overlapping, earlier match() call
 * resolving after a later one and clobbering the panel with the wrong
 * series' candidates).
 *
 * The page also owns the "Limit matches to:" source-filter selection
 * (`sourceFilter`, v-model'd on the screen's chip row) plus the `sources` list
 * (fetched by the composable on mount). Toggling the filter re-runs ONLY the
 * currently-open match, never every entry's — see the `watch(sourceFilter)`.
 *
 * Emit wiring:
 *   @update:source-filter → sourceFilter = $event (page-level "Limit matches to:")
 *   @start-scan          → startScan()
 *   @set-status-filter   → setStatusFilter(status)
 *   @load-more           → loadMore()
 *   @import-disk-only    → importDiskOnly(path)
 *   @match               → onMatch(path) — opens the panel + runs match(path)
 *   @skip                → skip(path)
 *   @import-all-disk-only → importAllDiskOnly()
 *   @match-confirm        → onMatchConfirm({path, matches}) — importWithMatches (Slice P)
 *   @load-breakdowns      → loadBreakdowns(candidates) — Configure-stage per-scanlator coverage
 *   @match-back           → onMatchBack() — closes the panel, no mutation
 */
import { computed, ref, watch } from 'vue'
import type { ProviderRef } from '~/composables/useSourceConfigure'

const {
  scanState,
  startScan,
  sources,
  entries,
  statusFilter,
  setStatusFilter,
  pending,
  entriesError,
  hasMore,
  loadMore,
  busy,
  error,
  skip,
  importDiskOnly,
  importWithMatches,
  breakdowns,
  loadBreakdowns,
  matching,
  matchError,
  matchGroups,
  match,
  batchImporting,
  batchError,
  batchResult,
  importAllDiskOnly,
} = useScanLibrary()

const busyPaths = computed(() => entries.value.filter((e) => busy(e.path)).map((e) => e.path))

const rowErrors = computed(() => {
  const out: Record<string, string> = {}
  for (const e of entries.value) {
    const msg = error(e.path)
    if (msg) out[e.path] = msg
  }
  return out
})

/** The staged entry currently in the Match sub-panel, or null when it's closed. */
const matchTarget = ref<{ path: string, title: string } | null>(null)

/**
 * The page-level "Limit matches to:" source-filter selection (v-model'd on the
 * ScanLibrary screen's chip row). Chosen ONCE for the page; every entry's match
 * search respects it. Toggling a chip does NOT re-fire every entry's match —
 * only the currently-open match refreshes (see the watch below). `matchTarget`
 * doubles as "which match is open" (its `path`), so no separate ref is needed.
 */
const sourceFilter = ref<string[]>([])

/**
 * Opens the Match sub-panel for one staged entry and kicks off the
 * cross-source search, restricted to the current `sourceFilter`.
 * `matching`/`matchError`/`matchGroups` (all from the composable, and all
 * covered by its stale-response guard) drive the panel's own
 * loading/error/results state while `match()` resolves.
 */
async function onMatch(path: string): Promise<void> {
  const entry = entries.value.find((e) => e.path === path)
  matchTarget.value = { path, title: entry?.title ?? path }
  await match(path, [...sourceFilter.value])
}

// Re-run ONLY the currently-open match when the source filter changes, so a
// toggled chip refreshes the panel the owner is looking at (without re-firing
// every entry's match). No open panel → nothing to do.
watch(sourceFilter, () => {
  const open = matchTarget.value
  if (open) void match(open.path, [...sourceFilter.value])
})

/**
 * Confirms the owner's gathered, ranked sources: runs `importWithMatches`,
 * then closes the panel only on success — a failed mutation's error surfaces
 * ON the panel (via the row's busy/error, reused from the table) so the
 * owner can retry without losing their selection.
 */
async function onMatchConfirm({ path, matches }: { path: string, matches: ProviderRef[] }): Promise<void> {
  await importWithMatches(path, matches)
  if (!error(path)) matchTarget.value = null
}

/** Abandons the match flow — returns to the staging table, no mutation fires. */
function onMatchBack(): void {
  matchTarget.value = null
}
</script>

<template>
  <div class="page-scan-library">
    <ScanLibrary
      :scan-state="scanState"
      :sources="sources"
      :source-filter="sourceFilter"
      :entries="entries"
      :status-filter="statusFilter"
      :pending="pending"
      :entries-error="entriesError"
      :has-more="hasMore"
      :busy-paths="busyPaths"
      :row-errors="rowErrors"
      :batch-importing="batchImporting"
      :batch-error="batchError"
      :batch-result="batchResult"
      :match-path="matchTarget?.path ?? null"
      :match-title="matchTarget?.title ?? ''"
      :match-groups="matchGroups"
      :match-breakdowns="breakdowns"
      :matching="matching"
      :match-error="matchError"
      @update:source-filter="sourceFilter = $event"
      @start-scan="startScan"
      @set-status-filter="setStatusFilter"
      @load-more="loadMore"
      @import-disk-only="importDiskOnly"
      @match="onMatch"
      @skip="skip"
      @import-all-disk-only="importAllDiskOnly"
      @match-confirm="onMatchConfirm"
      @load-breakdowns="loadBreakdowns"
      @match-back="onMatchBack"
    />
  </div>
</template>

<style scoped>
.page-scan-library {
  min-height: 100%;
}
</style>
