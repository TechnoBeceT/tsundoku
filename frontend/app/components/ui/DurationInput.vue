<script setup lang="ts">
import SelectField from './SelectField.vue'
import type { DurationUnit, DurationValue, SelectOption } from './forms.types'

/**
 * DurationInput — a number field paired with a unit dropdown, editing a single
 * `{ value, unit }` duration (the Settings download/refresh interval + retry
 * backoff rows). The number is clamped to a non-negative integer on every edit.
 *
 *   - `modelValue` (v-model): `{ value: number; unit: 'h' | 'm' | 's' }`.
 *   - `disabled`: blocks both controls + dims them.
 *
 * Emits `update:modelValue` with the full `{ value, unit }` object whenever
 * either control changes.
 */
const props = withDefaults(defineProps<{
  /** The duration being edited (v-model). */
  modelValue: DurationValue
  /** Blocks both controls + dims them. */
  disabled?: boolean
}>(), {
  disabled: false,
})

const emit = defineEmits<{
  /** The duration changed — carries the full `{ value, unit }`. */
  'update:modelValue': [value: DurationValue]
}>()

// The unit dropdown's options — stable order h → m → s, short labels to match
// the prototype's compact "2 h" / "30 m" presentation.
const unitOptions: SelectOption[] = [
  { value: 'h', label: 'h' },
  { value: 'm', label: 'm' },
  { value: 's', label: 's' },
]

// Clamp the raw input to a non-negative integer (NaN / negatives → 0).
function onValueInput(event: Event) {
  const raw = Number.parseInt((event.target as HTMLInputElement).value, 10)
  const value = Number.isFinite(raw) && raw > 0 ? raw : 0
  emit('update:modelValue', { value, unit: props.modelValue.unit })
}

function onUnitChange(unit: string) {
  emit('update:modelValue', { value: props.modelValue.value, unit: unit as DurationUnit })
}
</script>

<template>
  <div class="dur">
    <input
      class="dur__num"
      type="number"
      min="0"
      step="1"
      :value="modelValue.value"
      :disabled="disabled"
      @input="onValueInput"
    >
    <SelectField
      class="dur__unit"
      :model-value="modelValue.unit"
      :options="unitOptions"
      :disabled="disabled"
      aria-label="Duration unit"
      @update:model-value="onUnitChange"
    />
  </div>
</template>

<style scoped>
.dur {
  display: flex;
  align-items: center;
  gap: 8px;
}

.dur__num {
  width: 74px;
  padding: 9px 11px;
  border-radius: var(--radius-md);
  border: 1px solid var(--border2);
  background: var(--bg2);
  color: var(--text);
  font-family: var(--font-sans);
  font-size: var(--text-base);
  outline: none;
  transition: border-color 0.15s, box-shadow 0.15s;
}

.dur__num:focus {
  border-color: var(--accent);
  box-shadow: var(--ring-focus);
}

.dur__num:disabled {
  opacity: 0.6;
  cursor: default;
}
</style>
