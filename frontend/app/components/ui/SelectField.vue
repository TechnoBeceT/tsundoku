<script setup lang="ts">
import type { SelectOption } from './forms.types'

/**
 * SelectField — a token-styled native `<select>` (the Series-Detail provider
 * picker, the Settings duration-unit dropdown). Native rendering keeps it
 * accessible by default; the only styling is the token background/border plus a
 * custom chevron (the browser arrow is hidden via `appearance: none`).
 *
 *   - `modelValue` (v-model): the selected option's `value`.
 *   - `options`: the `{ value, label }[]` to render.
 *   - `disabled`: blocks selection + dims the control.
 *   - `ariaLabel`: accessible name (use when there's no associated visible label).
 *
 * Emits `update:modelValue` with the newly-selected value.
 */
defineProps<{
  /** The selected option value (v-model). */
  modelValue: string
  /** The options to render. */
  options: SelectOption[]
  /** Blocks selection + dims the control. */
  disabled?: boolean
  /** Accessible name when no visible label is associated. */
  ariaLabel?: string
}>()

const emit = defineEmits<{
  /** The selection changed — carries the new value. */
  'update:modelValue': [value: string]
}>()

function onChange(event: Event) {
  emit('update:modelValue', (event.target as HTMLSelectElement).value)
}
</script>

<template>
  <span class="select">
    <select
      class="select__el"
      :value="modelValue"
      :disabled="disabled"
      :aria-label="ariaLabel"
      @change="onChange"
    >
      <option v-for="opt in options" :key="opt.value" :value="opt.value">{{ opt.label }}</option>
    </select>
    <svg class="select__chev" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="m6 9 6 6 6-6" />
    </svg>
  </span>
</template>

<style scoped>
.select {
  position: relative;
  display: inline-flex;
  align-items: center;
  min-width: 0;
}

.select__el {
  width: 100%;
  padding: 9px 34px 9px 12px;
  border-radius: var(--radius-md);
  border: 1px solid var(--border2);
  background: var(--bg2);
  color: var(--text);
  font-family: var(--font-sans);
  font-size: var(--text-base);
  font-weight: var(--weight-semibold);
  cursor: pointer;
  outline: none;
  appearance: none;
  transition: border-color 0.15s, box-shadow 0.15s;
}

.select__el:focus {
  border-color: var(--accent);
  box-shadow: var(--ring-focus);
}

.select__el:disabled {
  opacity: 0.6;
  cursor: default;
}

.select__chev {
  position: absolute;
  right: 12px;
  color: var(--muted);
  pointer-events: none;
}
</style>
