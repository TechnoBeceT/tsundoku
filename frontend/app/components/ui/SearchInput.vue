<script setup lang="ts">
/**
 * SearchInput — a search-styled text input with a leading magnifier icon and an
 * optional clear (×) button (the Downloads activity searchbar + the Import
 * cross-source search). Standalone (not label-wrapped); the icon sits inside the
 * input frame and the value indents past it.
 *
 *   - `modelValue` (v-model): the search string.
 *   - `placeholder`: the empty-state hint text.
 *   - `clearable` (default true): show a × button to clear the field when it has text.
 *
 * Emits `update:modelValue` on every keystroke (and on clear → '') and `enter`
 * when Enter is pressed (run the search).
 */
withDefaults(defineProps<{
  /** The search value (v-model). */
  modelValue: string
  /** Empty-state placeholder. */
  placeholder?: string
  /** Show the clear (×) button when the field has text. */
  clearable?: boolean
}>(), {
  clearable: true,
})

const emit = defineEmits<{
  /** The value changed — carries the new string ('' on clear). */
  'update:modelValue': [value: string]
  /** Enter was pressed — run the search. */
  'enter': []
}>()

function onInput(event: Event) {
  emit('update:modelValue', (event.target as HTMLInputElement).value)
}
</script>

<template>
  <div class="search">
    <svg class="search__icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <circle cx="11" cy="11" r="7" />
      <path d="M21 21l-4.3-4.3" />
    </svg>
    <input
      class="search__input"
      type="search"
      :value="modelValue"
      :placeholder="placeholder"
      @input="onInput"
      @keydown.enter="emit('enter')"
    >
    <button
      v-if="clearable && modelValue"
      type="button"
      class="search__clear"
      aria-label="Clear search"
      @click="emit('update:modelValue', '')"
    >
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <path d="M18 6 6 18M6 6l12 12" />
      </svg>
    </button>
  </div>
</template>

<style scoped>
.search {
  position: relative;
  display: flex;
  align-items: center;
  width: 100%;
}

.search__icon {
  position: absolute;
  left: 13px;
  color: var(--faint);
  pointer-events: none;
}

.search__input {
  width: 100%;
  padding: 10px 13px 10px 38px;
  border-radius: var(--radius-md);
  border: 1px solid var(--border2);
  background: var(--bg2);
  color: var(--text);
  font-family: var(--font-sans);
  font-size: var(--text-base);
  outline: none;
  transition: border-color 0.15s, box-shadow 0.15s;
}

/* Strip the browser-native search clear affordance — we render our own. */
.search__input::-webkit-search-cancel-button {
  appearance: none;
}

.search__input::placeholder {
  color: var(--faint);
}

.search__input:focus {
  border-color: var(--accent);
  box-shadow: var(--ring-focus);
}

.search__clear {
  position: absolute;
  right: 10px;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 2px;
  border: none;
  background: none;
  color: var(--faint);
  cursor: pointer;
  transition: color 0.15s;
}

.search__clear:hover {
  color: var(--text);
}

.search__clear:focus-visible {
  outline: none;
  color: var(--accentBright);
}
</style>
