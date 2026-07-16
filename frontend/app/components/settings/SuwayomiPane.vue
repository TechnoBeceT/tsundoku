<script setup lang="ts">
import { computed, reactive, watch } from 'vue'
import SaveFooter from '../ui/SaveFooter.vue'
import FlareSolverrCard from './FlareSolverrCard.vue'
import type { FlareSolverrConfig, SaveState } from '../screens/settings.types'

/**
 * SuwayomiPane — the "Server config" settings pane. Now holds ONLY the
 * Tsundoku-owned FlareSolverr card (QCAT-238); the proxied Suwayomi
 * SOCKS-proxy card + read-only DB display were RETIRED with the P2
 * Suwayomi-removal backend cutover — the engine host has no such passthrough
 * endpoint (`/api/suwayomi/settings` is gone), so there is nothing left to
 * proxy. Do NOT re-add a SOCKS card here: that capability has no backend.
 *
 * Keeps a LOCAL editable copy of the FlareSolverr config seeded from props;
 * Save emits the edited config, and the copy re-seeds when the parent
 * reflects the persisted value back (§16 round-trip). The Save button
 * disables until the card is dirty.
 *
 * GOTCHA: `flare` is a `reactive()` object, not a `ref()` — the card is bound
 * `:model-value`/`@update:model-value="… => Object.assign(target, v)"` rather
 * than a whole-object `v-model`. A whole-object `v-model` desugars to
 * `flare = $event` (reassigning the binding), which does not update the
 * underlying object in place; `Object.assign` mutates the existing reactive
 * object, matching the re-seed pattern the watcher below already uses.
 *
 *   - `flareSolverr`: the Tsundoku-owned FlareSolverr config.
 *   - `flareSolverrSave`: the §16 save lifecycle for the FlareSolverr card.
 *
 * Emits `save-flaresolverr` with the full merged config.
 */
const props = withDefaults(defineProps<{
  /** The Tsundoku-owned FlareSolverr config. */
  flareSolverr: FlareSolverrConfig
  /** §16 state of the FlareSolverr Save button. */
  flareSolverrSave?: SaveState
}>(), {
  flareSolverrSave: () => ({ status: 'idle' }),
})

const emit = defineEmits<{
  /** Persist the edited FlareSolverr config — carries the full merged object. */
  'save-flaresolverr': [config: FlareSolverrConfig]
}>()

// Deep-clone helper keeps the local copy fully detached from the prop object.
const cloneFlare = (f: FlareSolverrConfig): FlareSolverrConfig => ({
  ...f,
  timeout: { ...f.timeout },
  sessionTtl: { ...f.sessionTtl },
})

const flare = reactive(cloneFlare(props.flareSolverr))

// Re-seed on every source-of-truth change (post-save rehydrate, §16).
watch(() => props.flareSolverr, v => Object.assign(flare, cloneFlare(v)), { deep: true })

const flareDirty = computed(() => JSON.stringify(flare) !== JSON.stringify(props.flareSolverr))

// SaveFooter speaks the ui SaveState (`error`); the screen prop carries `message`.
const flareFooterState = computed(() => ({ status: props.flareSolverrSave.status, error: props.flareSolverrSave.message }))

function onSaveFlareSolverr() {
  if (!flareDirty.value || props.flareSolverrSave.status === 'saving') return
  emit('save-flaresolverr', cloneFlare(flare))
}
</script>

<template>
  <div class="pane-stack">
    <FlareSolverrCard :model-value="flare" @update:model-value="v => Object.assign(flare, v)" />
    <SaveFooter :state="flareFooterState" :dirty="flareDirty" label="Save FlareSolverr settings" @save="onSaveFlareSolverr" />
  </div>
</template>

<style scoped>
/* Single-card pane, kept as a flex column for consistency with the other
   panes' stacked-card layout even though there's only one card now. */
.pane-stack {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
</style>
