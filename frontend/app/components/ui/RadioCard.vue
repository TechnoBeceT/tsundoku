<script setup lang="ts">
/**
 * RadioCard — a large selectable card carrying a radio dot, a title, and an
 * optional hint line (the Series-Detail delete-dialog "keep / also delete"
 * choice). The default slot is the title; the `hint` slot is the smaller
 * description below it.
 *
 * `selected` drives the active styling + the filled radio dot; `variant="danger"`
 * switches the active treatment to the danger palette (the destructive choice).
 * Real radio semantics: the card is `role="radio"` with `aria-checked`, and
 * emits `select` when clicked.
 */
withDefaults(defineProps<{
  /** Whether this card is the chosen option. */
  selected: boolean
  /** `danger` recolours the active state for a destructive choice. */
  variant?: 'default' | 'danger'
}>(), {
  variant: 'default',
})

const emit = defineEmits<{
  /** The card was chosen. */
  select: []
}>()
</script>

<template>
  <button
    type="button"
    class="radiocard"
    :class="[`radiocard--${variant}`, { 'radiocard--selected': selected }]"
    role="radio"
    :aria-checked="selected"
    @click="emit('select')"
  >
    <span class="radiocard__dot" :class="{ 'radiocard__dot--selected': selected }" />
    <span class="radiocard__body">
      <span class="radiocard__title"><slot /></span>
      <span v-if="$slots.hint" class="radiocard__hint"><slot name="hint" /></span>
    </span>
  </button>
</template>

<style scoped>
.radiocard {
  display: flex;
  gap: 12px;
  width: 100%;
  padding: 14px;
  border-radius: 13px;
  border: 1.5px solid var(--border);
  background: var(--surface2);
  text-align: left;
  cursor: pointer;
  transition: border-color 0.15s, background 0.15s;
}

/* Active treatment — default (accent) vs danger. */
.radiocard--default.radiocard--selected {
  border-color: var(--accent);
  background: var(--accentSoft);
}

.radiocard--danger.radiocard--selected {
  border-color: var(--danger);
  background: var(--danger-bg);
}

.radiocard__dot {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 18px;
  margin-top: 1px;
  flex: none;
  border-radius: 50%;
  border: 2px solid var(--border2);
}

.radiocard--default .radiocard__dot--selected {
  border-color: var(--accent);
}

.radiocard--danger .radiocard__dot--selected {
  border-color: var(--danger);
}

.radiocard__dot--selected::after {
  content: '';
  width: 9px;
  height: 9px;
  border-radius: 50%;
}

.radiocard--default .radiocard__dot--selected::after {
  background: var(--accent);
}

.radiocard--danger .radiocard__dot--selected::after {
  background: var(--danger);
}

.radiocard__body {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.radiocard__title {
  font-size: var(--text-md);
  font-weight: var(--weight-bold);
  color: var(--text);
}

.radiocard--danger .radiocard__title {
  color: var(--danger-bright);
}

.radiocard__hint {
  font-size: var(--text-sm);
  line-height: 1.45;
  color: var(--muted);
}
</style>
