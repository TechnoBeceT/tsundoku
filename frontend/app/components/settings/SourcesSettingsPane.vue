<script setup lang="ts">
import { computed, reactive, watch } from 'vue'
import DurationInput from '../ui/DurationInput.vue'
import SaveFooter from '../ui/SaveFooter.vue'
import SurfaceCard from '../ui/SurfaceCard.vue'
import TextField from '../ui/TextField.vue'
import SettingRow from './SettingRow.vue'
import type { SaveState, SourcesSettings } from '../screens/settings.types'

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
 * Emits `save` with the full edited copy.
 */
const props = withDefaults(defineProps<{
  /** The runtime-editable warm-up/politeness knobs. */
  sources: SourcesSettings
  /** §16 state of the Save button. */
  save?: SaveState
}>(), {
  save: () => ({ status: 'idle' }),
})

const emit = defineEmits<{
  /** Persist the edited knobs — carries the full edited copy. */
  save: [settings: SourcesSettings]
}>()

// Deep-clone so the local copy is fully detached from the prop object.
const cloneSources = (s: SourcesSettings): SourcesSettings => ({
  warmupInterval: { ...s.warmupInterval },
  warmupSlowThresholdMs: s.warmupSlowThresholdMs,
  failureThreshold: s.failureThreshold,
  cooldown: { ...s.cooldown },
  minRequestDelayMs: s.minRequestDelayMs,
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

    <SaveFooter :state="footerState" :dirty="dirty" label="Save changes" @save="onSave" />
  </SurfaceCard>
</template>
