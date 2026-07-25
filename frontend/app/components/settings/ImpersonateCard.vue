<script setup lang="ts">
import SurfaceCard from '../ui/SurfaceCard.vue'
import TextField from '../ui/TextField.vue'
import Toggle from '../ui/Toggle.vue'
import type { ImpersonateConfig } from '../screens/settings.types'

/**
 * ImpersonateCard — the toggle-gated Chrome-fingerprint image-proxy card on the
 * Server config pane (GAP-111). The enable Toggle reveals the gateway URL field;
 * editing either field emits the full updated config (v-model) so the parent
 * pane owns the dirty/save state. Mirrors FlareSolverrCard's shape, with just an
 * enable toggle + a URL (no timeout/session controls).
 *
 *   - `modelValue` (v-model): the impersonate config being edited.
 *
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
  <SurfaceCard title="Chrome-fingerprint image proxy" sub="Fetch images through a browser-fingerprint proxy for sources whose CDN blocks the default client">
    <template #actions>
      <!-- eslint-disable-next-line vue/attribute-hyphenation -- camelCase :ariaLabel binds the REQUIRED prop; kebab :aria-label routes to the native attr, leaving it unset (vue-tsc error). -->
      <Toggle :model-value="modelValue.enabled" :ariaLabel="'Enable impersonate gateway'" @update:model-value="patch({ enabled: $event })" />
    </template>
    <div v-if="modelValue.enabled" class="imp-body">
      <TextField class="field--block" label="Gateway URL" :model-value="modelValue.url" @update:model-value="patch({ url: $event })" />
      <p class="imp-hint">
        Only needed for the rare source whose image CDN blocks Tsundoku's default fingerprint. Leave the URL blank to turn it off.
      </p>
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

.imp-hint {
  margin: 0;
  font-size: 12.5px;
  color: var(--muted);
  line-height: 1.5;
}
</style>
