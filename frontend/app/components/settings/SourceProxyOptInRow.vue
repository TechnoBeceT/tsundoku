<script setup lang="ts">
import { computed } from 'vue'
import SettingRow from './SettingRow.vue'
import FormError from '../ui/FormError.vue'
import Toggle from '../ui/Toggle.vue'

/**
 * SourceProxyOptInRow — explicit image-proxy membership. This is intentionally
 * separate from ordinary inherited settings: Off is the safe extension-native
 * path, while On is a deliberate source-level opt-in.
 */
const props = withDefaults(defineProps<{
  enabled: boolean
  effectiveAvailable: boolean
  saving?: boolean
  error?: string | null
}>(), {
  saving: false,
  error: null,
})

const emit = defineEmits<{
  'set-override': [key: 'imageProxy', value: boolean]
}>()

const stateLabel = computed(() => {
  if (!props.enabled) return 'Off'
  return props.effectiveAvailable ? 'On · active' : 'On · unavailable'
})
</script>

<template>
  <div
    class="proxy-row"
    :class="{ 'proxy-row--enabled': enabled }"
    :aria-busy="saving"
  >
    <SettingRow
      name="Image proxy"
      hint="Off keeps this source on its native image path. Turn on only when its image host blocks the standard client; extension image processing does not run through the proxy."
    >
      <div class="proxy-row__control">
        <span class="proxy-row__state" aria-live="polite">{{ stateLabel }}</span>
        <!-- eslint-disable vue/attribute-hyphenation -->
        <Toggle
          :model-value="enabled"
          :disabled="saving"
          ariaLabel="Image proxy"
          @update:model-value="emit('set-override', 'imageProxy', $event)"
        />
        <!-- eslint-enable vue/attribute-hyphenation -->
      </div>
    </SettingRow>
    <FormError v-if="error" class="proxy-row__error" :message="error" />
  </div>
</template>

<style scoped>
.proxy-row {
  min-width: 0;
  max-width: 100%;
  padding-left: var(--space-md);
  border-left: 3px solid var(--border2);
}

.proxy-row--enabled {
  border-left-color: var(--accent);
}

.proxy-row__control {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: var(--space-sm);
  flex: none;
}

.proxy-row__state {
  color: var(--muted);
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  white-space: nowrap;
}

.proxy-row--enabled .proxy-row__state {
  color: var(--accentBright);
}

.proxy-row__error {
  padding: 0 0 var(--space-sm);
}

@media (max-width: 900px) {
  .proxy-row__control {
    width: 100%;
    justify-content: flex-start;
  }
}
</style>
