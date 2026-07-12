<script setup lang="ts">
import { CheckboxRoot, CheckboxIndicator } from 'reka-ui'

/**
 * Checkbox — the square tick box for multi-select lists (as opposed to `Toggle`,
 * which is the on/off SWITCH for a single setting). Built on reka-ui's accessible
 * `CheckboxRoot` / `CheckboxIndicator` (real `role="checkbox"` + `aria-checked` +
 * keyboard support), dressed in the same token CSS the app's other tick boxes use.
 *
 * `modelValue` is the ticked state (v-model); `ariaLabel` is REQUIRED (the box has
 * no text of its own — the label sits beside it in the consuming row); `disabled`
 * dims it and blocks toggling.
 *
 * GOTCHA: reka's own model is tri-state (`boolean | 'indeterminate'`). This atom
 * is deliberately BINARY — it coerces the emit to a plain boolean, so a consumer
 * can never end up storing the string 'indeterminate' in a boolean ref.
 */
defineProps<{
  /** The ticked state (v-model). */
  modelValue: boolean
  /** Disables interaction + dims the control. */
  disabled?: boolean
  /** Accessible label — required since the box shows no text. */
  ariaLabel: string
}>()

const emit = defineEmits<{
  /** The box was ticked/unticked — carries the new boolean. */
  'update:modelValue': [value: boolean]
}>()
</script>

<template>
  <CheckboxRoot
    class="check"
    :model-value="modelValue"
    :disabled="disabled"
    :aria-label="ariaLabel"
    @update:model-value="emit('update:modelValue', $event === true)"
  >
    <CheckboxIndicator class="check__mark">
      <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <path d="M20 6L9 17l-5-5" />
      </svg>
    </CheckboxIndicator>
  </CheckboxRoot>
</template>

<style scoped>
.check {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  flex: none;
  padding: 0;
  border-radius: var(--radius-xs);
  border: 1.5px solid var(--border2);
  background: transparent;
  color: var(--cover-text);
  cursor: pointer;
  transition: background 0.15s, border-color 0.15s;
}

.check[data-state='checked'] {
  border-color: var(--accent);
  background: var(--accent);
}

.check:disabled {
  cursor: default;
  opacity: 0.6;
}

.check:focus-visible {
  outline: none;
  box-shadow: var(--ring-focus);
}

.check__mark {
  display: flex;
  align-items: center;
  justify-content: center;
}
</style>
