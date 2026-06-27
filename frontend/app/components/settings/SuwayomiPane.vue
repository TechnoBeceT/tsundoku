<script setup lang="ts">
import { computed, reactive, watch } from 'vue'
import SaveFooter from '../ui/SaveFooter.vue'
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
  <section class="card">
    <h2 class="card__title">Database</h2>
    <p class="card__sub">The engine's DB backend — a deploy concern, read-only here.</p>
    <div class="lrow"><span class="lrow__label-plain">Type</span><span class="lrow__val">{{ config.database.type }}</span></div>
    <div class="lrow"><span class="lrow__label-plain">URL</span><span class="lrow__val">{{ config.database.url }}</span></div>
    <div class="lrow"><span class="lrow__label-plain">Username</span><span class="lrow__val">{{ config.database.username }}</span></div>
    <div class="lrow"><span class="lrow__label-plain">Password</span><span class="lrow__val lrow__val--muted">••••••••</span></div>
  </section>

  <ProxyConfigCard v-model="socks" />

  <FlareSolverrCard v-model="flare" />

  <SaveFooter class="suwa-foot" :state="footerState" :dirty="dirty" label="Save engine settings" @save="onSave" />
</template>

<style scoped>
.card {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-2xl);
  padding: 20px;
  margin-bottom: 16px;
}

.card__title {
  font-family: var(--font-display);
  font-weight: var(--weight-bold);
  font-size: var(--text-lg);
  color: var(--text);
  margin: 0;
}

.card__sub {
  font-size: 12.5px;
  color: var(--faint);
  margin: 2px 0 8px;
}

/* ---- Read-only DB rows (plain label, no padlock) -------------------------- */
.lrow {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 12px 0;
  border-top: 1px solid var(--border);
}

.lrow__label-plain {
  font-size: var(--text-base);
  color: var(--muted);
}

.lrow__val {
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  color: var(--text);
}

.lrow__val--muted {
  color: var(--muted);
}

/* SaveFooter sits a touch lower under the cards (matches the original 16px gap). */
.suwa-foot {
  margin-top: 16px;
}
</style>
