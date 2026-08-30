<script setup lang="ts">
import SurfaceCard from '../ui/SurfaceCard.vue'
import LibraryDedupDialog from './LibraryDedupDialog.vue'
import RedownloadDialog from './RedownloadDialog.vue'
import type { SaveState, SourceOption, SourcesSettings } from '../screens/settings.types'
import type { RedownloadFilter, RedownloadPreview } from '~/composables/useRedownload'
import type { SourceThroughputPolicy } from '~/composables/useSourceThroughput'

/**
 * Owner-triggered library actions that deliberately remain outside the
 * Download engine and its Source exceptions editor. The legacy protection and
 * throughput props stay declared for compatibility while the Settings container
 * adopts the consolidated pane; this pane no longer renders or mutates them.
 *
 * Renders a "Library maintenance" card, which COMPOSES
 * LibraryDedupDialog: the trigger for the library-wide duplicate-source dedup
 * sweep (`POST /api/library/dedup-providers`, fire-and-forget 202 — see
 * useLibraryMaintenance), gated behind the shared destructive ConfirmModal
 * (QCAT-222) because the sweep renames CBZ files across the whole library.
 * `dedupAllBusy`/`dedupAllMessage`/`dedupAllError` are the §16 trio for that
 * action, owned by the parent (mirrors how SourceMetricsPane owns
 * `warming`/`warmMessage`/`warmError`), and `dedupAllSkippedBusy` carries the
 * count of series the finished sweep had to skip because a merge was already
 * running on them — its own actionable line, not part of the summary sentence.
 *
 * A second "Re-download from a source" card COMPOSES RedownloadDialog: the
 * owner-triggered, previewed bulk re-queue of already-downloaded chapters from
 * one source (`GET`/`POST /api/downloads/redownload`, see useRedownload), also
 * gated behind the shared ConfirmModal (QCAT-222) because it sweeps the whole
 * library. Its §16 state is owned by the parent, same as the dedup trio above.
 * Nothing is deleted by it — every existing CBZ stays on disk until its
 * replacement lands.
 *
 * Emits `dedupAll` to trigger the sweep, and `redownloadPreview` /
 * `redownload` / `redownloadReset` for the re-download.
 */
withDefaults(defineProps<{
  /** Transitional prop; global protection now belongs to DownloadEnginePane. */
  sources: SourcesSettings
  throughputPolicies?: SourceThroughputPolicy[]
  throughputSources?: SourceOption[]
  globalDownloadConcurrency?: number
  throughputLoading?: boolean
  throughputReady?: boolean
  throughputSavingSourceId?: string | null
  throughputLoadError?: string | null
  throughputError?: string | null
  /** §16 state of the Save button. */
  save?: SaveState
  /** True while the library-wide dedup sweep request is in flight. */
  dedupAllBusy?: boolean
  /** Started/success message from the last dedup sweep trigger. */
  dedupAllMessage?: string | null
  /** Error from the last dedup sweep trigger. */
  dedupAllError?: string | null
  /** Series the last dedup sweep skipped because a merge was already running. */
  dedupAllSkippedBusy?: number
  /** The last bulk-re-download preview, or null when none is loaded. */
  redownloadPreview?: RedownloadPreview | null
  /** True while the re-download preview request is in flight. */
  redownloadPreviewBusy?: boolean
  /** A failed-preview message for the re-download, or null. */
  redownloadPreviewError?: string | null
  /** True while the re-download apply request is in flight. */
  redownloadApplying?: boolean
  /** Success line from the last re-download apply, or null. */
  redownloadMessage?: string | null
  /** Failure line from the last re-download apply, or null. */
  redownloadError?: string | null
}>(), {
  save: () => ({ status: 'idle' }),
  throughputPolicies: () => [],
  throughputSources: () => [],
  globalDownloadConcurrency: 5,
  throughputLoading: false,
  throughputReady: true,
  throughputSavingSourceId: null,
  throughputLoadError: null,
  throughputError: null,
  dedupAllBusy: false,
  dedupAllMessage: null,
  dedupAllError: null,
  dedupAllSkippedBusy: 0,
  redownloadPreview: null,
  redownloadPreviewBusy: false,
  redownloadPreviewError: null,
  redownloadApplying: false,
  redownloadMessage: null,
  redownloadError: null,
})

const emit = defineEmits<{
  /** Transitional events retained until the screen wiring migration. */
  save: [settings: SourcesSettings]
  saveConcurrency: [sourceId: string, value: number]
  inheritConcurrency: [sourceId: string]
  saveImageDelay: [sourceId: string, value: string]
  inheritImageDelay: [sourceId: string]
  reloadThroughput: []
  /** Trigger the library-wide duplicate-source dedup sweep. */
  dedupAll: []
  /** Load the bulk-re-download preview for this filter (reads only). */
  redownloadPreview: [filter: RedownloadFilter]
  /** Apply the bulk re-download (reachable only via its ConfirmModal). */
  redownload: [filter: RedownloadFilter]
  /** The re-download filter changed — discard the loaded preview/outcome. */
  redownloadReset: []
}>()
</script>

<template>
  <div class="pane-stack">
    <SurfaceCard
      title="Library maintenance"
      sub="One-shot cleanup. Merges the same physical source represented twice on a series (a disk-import artifact) across your whole library — no re-downloading."
    >
      <LibraryDedupDialog
        :busy="dedupAllBusy"
        :message="dedupAllMessage"
        :error="dedupAllError"
        :skipped-busy="dedupAllSkippedBusy"
        @confirm="emit('dedupAll')"
      />
    </SurfaceCard>

    <SurfaceCard
      title="Re-download from a source"
      sub="Fetch already-downloaded chapters again from one source — for when its stored files turn out to be wrong. Nothing is deleted: each file stays on disk until its replacement lands."
    >
      <RedownloadDialog
        :preview="redownloadPreview"
        :preview-busy="redownloadPreviewBusy"
        :preview-error="redownloadPreviewError"
        :applying="redownloadApplying"
        :apply-message="redownloadMessage"
        :apply-error="redownloadError"
        @preview="emit('redownloadPreview', $event)"
        @confirm="emit('redownload', $event)"
        @reset="emit('redownloadReset')"
      />
    </SurfaceCard>
  </div>
</template>

<style scoped>
.pane-stack {
  display: grid;
  gap: var(--space-base);
}
</style>
