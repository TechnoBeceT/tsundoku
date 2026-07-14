<script setup lang="ts">
import { computed, reactive, watch } from 'vue'
import SaveFooter from '../ui/SaveFooter.vue'
import ProxyConfigCard from './ProxyConfigCard.vue'
import FlareSolverrCard from './FlareSolverrCard.vue'
import type { FlareSolverrConfig, SaveState, SocksProxyConfig, SuwayomiConfig } from '../screens/settings.types'

/**
 * SuwayomiPane — the Suwayomi/engine config pane: the proxied SOCKS card
 * (Suwayomi's own settings) and the Tsundoku-owned FlareSolverr card
 * (QCAT-238), each with its OWN §16 SaveFooter — they hit two DIFFERENT
 * backend endpoints (`/api/suwayomi/settings` vs `/api/flaresolverr/settings`)
 * via two independent composables (useSuwayomiSettings /
 * useFlareSolverrSettings), so a save/error in one is never conflated with
 * the other's state. They still render stacked in one pane because that is
 * where the owner expects to find both Cloudflare-bypass knobs.
 *
 * Keeps LOCAL editable copies of the SOCKS + FlareSolverr config seeded from
 * props; each Save emits its own config, and the copy re-seeds when the
 * parent reflects the persisted value back (§16 round-trip). Each Save button
 * disables until its own card is dirty.
 *
 * GOTCHA: `socks`/`flare` are `reactive()` objects, not `ref()`s — the cards are
 * bound `:model-value`/`@update:model-value="… => Object.assign(target, v)"`
 * rather than whole-object `v-model`. A whole-object `v-model` desugars to
 * `socks = $event` (reassigning the binding), which does not update the
 * underlying object in place; `Object.assign` mutates the existing reactive
 * object, matching the re-seed pattern the watchers below already use.
 *
 *   - `config`: the proxied Suwayomi SOCKS config.
 *   - `save`: the §16 save lifecycle for the SOCKS card.
 *   - `flareSolverr`: the Tsundoku-owned FlareSolverr config.
 *   - `flareSolverrSave`: the §16 save lifecycle for the FlareSolverr card.
 *
 * Emits `save` (SOCKS) and `save-flaresolverr` (FlareSolverr), each with its
 * full merged config.
 */
const props = withDefaults(defineProps<{
  /** The proxied Suwayomi SOCKS config. */
  config: SuwayomiConfig
  /** §16 state of the SOCKS Save button. */
  save?: SaveState
  /** The Tsundoku-owned FlareSolverr config. */
  flareSolverr: FlareSolverrConfig
  /** §16 state of the FlareSolverr Save button. */
  flareSolverrSave?: SaveState
}>(), {
  save: () => ({ status: 'idle' }),
  flareSolverrSave: () => ({ status: 'idle' }),
})

const emit = defineEmits<{
  /** Persist the edited SOCKS config — carries the full merged object. */
  save: [config: SuwayomiConfig]
  /** Persist the edited FlareSolverr config — carries the full merged object. */
  'save-flaresolverr': [config: FlareSolverrConfig]
}>()

// Deep-clone helpers keep the local copies fully detached from the prop object.
const cloneSocks = (s: SocksProxyConfig): SocksProxyConfig => ({ ...s })
const cloneFlare = (f: FlareSolverrConfig): FlareSolverrConfig => ({
  ...f,
  timeout: { ...f.timeout },
  sessionTtl: { ...f.sessionTtl },
})

const socks = reactive(cloneSocks(props.config.socks))
const flare = reactive(cloneFlare(props.flareSolverr))

// Re-seed on every source-of-truth change (post-save rehydrate, §16).
watch(() => props.config.socks, v => Object.assign(socks, cloneSocks(v)), { deep: true })
watch(() => props.flareSolverr, v => Object.assign(flare, cloneFlare(v)), { deep: true })

const socksDirty = computed(() => JSON.stringify(socks) !== JSON.stringify(props.config.socks))
const flareDirty = computed(() => JSON.stringify(flare) !== JSON.stringify(props.flareSolverr))

// SaveFooter speaks the ui SaveState (`error`); the screen prop carries `message`.
const socksFooterState = computed(() => ({ status: props.save.status, error: props.save.message }))
const flareFooterState = computed(() => ({ status: props.flareSolverrSave.status, error: props.flareSolverrSave.message }))

function onSaveSocks() {
  if (!socksDirty.value || props.save.status === 'saving') return
  emit('save', { database: props.config.database, socks: cloneSocks(socks) })
}

function onSaveFlareSolverr() {
  if (!flareDirty.value || props.flareSolverrSave.status === 'saving') return
  emit('save-flaresolverr', cloneFlare(flare))
}
</script>

<template>
  <div class="pane-stack">
    <ProxyConfigCard :model-value="socks" @update:model-value="v => Object.assign(socks, v)" />
    <SaveFooter :state="socksFooterState" :dirty="socksDirty" label="Save SOCKS settings" @save="onSaveSocks" />

    <FlareSolverrCard :model-value="flare" @update:model-value="v => Object.assign(flare, v)" />
    <SaveFooter :state="flareFooterState" :dirty="flareDirty" label="Save FlareSolverr settings" @save="onSaveFlareSolverr" />
  </div>
</template>

<style scoped>
/* The pane stacks the two gated cards (each with its own SaveFooter) with the
   shared 16px inter-card rhythm. */
.pane-stack {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
</style>
