<script setup lang="ts">
import TextField from '../ui/TextField.vue'
import Toggle from '../ui/Toggle.vue'
import type { SocksProxyConfig } from '../screens/settings.types'

/**
 * ProxyConfigCard — the toggle-gated SOCKS-proxy card on the Suwayomi server
 * config pane. The enable Toggle reveals the connection fields; editing any field
 * emits the full updated config (v-model), so the parent pane owns the dirty/save
 * state for the whole Suwayomi config.
 *
 *   - `modelValue` (v-model): the SOCKS config being edited.
 *
 * Emits `update:modelValue` with the full `{ ...config, <changed field> }`.
 */
const props = defineProps<{
  /** The SOCKS-proxy config (v-model). */
  modelValue: SocksProxyConfig
}>()

const emit = defineEmits<{
  /** The config changed — carries the full updated object. */
  'update:modelValue': [value: SocksProxyConfig]
}>()

// Emit a shallow-merged copy so a single field edit never drops the rest (§16).
function patch(part: Partial<SocksProxyConfig>) {
  emit('update:modelValue', { ...props.modelValue, ...part })
}
</script>

<template>
  <section class="card">
    <div class="card__head-row">
      <div>
        <h2 class="card__title">SOCKS proxy</h2>
        <p class="card__sub card__sub--tight">Route source traffic through a SOCKS proxy</p>
      </div>
      <Toggle :model-value="modelValue.enabled" aria-label="Enable SOCKS proxy" @update:model-value="patch({ enabled: $event })" />
    </div>
    <div v-if="modelValue.enabled" class="field-grid">
      <TextField label="Version" :model-value="modelValue.version" @update:model-value="patch({ version: $event })" />
      <TextField label="Host" placeholder="127.0.0.1" :model-value="modelValue.host" @update:model-value="patch({ host: $event })" />
      <TextField label="Port" :model-value="modelValue.port" @update:model-value="patch({ port: $event })" />
      <TextField label="Username" :model-value="modelValue.username" @update:model-value="patch({ username: $event })" />
      <TextField class="field--full" label="Password" type="password" placeholder="••••••••" :model-value="modelValue.password" @update:model-value="patch({ password: $event })" />
    </div>
  </section>
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

.card__head-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.card__sub {
  font-size: 12.5px;
  color: var(--faint);
  margin: 2px 0 8px;
}

.card__sub--tight {
  margin-bottom: 0;
}

.field-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
  margin-top: 16px;
}

.field--full {
  grid-column: span 2;
}
</style>
