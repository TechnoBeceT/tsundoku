<script setup lang="ts">
import DurationInput from '../ui/DurationInput.vue'
import SurfaceCard from '../ui/SurfaceCard.vue'
import TextField from '../ui/TextField.vue'
import Toggle from '../ui/Toggle.vue'
import type { FlareSolverrConfig } from '../screens/settings.types'

/**
 * FlareSolverrCard — the toggle-gated FlareSolverr (Cloudflare-bypass) card on
 * the Suwayomi server config pane. The enable Toggle reveals the server URL,
 * timeout, session, and response-fallback controls; editing any field emits the
 * full updated config (v-model) so the parent pane owns the dirty/save state.
 *
 *   - `modelValue` (v-model): the FlareSolverr config being edited.
 *
 * Emits `update:modelValue` with the full `{ ...config, <changed field> }`.
 */
const props = defineProps<{
  /** The FlareSolverr config (v-model). */
  modelValue: FlareSolverrConfig
}>()

const emit = defineEmits<{
  /** The config changed — carries the full updated object. */
  'update:modelValue': [value: FlareSolverrConfig]
}>()

// Emit a shallow-merged copy so a single field edit never drops the rest (§16).
function patch(part: Partial<FlareSolverrConfig>) {
  emit('update:modelValue', { ...props.modelValue, ...part })
}
</script>

<template>
  <SurfaceCard title="Cloudflare bypass (FlareSolverr)" sub="Solve Cloudflare challenges for protected sources">
    <template #actions>
      <Toggle :model-value="modelValue.enabled" aria-label="Enable FlareSolverr" @update:model-value="patch({ enabled: $event })" />
    </template>
    <div v-if="modelValue.enabled" class="flare-body">
      <TextField class="field--block" label="Server URL" :model-value="modelValue.url" @update:model-value="patch({ url: $event })" />
      <div class="field-grid">
        <div class="field">
          <span class="field__label">Request timeout</span>
          <DurationInput :model-value="modelValue.timeout" @update:model-value="patch({ timeout: $event })" />
        </div>
        <TextField label="Session name" :model-value="modelValue.session" @update:model-value="patch({ session: $event })" />
        <div class="field">
          <span class="field__label">Session TTL</span>
          <DurationInput :model-value="modelValue.sessionTtl" @update:model-value="patch({ sessionTtl: $event })" />
        </div>
        <div class="field field--inline">
          <Toggle :model-value="modelValue.fallback" aria-label="Response fallback" @update:model-value="patch({ fallback: $event })" />
          <span class="field__inline-label">Response fallback</span>
        </div>
      </div>
    </div>
  </SurfaceCard>
</template>

<style scoped>
.flare-body {
  margin-top: 16px;
}

.field-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
  margin-top: 16px;
}

.field {
  display: flex;
  flex-direction: column;
}

.field--block {
  margin-bottom: 12px;
}

.field--inline {
  flex-direction: row;
  align-items: center;
  gap: 10px;
  align-self: end;
  padding-bottom: 2px;
}

.field__label {
  display: block;
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  text-transform: uppercase;
  letter-spacing: var(--tracking-label);
  color: var(--faint);
  margin-bottom: 6px;
}

.field__inline-label {
  font-size: 12.5px;
  font-weight: var(--weight-semibold);
  color: var(--muted);
}
</style>
