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
 * Emit wiring:
 *   @start-scan          → startScan()
 *   @set-status-filter   → setStatusFilter(status)
 *   @load-more           → loadMore()
 *   @import-disk-only    → importDiskOnly(path)
 *   @match               → onMatch(path) — stub; the match dialog is Task 7
 *   @skip                → skip(path)
 *   @import-all-disk-only → importAllDiskOnly()
 */
import { computed } from 'vue'

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

/**
 * Match (stub): the cross-source match search dialog is a separate task
 * (Task 7 in the Phase-B plan). This screen only needs to emit the request
 * per row for now — wiring the dialog + the composable's `match()` call lands
 * with that task, so this intentionally does nothing yet rather than firing a
 * network call with no result to show for it.
 */
function onMatch(_path: string): void {
  // Intentional no-op until Task 7 adds the match dialog.
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
      @start-scan="startScan"
      @set-status-filter="setStatusFilter"
      @load-more="loadMore"
      @import-disk-only="importDiskOnly"
      @match="onMatch"
      @skip="skip"
      @import-all-disk-only="importAllDiskOnly"
    />
  </div>
</template>

<style scoped>
.page-scan-library {
  min-height: 100%;
}
</style>
