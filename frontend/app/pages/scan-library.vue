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
 * Emit wiring:
 *   @start-scan          → startScan()
 *   @set-status-filter   → setStatusFilter(status)
 *   @load-more           → loadMore()
 *   @import-disk-only    → importDiskOnly(path)
 *   @match               → onMatch(path) — opens the panel + runs match(path)
 *   @skip                → skip(path)
 *   @import-all-disk-only → importAllDiskOnly()
 *   @match-confirm        → onMatchConfirm({path, match}) — importWithMatch
 *   @match-back           → onMatchBack() — closes the panel, no mutation
 */
import { computed, ref } from 'vue'
import type { ScanMatch } from '~/composables/useScanLibrary'

const {
  scanState,
  startScan,
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
  importWithMatch,
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
 * Opens the Match sub-panel for one staged entry and kicks off the
 * cross-source search. `matching`/`matchError`/`matchGroups` (all from the
 * composable, and all covered by its stale-response guard) drive the
 * panel's own loading/error/results state while `match()` resolves.
 */
async function onMatch(path: string): Promise<void> {
  const entry = entries.value.find((e) => e.path === path)
  matchTarget.value = { path, title: entry?.title ?? path }
  await match(path)
}

/**
 * Confirms the owner's picked source: runs `importWithMatch`, then closes
 * the panel only on success — a failed mutation's error surfaces ON the
 * panel (via the row's busy/error, reused from the table) so the owner can
 * retry without losing their candidate selection.
 */
async function onMatchConfirm({ path, match: selection }: { path: string, match: ScanMatch }): Promise<void> {
  await importWithMatch(path, selection)
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
      :matching="matching"
      :match-error="matchError"
      @start-scan="startScan"
      @set-status-filter="setStatusFilter"
      @load-more="loadMore"
      @import-disk-only="importDiskOnly"
      @match="onMatch"
      @skip="skip"
      @import-all-disk-only="importAllDiskOnly"
      @match-confirm="onMatchConfirm"
      @match-back="onMatchBack"
    />
  </div>
</template>

<style scoped>
.page-scan-library {
  min-height: 100%;
}
</style>
