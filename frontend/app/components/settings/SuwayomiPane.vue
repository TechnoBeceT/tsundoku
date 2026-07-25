<script setup lang="ts">
import { computed, reactive, watch } from 'vue'
import SaveFooter from '../ui/SaveFooter.vue'
import FlareSolverrCard from './FlareSolverrCard.vue'
import ImpersonateCard from './ImpersonateCard.vue'
import type { FlareSolverrConfig, ImpersonateConfig, SaveState } from '../screens/settings.types'

/**
 * SuwayomiPane — the "Server config" settings pane. Holds two Tsundoku-owned
 * cards, each with its own §16 SaveFooter: the FlareSolverr (Cloudflare-bypass)
 * card (QCAT-238) and the impersonate-gateway (Chrome-fingerprint image proxy)
 * card (GAP-111). The proxied Suwayomi SOCKS-proxy card + read-only DB display
 * were RETIRED with the P2 Suwayomi-removal backend cutover — the engine host
 * has no such passthrough endpoint. Do NOT re-add a SOCKS card here.
 *
 * Each card keeps a LOCAL editable copy seeded from its prop; Save emits the
 * edited config, and the copy re-seeds when the parent reflects the persisted
 * value back (§16 round-trip). Each Save button disables until its card is dirty.
 *
 * GOTCHA: `flare`/`imp` are `reactive()` objects, not `ref()`s — each card is
 * bound `:model-value`/`@update:model-value="v => Object.assign(target, v)"`
 * rather than a whole-object `v-model`. A whole-object `v-model` desugars to
 * `flare = $event` (reassigning the binding), which does not update the
 * underlying object in place; `Object.assign` mutates the existing reactive
 * object, matching the re-seed pattern the watchers below already use.
 *
 *   - `flareSolverr`: the Tsundoku-owned FlareSolverr config.
 *   - `flareSolverrSave`: the §16 save lifecycle for the FlareSolverr card.
 *   - `impersonate`: the Tsundoku-owned impersonate-gateway config.
 *   - `impersonateSave`: the §16 save lifecycle for the impersonate card.
 *
 * Emits `save-flaresolverr` / `save-impersonate` with the full merged config.
 */
const props = withDefaults(defineProps<{
  /** The Tsundoku-owned FlareSolverr config. */
  flareSolverr: FlareSolverrConfig
  /** §16 state of the FlareSolverr Save button. */
  flareSolverrSave?: SaveState
  /** The Tsundoku-owned impersonate-gateway config. */
  impersonate: ImpersonateConfig
  /** §16 state of the impersonate Save button. */
  impersonateSave?: SaveState
}>(), {
  flareSolverrSave: () => ({ status: 'idle' }),
  impersonateSave: () => ({ status: 'idle' }),
})

const emit = defineEmits<{
  /** Persist the edited FlareSolverr config — carries the full merged object. */
  'save-flaresolverr': [config: FlareSolverrConfig]
  /** Persist the edited impersonate config — carries the full merged object. */
  'save-impersonate': [config: ImpersonateConfig]
}>()

// Deep-clone helper keeps the local FlareSolverr copy fully detached from the prop.
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

// The impersonate card mirrors the same local-copy + dirty + §16 machinery.
const cloneImp = (i: ImpersonateConfig): ImpersonateConfig => ({ ...i })

const imp = reactive(cloneImp(props.impersonate))

watch(() => props.impersonate, v => Object.assign(imp, cloneImp(v)), { deep: true })

const impDirty = computed(() => JSON.stringify(imp) !== JSON.stringify(props.impersonate))

const impFooterState = computed(() => ({ status: props.impersonateSave.status, error: props.impersonateSave.message }))

function onSaveImpersonate() {
  if (!impDirty.value || props.impersonateSave.status === 'saving') return
  emit('save-impersonate', cloneImp(imp))
}
</script>

<template>
  <div class="pane-stack">
    <FlareSolverrCard :model-value="flare" @update:model-value="v => Object.assign(flare, v)" />
    <SaveFooter :state="flareFooterState" :dirty="flareDirty" label="Save FlareSolverr settings" @save="onSaveFlareSolverr" />
    <ImpersonateCard :model-value="imp" @update:model-value="v => Object.assign(imp, v)" />
    <SaveFooter :state="impFooterState" :dirty="impDirty" label="Save image-proxy settings" @save="onSaveImpersonate" />
  </div>
</template>

<style scoped>
/* Stacked cards, each with its own SaveFooter, in a flex column. */
.pane-stack {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
</style>
