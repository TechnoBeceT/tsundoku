<script setup lang="ts">
import SurfaceCard from '../ui/SurfaceCard.vue'
import TextField from '../ui/TextField.vue'
import Toggle from '../ui/Toggle.vue'
import type { ImpersonateConfig } from '../screens/settings.types'

/**
 * ImpersonateCard — the toggle-gated Chrome-fingerprint image-proxy card on the
 * Download engine Access & bypass section (GAP-111). The enable Toggle reveals
 * the global gateway URL. Per-source membership is deliberately absent: its
 * one-source-at-a-time control lives in Source exceptions.
 *
 *   - `modelValue` (v-model): the impersonate config being edited.
 * Emits `update:modelValue` with the full `{ ...config, <changed field> }`.
 */
const props = defineProps<{
  /** The impersonate config (v-model). */
  modelValue: ImpersonateConfig
}>()

const emit = defineEmits<{
  /** The config changed — carries the full updated object. */
  'update:modelValue': [value: ImpersonateConfig]
}>()

// Emit a shallow-merged copy so a single field edit never drops the rest (§16).
function patch(part: Partial<ImpersonateConfig>) {
  emit('update:modelValue', { ...props.modelValue, ...part })
}
</script>

<template>
  <SurfaceCard title="Chrome-fingerprint image proxy" sub="Configure the shared browser-fingerprint gateway; opt sources in from Source exceptions">
    <template #actions>
      <!-- eslint-disable-next-line vue/attribute-hyphenation -- camelCase :ariaLabel binds the REQUIRED prop; kebab :aria-label routes to the native attr, leaving it unset (vue-tsc error). -->
      <Toggle :model-value="modelValue.enabled" :ariaLabel="'Enable impersonate gateway'" @update:model-value="patch({ enabled: $event })" />
    </template>
    <div v-if="modelValue.enabled" class="imp-body">
      <TextField class="field--block" label="Gateway URL" :model-value="modelValue.url" @update:model-value="patch({ url: $event })" />
    </div>
  </SurfaceCard>
</template>

<style scoped>
.imp-body {
  margin-top: 16px;
}

.field--block {
  margin-bottom: 12px;
}

</style>
