<script setup lang="ts">
import { computed, reactive, watch } from 'vue'
import LockedRow from '../ui/LockedRow.vue'
import SaveFooter from '../ui/SaveFooter.vue'
import SurfaceCard from '../ui/SurfaceCard.vue'
import ProxyConfigCard from './ProxyConfigCard.vue'
import FlareSolverrCard from './FlareSolverrCard.vue'
import type { FlareSolverrConfig, SaveState, SocksProxyConfig, SuwayomiConfig } from '../screens/settings.types'

/**
 * SuwayomiPane — the proxied Suwayomi server config pane: the read-only Database
 * card (a deploy concern) plus the two editable, toggle-gated cards (SOCKS proxy
 * + FlareSolverr) and a §16 SaveFooter.
 *
 * Keeps LOCAL editable copies of the SOCKS + FlareSolverr config seeded from
 * `config`; Save emits the merged config (read-only DB passed through unchanged),
 * and the copies re-seed when the parent reflects the persisted value back (§16
 * round-trip). The Save button disables until something is dirty.
 *
 *   - `config`: the whole proxied Suwayomi config (read-only DB + two editables).
 *   - `save`: the §16 save lifecycle (loading / success / error).
 *
 * Emits `save` with the full merged config.
 */
const props = withDefaults(defineProps<{
  /** The proxied Suwayomi server config. */
  config: SuwayomiConfig
  /** §16 state of the Save button. */
  save?: SaveState
}>(), {
  save: () => ({ status: 'idle' }),
})

const emit = defineEmits<{
  /** Persist the edited Suwayomi config — carries the full merged object. */
  save: [config: SuwayomiConfig]
}>()

// Deep-clone helpers keep the local copies fully detached from the prop object.
const cloneSocks = (s: SocksProxyConfig): SocksProxyConfig => ({ ...s })
const cloneFlare = (f: FlareSolverrConfig): FlareSolverrConfig => ({
  ...f,
  timeout: { ...f.timeout },
  sessionTtl: { ...f.sessionTtl },
})

const socks = reactive(cloneSocks(props.config.socks))
const flare = reactive(cloneFlare(props.config.flareSolverr))

// Re-seed on every source-of-truth change (post-save rehydrate, §16).
watch(() => props.config.socks, v => Object.assign(socks, cloneSocks(v)), { deep: true })
watch(() => props.config.flareSolverr, v => Object.assign(flare, cloneFlare(v)), { deep: true })

const dirty = computed(() =>
  JSON.stringify(socks) !== JSON.stringify(props.config.socks)
  || JSON.stringify(flare) !== JSON.stringify(props.config.flareSolverr),
)

// SaveFooter speaks the ui SaveState (`error`); the screen prop carries `message`.
const footerState = computed(() => ({ status: props.save.status, error: props.save.message }))

function onSave() {
  if (!dirty.value || props.save.status === 'saving') return
  emit('save', {
    database: props.config.database,
    socks: cloneSocks(socks),
    flareSolverr: cloneFlare(flare),
  })
}
</script>

<template>
  <div class="pane-stack">
    <SurfaceCard title="Database" sub="The engine's DB backend — a deploy concern, read-only here.">
      <LockedRow plain label="Type" :value="config.database.type" />
      <LockedRow plain label="URL" :value="config.database.url" />
      <LockedRow plain label="Username" :value="config.database.username" />
      <LockedRow plain muted label="Password" value="••••••••" />
    </SurfaceCard>

    <ProxyConfigCard v-model="socks" />

    <FlareSolverrCard v-model="flare" />

    <SaveFooter :state="footerState" :dirty="dirty" label="Save engine settings" @save="onSave" />
  </div>
</template>

<style scoped>
/* The pane stacks the DB card, the two gated cards, and the SaveFooter with the
   shared 16px inter-card rhythm. */
.pane-stack {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
</style>
