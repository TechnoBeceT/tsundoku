<script setup lang="ts">
/**
 * TextField — a labelled text input (the Auth credential fields, the Settings
 * proxy/host fields, the category rename input). Renders an optional uppercase
 * label above a token-styled input with an accent focus ring.
 *
 *   - `modelValue` (v-model): the input's string value.
 *   - `label`: the field label shown above the input (omit for a bare input).
 *   - `type` (default 'text'): the native input type ('text' | 'password' | …).
 *   - `placeholder`: the empty-state hint text.
 *   - `disabled`: blocks input + dims the field.
 *   - `mono`: renders the value in the monospace font (URLs / tokens / IDs).
 *   - `autocomplete`: the native `autocomplete` token (e.g. 'username',
 *     'current-password') so the browser can offer + store credential autofill.
 *   - `name`: the native input `name` (paired with `autocomplete` so password
 *     managers can associate the field).
 *   - `compact`: a fixed-width inline variant for the Settings integer rows —
 *     the input shrinks to a number-sized box instead of label-above-full-width.
 *
 * Emits `update:modelValue` on every keystroke, `enter` when Enter is pressed,
 * and `blur` when the input loses focus (for commit-on-blur consumers).
 */
withDefaults(defineProps<{
  /** The input value (v-model). */
  modelValue: string
  /** Optional label shown above the input. */
  label?: string
  /** Native input type. */
  type?: string
  /** Empty-state placeholder. */
  placeholder?: string
  /** Blocks input + dims the field. */
  disabled?: boolean
  /** Render the value in the monospace font. */
  mono?: boolean
  /** Native `autocomplete` token for browser/password-manager autofill. */
  autocomplete?: string
  /** Native input `name` (pair with `autocomplete` for credential autofill). */
  name?: string
  /** Fixed-width inline variant (Settings integer rows) instead of full-width. */
  compact?: boolean
}>(), {
  type: 'text',
  disabled: false,
  mono: false,
  compact: false,
})

const emit = defineEmits<{
  /** The value changed — carries the new string. */
  'update:modelValue': [value: string]
  /** Enter was pressed in the input. */
  'enter': []
  /** The input lost focus (for commit-on-blur consumers). */
  'blur': []
}>()

// Push the input event up as a typed string (the DOM gives us a generic Event).
function onInput(event: Event) {
  emit('update:modelValue', (event.target as HTMLInputElement).value)
}
</script>

<template>
  <label class="field" :class="{ 'field--compact': compact }">
    <span v-if="label" class="field__label">{{ label }}</span>
    <input
      class="field__input"
      :class="{ 'field__input--mono': mono }"
      :type="type"
      :value="modelValue"
      :placeholder="placeholder"
      :disabled="disabled"
      :autocomplete="autocomplete"
      :name="name"
      @input="onInput"
      @keydown.enter="emit('enter')"
      @blur="emit('blur')"
    >
  </label>
</template>

<style scoped>
.field {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

/* Inline fixed-width variant: a number-sized box (matches the Settings
   integer rows) instead of stretching to the container width. */
.field--compact {
  width: max-content;
}

.field--compact .field__input {
  width: 80px;
  padding: 9px 11px;
}

.field__label {
  display: block;
  margin-bottom: 6px;
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  text-transform: uppercase;
  letter-spacing: var(--tracking-label);
  color: var(--faint);
}

.field__input {
  width: 100%;
  padding: 10px 13px;
  border-radius: var(--radius-md);
  border: 1px solid var(--border2);
  background: var(--bg2);
  color: var(--text);
  font-family: var(--font-sans);
  font-size: var(--text-base);
  outline: none;
  transition: border-color 0.15s, box-shadow 0.15s;
}

.field__input--mono {
  font-family: var(--font-mono);
}

.field__input::placeholder {
  color: var(--faint);
}

.field__input:focus {
  border-color: var(--accent);
  box-shadow: var(--ring-focus);
}

.field__input:disabled {
  opacity: 0.6;
  cursor: default;
}
</style>
