<script setup lang="ts">
import LockedRow from '../ui/LockedRow.vue'
import SurfaceCard from '../ui/SurfaceCard.vue'
import Toggle from '../ui/Toggle.vue'
import SettingRow from './SettingRow.vue'
import type { SystemInfo } from '../screens/settings.types'

/**
 * Library-only settings that remain after scheduling and capacity moved to the
 * Download engine pane: metadata auto-identification plus read-only deploy facts.
 *
 *   - `system`: read-only deploy-time facts for the System card.
 *   - `autoIdentify`: the current `metadata.auto_identify` setting value —
 *     mirrors TrackersPane's own `autoUpdateTrack` toggle wiring (a
 *     standalone tunable, saved independently of the Save-button batch above,
 *     since flipping it takes effect immediately rather than needing Save).
 *   - `autoIdentifyBusy`: true while the toggle's own save is in flight.
 *
 * Emits `toggle-auto-identify` with the new boolean value.
 */
withDefaults(defineProps<{
  /** Read-only deploy-time facts (env-sourced). */
  system: SystemInfo
  /** The current `metadata.auto_identify` setting value. */
  autoIdentify?: boolean
  /** True while the auto-identify toggle's own save is in flight. */
  autoIdentifyBusy?: boolean
}>(), {
  autoIdentify: true,
  autoIdentifyBusy: false,
})

const emit = defineEmits<{
  /** The auto-identify toggle was flipped — carries the new value. */
  'toggle-auto-identify': [value: boolean]
}>()
</script>

<template>
  <div class="pane-stack">
    <SurfaceCard
      title="Library behavior"
      sub="Metadata behavior that is independent of the download engine."
    >
      <SettingRow name="Auto-identify new series" hint="Automatically match + merge rich metadata for a freshly adopted/imported series. Applies immediately (no Save needed) and never overrides a series you've hand-picked matches for.">
        <!-- eslint-disable-next-line vue/attribute-hyphenation -- camelCase :ariaLabel binds the REQUIRED prop; kebab :aria-label routes to the native attr, leaving it unset (vue-tsc error). -->
        <Toggle :model-value="autoIdentify" :ariaLabel="'Auto-identify new series'" :disabled="autoIdentifyBusy" @update:model-value="emit('toggle-auto-identify', $event)" />
      </SettingRow>
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

</style>
