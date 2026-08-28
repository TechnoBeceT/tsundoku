<script setup lang="ts">
import { computed, reactive, watch } from 'vue'
import DurationInput from '../ui/DurationInput.vue'
import SaveFooter from '../ui/SaveFooter.vue'
import SurfaceCard from '../ui/SurfaceCard.vue'
import TextField from '../ui/TextField.vue'
import LibraryDedupDialog from './LibraryDedupDialog.vue'
import RedownloadDialog from './RedownloadDialog.vue'
import SettingRow from './SettingRow.vue'
import SourceThroughputControl from './SourceThroughputControl.vue'
import FormError from '../ui/FormError.vue'
import type { SaveState, SourceOption, SourcesSettings } from '../screens/settings.types'
import type { RedownloadFilter, RedownloadPreview } from '~/composables/useRedownload'
import type { SourceThroughputPolicy } from '~/composables/useSourceThroughput'

/**
 * SourcesSettingsPane — the anti-IP-block runtime knobs (source-politeness
 * spec): the warm-up job's cadence + slow-source threshold, then the
 * per-source circuit-breaker's failure threshold + cooldown, then the
 * politeness delay between requests to one source. Sits above the existing
 * SourceMetricsPane in the same "Sources" nav area (Settings.vue stacks the
 * two), mirroring how LibraryPane stacks its own two SurfaceCards.
 *
 * Keeps a LOCAL editable copy seeded from `sources`; Save emits that copy, and
 * when the parent reflects the persisted value back the copy re-seeds (§16
 * round-trip). The Save button disables until the copy is dirty.
 *
 *   - `sources`: the 5 runtime-editable knobs (the source of truth).
 *   - `save`: the §16 state of the Save button.
 *
 * Also renders a second "Library maintenance" card, which COMPOSES
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
 * A third "Re-download from a source" card COMPOSES RedownloadDialog: the
 * owner-triggered, previewed bulk re-queue of already-downloaded chapters from
 * one source (`GET`/`POST /api/downloads/redownload`, see useRedownload), also
 * gated behind the shared ConfirmModal (QCAT-222) because it sweeps the whole
 * library. Its §16 state is owned by the parent, same as the dedup trio above.
 * Nothing is deleted by it — every existing CBZ stays on disk until its
 * replacement lands.
 *
 * Emits `save` with the full edited copy, `dedupAll` to trigger the sweep, and
 * `redownloadPreview` / `redownload` / `redownloadReset` for the re-download.
 */
