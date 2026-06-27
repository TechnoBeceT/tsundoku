<script setup lang="ts">
import SurfaceCard from '../ui/SurfaceCard.vue'
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
  <SurfaceCard title="SOCKS proxy" sub="Route source traffic through a SOCKS proxy">
    <template #actions>
      <Toggle :model-value="modelValue.enabled" aria-label="Enable SOCKS proxy" @update:model-value="patch({ enabled: $event })" />
    </template>
    <div v-if="modelValue.enabled" class="field-grid">
      <TextField label="Version" :model-value="modelValue.version" @update:model-value="patch({ version: $event })" />
      <TextField label="Host" placeholder="127.0.0.1" :model-value="modelValue.host" @update:model-value="patch({ host: $event })" />
      <TextField label="Port" :model-value="modelValue.port" @update:model-value="patch({ port: $event })" />
      <TextField label="Username" :model-value="modelValue.username" @update:model-value="patch({ username: $event })" />
      <TextField class="field--full" label="Password" type="password" placeholder="••••••••" :model-value="modelValue.password" @update:model-value="patch({ password: $event })" />
    </div>
  </SurfaceCard>
</template>

<style scoped>
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
