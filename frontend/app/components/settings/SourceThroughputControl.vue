<script setup lang="ts">
import { ref, watch } from 'vue'
import AppButton from '../ui/AppButton.vue'
import TextField from '../ui/TextField.vue'
import FormError from '../ui/FormError.vue'
import type { SourceThroughputPolicy } from '~/composables/useSourceThroughput'

const props = withDefaults(defineProps<{ policy: SourceThroughputPolicy, sourceName?: string, globalConcurrency?: number, globalImageDelay?: string, saving?: boolean, error?: string | null }>(), { sourceName: '', globalConcurrency: 5, globalImageDelay: '500ms', saving: false, error: null })
const emit = defineEmits<{
  'save-concurrency': [sourceId: string, value: number]
  'inherit-concurrency': [sourceId: string]
  'save-image-delay': [sourceId: string, value: string]
  'inherit-image-delay': [sourceId: string]
}>()
const concurrency = ref('')
const imageDelay = ref('')
watch(() => props.policy, (policy) => {
  concurrency.value = String(policy.downloadConcurrency.override ?? policy.downloadConcurrency.effective)
  imageDelay.value = policy.imageRequestDelay.override ?? policy.imageRequestDelay.effective
}, { immediate: true, deep: true })
const saveConcurrency = () => emit('save-concurrency', props.policy.sourceId, Number(concurrency.value))
const saveDelay = () => emit('save-image-delay', props.policy.sourceId, imageDelay.value)
</script>

<template>
  <section class="throughput" :aria-label="`${sourceName || policy.sourceId} throughput`">
    <header class="throughput__header">
      <div><strong>{{ sourceName || `Source ${policy.sourceId}` }}</strong><span v-if="sourceName" class="throughput__id">{{ policy.sourceId }}</span></div>
      <span v-if="saving" class="throughput__saving" role="status">Saving…</span>
    </header>
    <FormError v-if="error" :message="error" />
    <div class="throughput__policy">
      <div class="throughput__label"><strong>Chapter concurrency</strong><span>Global: {{ globalConcurrency }} → {{ policy.downloadConcurrency.override === null ? 'Uses global' : `Override: ${policy.downloadConcurrency.override}` }}</span><span class="throughput__effective">Effective: {{ policy.downloadConcurrency.effective }}</span></div>
      <div class="throughput__edit">
        <TextField v-model="concurrency" compact type="number" :disabled="saving" @enter="saveConcurrency" />
        <AppButton variant="mini" size="xs" :disabled="saving" @click="saveConcurrency">Set override</AppButton>
        <AppButton data-testid="inherit-concurrency" variant="text" size="xs" :disabled="saving || policy.downloadConcurrency.override === null" @click="emit('inherit-concurrency', policy.sourceId)">Use global</AppButton>
      </div>
    </div>
    <div class="throughput__policy">
      <div class="throughput__label"><strong>Image request delay</strong><span>Global: {{ globalImageDelay }} → {{ policy.imageRequestDelay.override === null ? 'Uses global' : `Override: ${policy.imageRequestDelay.override}` }}</span><span class="throughput__effective">Effective: {{ policy.imageRequestDelay.effective }}</span></div>
      <div class="throughput__edit">
        <label class="throughput__duration"><span class="sr-only">Image request delay</span><input v-model="imageDelay" data-testid="image-delay" :disabled="saving" @keydown.enter="saveDelay"></label>
        <AppButton variant="mini" size="xs" :disabled="saving" @click="saveDelay">Set override</AppButton>
        <AppButton data-testid="inherit-delay" variant="text" size="xs" :disabled="saving || policy.imageRequestDelay.override === null" @click="emit('inherit-image-delay', policy.sourceId)">Use global</AppButton>
      </div>
    </div>
  </section>
</template>

<style scoped>
.throughput { padding: 14px 0; border-bottom: 1px solid var(--border); }
.throughput__header, .throughput__policy, .throughput__edit { display: flex; align-items: center; }
.throughput__header { justify-content: space-between; gap: 12px; margin-bottom: 10px; color: var(--text); }
.throughput__id { margin-left: 8px; color: var(--faint); font: var(--text-xs) var(--font-mono); }
.throughput__saving { color: var(--accentBright); font-size: var(--text-xs); }
.throughput__policy { justify-content: space-between; gap: 18px; padding: 9px 0 9px 12px; border-left: 2px solid var(--border2); }
.throughput__label { display: grid; min-width: 0; gap: 2px; color: var(--muted); font-size: var(--text-xs); }
.throughput__label strong { color: var(--text); font-size: var(--text-sm); }
.throughput__effective { color: var(--accentBright); font-family: var(--font-mono); }
.throughput__edit { flex-wrap: wrap; justify-content: flex-end; gap: 7px; }
.throughput__duration input { width: 80px; padding: 8px 9px; border: 1px solid var(--border2); border-radius: var(--radius-md); outline: none; background: var(--bg2); color: var(--text); font-family: var(--font-mono); }
.throughput__duration input:focus { border-color: var(--accent); box-shadow: var(--ring-focus); }
.throughput__duration input:disabled { opacity: .6; }
.sr-only { position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px; overflow: hidden; clip: rect(0,0,0,0); white-space: nowrap; border: 0; }
@media (max-width: 700px) { .throughput__policy { align-items: flex-start; flex-direction: column; } .throughput__edit { justify-content: flex-start; width: 100%; } }
</style>
