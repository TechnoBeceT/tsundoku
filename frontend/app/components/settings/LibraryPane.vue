<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import DurationInput from '../ui/DurationInput.vue'
import LockedRow from '../ui/LockedRow.vue'
import SaveFooter from '../ui/SaveFooter.vue'
import SurfaceCard from '../ui/SurfaceCard.vue'
import TextField from '../ui/TextField.vue'
import SettingRow from './SettingRow.vue'
import type { LibrarySettings, SaveState, SystemInfo } from '../screens/settings.types'

/**
 * LibraryPane — the "Schedules & Behavior" pane: the runtime-editable library
 * knobs (three duration rows + three integer rows + an advanced disclosure) and
 * the read-only System card of deploy-time facts.
 *
 * Keeps a LOCAL editable copy seeded from `library`; Save emits that copy, and
 * when the parent reflects the persisted value back the copy re-seeds (§16
 * round-trip). The Save button disables until the copy is dirty.
 *
 *   - `library`: the runtime-editable knobs (the source of truth).
 *   - `system`: read-only deploy-time facts for the System card.
 *   - `save`: the §16 save lifecycle (loading / success / error).
 *
 * Emits `save` with the full edited copy.
 */
const props = withDefaults(defineProps<{
  /** The runtime-editable library knobs. */
  library: LibrarySettings
  /** Read-only deploy-time facts (env-sourced). */
  system: SystemInfo
  /** §16 state of the Save button. */
  save?: SaveState
}>(), {
  save: () => ({ status: 'idle' }),
})

const emit = defineEmits<{
  /** Persist the edited knobs — carries the full edited copy. */
  save: [settings: LibrarySettings]
}>()

// Deep-clone so the local copy is fully detached from the prop object.
const cloneLibrary = (l: LibrarySettings): LibrarySettings => ({
  refreshInterval: { ...l.refreshInterval },
  downloadInterval: { ...l.downloadInterval },
  retryBackoff: { ...l.retryBackoff },
  maxRetries: l.maxRetries,
  staleGraceDays: l.staleGraceDays,
  refreshConcurrency: l.refreshConcurrency,
})

const lib = reactive(cloneLibrary(props.library))

// Re-seed on every source-of-truth change (post-save rehydrate, §16): dirty
// resets to false once the persisted values flow back.
watch(() => props.library, v => Object.assign(lib, cloneLibrary(v)), { deep: true })

const dirty = computed(() => JSON.stringify(lib) !== JSON.stringify(props.library))

// SaveFooter speaks the ui SaveState (`error`); the screen prop carries `message`.
const footerState = computed(() => ({ status: props.save.status, error: props.save.message }))

const advancedOpen = ref(false)

// Clamp a raw integer-field input to a non-negative integer (NaN / negatives → 0).
const clampInt = (raw: string): number => Math.max(0, Number.parseInt(raw, 10) || 0)
// Chapter max retries is a PER-SOURCE budget with a hard floor of 1 (a source must
// always get at least one attempt — the backend rejects 0 with a 400).
const clampMin1 = (raw: string): number => Math.max(1, Number.parseInt(raw, 10) || 1)

function onSave() {
  if (!dirty.value || props.save.status === 'saving') return
  emit('save', cloneLibrary(lib))
}
</script>

<template>
  <div class="pane-stack">
    <SurfaceCard
      title="Schedules & Behavior"
      sub="Runtime-editable timing. The job schedulers re-read these on the next tick."
    >
      <SettingRow name="Refresh interval" hint="How often to poll titles for new chapters">
        <DurationInput v-model="lib.refreshInterval" />
      </SettingRow>

      <SettingRow name="Download interval" hint="Queue-drain & upgrade-swap cadence">
        <DurationInput v-model="lib.downloadInterval" />
      </SettingRow>

      <SettingRow name="Chapter retry backoff" hint="Wait before retrying a failed chapter">
        <DurationInput v-model="lib.retryBackoff" />
      </SettingRow>

      <SettingRow name="Chapter max retries" hint="Attempts per source before that source is given up; a chapter fails only when all its sources are exhausted">
        <TextField compact type="number" :model-value="String(lib.maxRetries)" @update:model-value="lib.maxRetries = clampMin1($event)" />
      </SettingRow>

      <SettingRow name="Stale-grace days" hint="Health threshold before a source counts as stale">
        <TextField compact type="number" :model-value="String(lib.staleGraceDays)" @update:model-value="lib.staleGraceDays = clampInt($event)" />
      </SettingRow>

      <div class="advanced">
        <button type="button" class="advanced__toggle" @click="advancedOpen = !advancedOpen">
          <svg class="advanced__chev" :class="{ 'advanced__chev--open': advancedOpen }" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M9 18l6-6-6-6" /></svg>
          Advanced
        </button>
        <SettingRow v-if="advancedOpen" flush name="Refresh concurrency" hint="Parallel source fetches — be gentle on sources">
          <TextField compact type="number" :model-value="String(lib.refreshConcurrency)" @update:model-value="lib.refreshConcurrency = clampInt($event)" />
        </SettingRow>
      </div>

      <SaveFooter :state="footerState" :dirty="dirty" label="Save changes" @save="onSave" />
    </SurfaceCard>

    <SurfaceCard
      title="System"
      sub="Set at deploy time via environment variables — read-only here."
    >
      <LockedRow label="Storage folder" :value="system.storageFolder" />
      <LockedRow label="Server port" :value="system.serverPort" />
      <LockedRow label="Database" :value="system.database" />
    </SurfaceCard>
  </div>
</template>

<style scoped>
/* The pane stacks two cards with the shared 16px inter-card rhythm. */
.pane-stack {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

/* ---- Advanced disclosure -------------------------------------------------- */
.advanced {
  border-top: 1px solid var(--border);
  padding-top: 11px;
  margin-top: 2px;
}

.advanced__toggle {
  display: flex;
  align-items: center;
  gap: 7px;
  background: none;
  border: none;
  color: var(--muted);
  font-family: var(--font-sans);
  font-size: 12.5px;
  font-weight: var(--weight-bold);
  cursor: pointer;
  padding: 0;
}

.advanced__chev {
  transition: transform 0.15s;
}

.advanced__chev--open {
  transform: rotate(90deg);
}
</style>
