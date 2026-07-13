<script setup lang="ts">
import { computed } from 'vue'
import IconButton from './IconButton.vue'

/**
 * ChapterProgressStepper — a compact numeric −/＋ stepper for a bounded integer
 * (e.g. "chapters read"). Two `IconButton`s flank the current value; the buttons
 * clamp to `[min, max]` and disable at the ends. It is a controlled input: it
 * never mutates its own value, it emits `update:modelValue` with the clamped
 * next value for the parent to apply (`v-model`).
 *
 * This is DISTINCT from the flow `Stepper` (a numbered wizard-step indicator) —
 * this one is a plus/minus number spinner.
 *
 *   - `modelValue` (required): the current value.
 *   - `max`: upper bound (no upper bound when absent).
 *   - `min` (default 0): lower bound.
 *   - `disabled`: blocks both buttons.
 */
const props = withDefaults(defineProps<{
  /** The current value (controlled — bind with `v-model`). */
  modelValue: number
  /** Upper bound; unbounded above when omitted. */
  max?: number
  /** Lower bound. */
  min?: number
  /** Disables both buttons. */
  disabled?: boolean
}>(), {
  min: 0,
  disabled: false,
})

const emit = defineEmits<{
  /** The value changed — carries the clamped next value. */
  'update:modelValue': [value: number]
}>()

const atMin = computed(() => props.modelValue <= props.min)
const atMax = computed(() => props.max !== undefined && props.modelValue >= props.max)

// Clamp into range before emitting so the parent never receives an out-of-bounds
// value even if the current modelValue started outside it.
const step = (delta: number): void => {
  const next = props.modelValue + delta
  const lower = Math.max(next, props.min)
  const clamped = props.max !== undefined ? Math.min(lower, props.max) : lower
  if (clamped !== props.modelValue) emit('update:modelValue', clamped)
}
</script>

<template>
  <div class="stepper-num">
    <!-- eslint-disable vue/attribute-hyphenation -->
    <!-- camelCase :ariaLabel binds IconButton's REQUIRED prop; kebab :aria-label
         routes to the native attr, leaving the prop unset (vue-tsc error). -->
    <IconButton
      :ariaLabel="'Decrease'"
      :disabled="disabled || atMin"
      @click="step(-1)"
    >
      <Icon name="lucide:minus" />
    </IconButton>

    <span class="stepper-num__value">
      {{ modelValue }}<span v-if="max !== undefined" class="stepper-num__max">/{{ max }}</span>
    </span>

    <IconButton
      :ariaLabel="'Increase'"
      :disabled="disabled || atMax"
      @click="step(1)"
    >
      <Icon name="lucide:plus" />
    </IconButton>
    <!-- eslint-enable vue/attribute-hyphenation -->
  </div>
</template>

<style scoped>
.stepper-num {
  display: inline-flex;
  align-items: center;
  gap: 10px;
}

.stepper-num__value {
  min-width: 44px;
  text-align: center;
  font-family: var(--font-mono);
  font-size: var(--text-md);
  font-weight: var(--weight-bold);
  color: var(--text);
  font-variant-numeric: tabular-nums;
}

.stepper-num__max {
  color: var(--faint);
  font-weight: var(--weight-regular);
}
</style>
