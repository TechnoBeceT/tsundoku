<script setup lang="ts">
import type { SegmentOption } from './controls.types'

/**
 * SegmentedToggle — a small filled-pill segmented switch (e.g. Popular / Latest
 * on Discover). One option is active at a time, painted in the accent fill; the
 * rest are quiet. Built as an accessible `role="tablist"` of `role="tab"`
 * buttons.
 *
 * `modelValue` is the active option `key` (v-model); `options` is the ordered
 * list of `{ key, label }`. Emits `update:modelValue` with the picked `key`.
 */
defineProps<{
  /** The active option key (v-model). */
  modelValue: string
  /** The ordered options to render. */
  options: SegmentOption[]
}>()

const emit = defineEmits<{
  /** A segment was picked — carries its key. */
  'update:modelValue': [value: string]
}>()
</script>

<template>
  <div class="seg" role="tablist">
    <button
      v-for="o in options"
      :key="o.key"
      type="button"
      role="tab"
      class="seg__btn"
      :class="{ 'seg__btn--active': modelValue === o.key }"
      :aria-selected="modelValue === o.key"
      @click="emit('update:modelValue', o.key)"
    >
      {{ o.label }}
    </button>
  </div>
</template>

<style scoped>
.seg {
  display: inline-flex;
  gap: var(--space-2xs);
  padding: var(--space-2xs);
  background: var(--surface2);
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
}

.seg__btn {
  padding: var(--space-xs) var(--space-lg); /* 8px 16px @16 */
  border-radius: 0.5625rem; /* 9px @16 — off-ladder radius, byte-identical */
  border: none;
  background: transparent;
  color: var(--muted);
  font-family: var(--font-sans);
  font-size: var(--text-base);
  font-weight: var(--weight-bold);
  cursor: pointer;
  transition: color 0.15s, background 0.15s;
}

.seg__btn:hover {
  color: var(--text);
}

.seg__btn--active {
  background: var(--accent);
  color: var(--cover-text);
}

.seg__btn:focus-visible {
  outline: none;
  box-shadow: var(--ring-focus);
}
</style>
