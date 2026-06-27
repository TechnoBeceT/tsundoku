<script setup lang="ts">
import { SwitchRoot, SwitchThumb } from 'reka-ui'

/**
 * Toggle — an on/off switch (the 44×25 knob switch used by the Monitored /
 * Completed / "Upgrades only" controls). Built on reka-ui's accessible
 * `SwitchRoot` / `SwitchThumb` (real `role="switch"` + keyboard support),
 * dressed in the prototype's token CSS.
 *
 * `modelValue` is the on/off state (v-model); `ariaLabel` is REQUIRED (the switch
 * has no visible text of its own); `disabled` greys it out and blocks toggling.
 * Emits `update:modelValue` with the NEW boolean when the owner flips it.
 */
defineProps<{
  /** The on/off state (v-model). */
  modelValue: boolean
  /** Disables interaction + dims the control. */
  disabled?: boolean
  /** Accessible label — required since the switch shows no text. */
  ariaLabel: string
}>()

const emit = defineEmits<{
  /** The switch flipped — carries the new boolean. */
  'update:modelValue': [value: boolean]
}>()
</script>

<template>
  <SwitchRoot
    class="switch"
    :model-value="modelValue"
    :disabled="disabled"
    :aria-label="ariaLabel"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <SwitchThumb class="switch__knob" />
  </SwitchRoot>
</template>

<style scoped>
.switch {
  position: relative;
  width: 44px;
  height: 25px;
  flex: none;
  padding: 0;
  border-radius: var(--radius-pill);
  border: 1px solid var(--border);
  background: var(--surface3);
  cursor: pointer;
  transition: background 0.2s;
}

.switch[data-state='checked'] {
  background: var(--accent);
}

.switch:disabled {
  cursor: default;
  opacity: 0.6;
}

.switch:focus-visible {
  outline: none;
  box-shadow: var(--ring-focus);
}

.switch__knob {
  position: absolute;
  top: 2px;
  left: 2px;
  display: block;
  width: 19px;
  height: 19px;
  border-radius: 50%;
  background: var(--cover-text);
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.4);
  transition: left 0.2s;
}

.switch__knob[data-state='checked'] {
  left: 21px;
}
</style>