const props = withDefaults(defineProps<{
  /** The runtime-editable warm-up/politeness knobs. */
  sources: SourcesSettings
  throughputPolicies?: SourceThroughputPolicy[]
  throughputSources?: SourceOption[]
  globalDownloadConcurrency?: number
  throughputLoading?: boolean
  throughputSavingSourceId?: string | null
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
  throughputSavingSourceId: null,
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
  /** Persist the edited knobs — carries the full edited copy. */
  save: [settings: SourcesSettings]
  saveConcurrency: [sourceId: string, value: number]
  inheritConcurrency: [sourceId: string]
  saveImageDelay: [sourceId: string, value: string]
  inheritImageDelay: [sourceId: string]
  /** Trigger the library-wide duplicate-source dedup sweep. */
  dedupAll: []
  /** Load the bulk-re-download preview for this filter (reads only). */
  redownloadPreview: [filter: RedownloadFilter]
  /** Apply the bulk re-download (reachable only via its ConfirmModal). */
  redownload: [filter: RedownloadFilter]
  /** The re-download filter changed — discard the loaded preview/outcome. */
  redownloadReset: []
}>()

// Deep-clone so the local copy is fully detached from the prop object.
const cloneSources = (s: SourcesSettings): SourcesSettings => ({
  warmupInterval: { ...s.warmupInterval },
  warmupSlowThresholdMs: s.warmupSlowThresholdMs,
  failureThreshold: s.failureThreshold,
  cooldown: { ...s.cooldown },
  minRequestDelayMs: s.minRequestDelayMs,
  imageRequestDelayMs: s.imageRequestDelayMs,
})

const src = reactive(cloneSources(props.sources))

// Re-seed on every source-of-truth change (post-save rehydrate, §16): dirty
// resets to false once the persisted values flow back.
watch(() => props.sources, v => Object.assign(src, cloneSources(v)), { deep: true })

const dirty = computed(() => JSON.stringify(src) !== JSON.stringify(props.sources))

// SaveFooter speaks the ui SaveState (`error`); the screen prop carries `message`.
const footerState = computed(() => ({ status: props.save.status, error: props.save.message }))

// Clamp a raw integer-field input to a non-negative integer (NaN / negatives → 0).
const clampInt = (raw: string): number => Math.max(0, Number.parseInt(raw, 10) || 0)
// Failure threshold is a floor-1 count (the backend rejects 0 with a 400) — a
// breaker must always require at least one failure before it can trip.
const clampMin1 = (raw: string): number => Math.max(1, Number.parseInt(raw, 10) || 1)

function onSave() {
  if (!dirty.value || props.save.status === 'saving') return
  emit('save', cloneSources(src))
}
const onSaveConcurrency = (sourceId: string, value: number) => emit('saveConcurrency', sourceId, value)
const onSaveImageDelay = (sourceId: string, value: string) => emit('saveImageDelay', sourceId, value)
</script>

<template>
  <SurfaceCard
    title="Anti-Block Protection"
    sub="Warm-up cadence + per-source circuit-breaker. Protects against a source hard-blocking this deployment's IP."
  >
    <SettingRow name="Warm-up interval" hint="How often to keep anti-bot source sessions warm; 0 disables (recommended if a source keeps getting IP-blocked)">
      <DurationInput v-model="src.warmupInterval" />
    </SettingRow>

    <SettingRow name="Warm-up slow threshold" hint="A source slower than this (ms) is treated as needing warming">
      <TextField compact type="number" :model-value="String(src.warmupSlowThresholdMs)" @update:model-value="src.warmupSlowThresholdMs = clampInt($event)" />
    </SettingRow>

    <SettingRow name="Failure threshold" hint="Consecutive failures before a source is paused">
      <TextField compact type="number" :model-value="String(src.failureThreshold)" @update:model-value="src.failureThreshold = clampMin1($event)" />
    </SettingRow>

    <SettingRow name="Source cooldown" hint="How long a failing/blocked source is paused">
      <DurationInput v-model="src.cooldown" />
    </SettingRow>

    <SettingRow name="Politeness delay" hint="Minimum gap (ms) between requests to one source; protects against IP blocks — 0 disables">
      <TextField compact type="number" :model-value="String(src.minRequestDelayMs)" @update:model-value="src.minRequestDelayMs = clampInt($event)" />
    </SettingRow>

    <SettingRow name="Image request delay" hint="Gap (ms) between individual image requests; 0 disables. Per-source overrides are below.">
      <TextField compact type="number" :model-value="String(src.imageRequestDelayMs)" @update:model-value="src.imageRequestDelayMs = clampInt($event)" />
    </SettingRow>

    <SaveFooter :state="footerState" :dirty="dirty" label="Save changes" @save="onSave" />
  </SurfaceCard>

  <SurfaceCard
    title="Per-source download pace"
    sub="Keep the global throughput for most sources, then slow down only providers that enforce tighter request limits."
  >
    <p v-if="throughputLoading" class="throughput-status" role="status">Loading source policies…</p>
    <FormError v-if="throughputError" :message="throughputError" />
    <SourceThroughputControl
      v-for="policy in throughputPolicies"
      :key="policy.sourceId"
      :policy="policy"
      :source-name="throughputSources.find(source => source.id === policy.sourceId)?.name"
      :global-concurrency="globalDownloadConcurrency"
      :global-image-delay="`${sources.imageRequestDelayMs}ms`"
      :saving="throughputSavingSourceId === policy.sourceId"
      @save-concurrency="onSaveConcurrency"
      @inherit-concurrency="emit('inheritConcurrency', $event)"
      @save-image-delay="onSaveImageDelay"
      @inherit-image-delay="emit('inheritImageDelay', $event)"
    />
    <p v-if="!throughputLoading && throughputPolicies.length === 0" class="throughput-status">No sources are available to configure.</p>
  </SurfaceCard>

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
</template>

<style scoped>
.throughput-status { margin: 0; color: var(--muted); font-size: var(--text-sm); }
</style>
