<script setup lang="ts">
/**
 * StatTile — a small labelled metric: a big number over a muted caption.
 *
 * Used in stat strips / summary headers. The value renders large in the `tone`
 * colour; the label is the muted caption beneath. Pass plain props for the
 * common case, or use the default slot to render a custom value node.
 *
 *   - `label` (required): the caption beneath the value.
 *   - `value` (required): the headline number/string (ignored if the default
 *     slot is filled).
 *   - `tone`  (CSS colour, default `var(--text)`): the value colour. Pass a
 *     token-backed `var(--…)` value; never a raw hex.
 */
withDefaults(defineProps<{
  /** Muted caption beneath the value. */
  label: string
  /** Headline number/string (ignored when the default slot is used). */
  value: string | number
  /** Value colour — a token-backed CSS value. */
  tone?: string
}>(), {
  tone: 'var(--text)',
})
</script>

<template>
  <div class="stat">
    <div class="stat__value" :style="{ color: tone }">
      <slot>{{ value }}</slot>
    </div>
    <div class="stat__label">{{ label }}</div>
  </div>
</template>

<style scoped>
.stat {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.stat__value {
  font-family: var(--font-display);
  font-size: var(--text-3xl);
  font-weight: var(--weight-bold);
  line-height: 1.1;
}

.stat__label {
  font-size: var(--text-sm);
  font-weight: var(--weight-semibold);
  color: var(--muted);
}
</style>
