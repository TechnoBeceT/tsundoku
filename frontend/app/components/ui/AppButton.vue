<script setup lang="ts">
import { computed } from 'vue'
import Spinner from './Spinner.vue'

/**
 * AppButton — the one button primitive for the whole app. Every screen button
 * (Settings save / mini / solid / text, Import primary+ghost, Series-Detail
 * confirm/cancel, the Auth submit, the AppShell "Adopt" header CTA) collapses
 * into this single atom so the look is unified per variant.
 *
 *   - `variant` (default 'primary'): the colour treatment —
 *       · 'primary'      → the accent-gradient CTA with a `--cover-text` label
 *       · 'solid'        → a flat accent fill (same label colour, no gradient)
 *       · 'ghost'        → transparent with a border (secondary action)
 *       · 'mini'         → a compact bordered surface chip (row action)
 *       · 'text'         → a quiet muted bordered button (low-emphasis)
 *       · 'danger-ghost' → the destructive treatment (danger border + tint)
 *   - `size` (default 'md'): 'sm' | 'md' | 'lg' — drives padding + font size.
 *   - `loading`: shows a <Spinner> in the icon slot's place and disables the button (§16).
 *   - `disabled`: blocks interaction + dims the button.
 *   - `type` (default 'button'): 'button' | 'submit' (use 'submit' inside a form).
 *
 * Slots: `icon` (leading, replaced by the spinner while loading) + default (the label).
 * Emits `click` (suppressed while loading/disabled — the native button guards it).
 */
const props = withDefaults(defineProps<{
  /** Colour treatment — see the component doc above. */
  variant?: 'primary' | 'ghost' | 'solid' | 'mini' | 'text' | 'danger-ghost'
  /** Size scale — drives padding + font size. */
  size?: 'sm' | 'md' | 'lg'
  /** In-flight state: swaps the leading icon for a spinner and disables. */
  loading?: boolean
  /** Disables interaction + dims the button. */
  disabled?: boolean
  /** Native button type — 'submit' to submit the enclosing form. */
  type?: 'button' | 'submit'
}>(), {
  variant: 'primary',
  size: 'md',
  loading: false,
  disabled: false,
  type: 'button',
})

const emit = defineEmits<{
  /** The button was activated (only fires when enabled + not loading). */
  click: []
}>()

// loading implies disabled — the control can't be re-triggered mid-flight.
const isDisabled = computed(() => props.disabled || props.loading)

// Spinner tone: filled variants carry a light label, so the ring is on-accent;
// everything else inherits the text colour.
const spinnerTone = computed(() =>
  props.variant === 'primary' || props.variant === 'solid' ? 'on-accent' : 'current')

// Ring diameter tracks the size scale so it sits on the label baseline.
const spinnerSize = computed(() => ({ sm: 13, md: 14, lg: 16 }[props.size]))
</script>

<template>
  <button
    class="btn"
    :class="[`btn--${variant}`, `btn--${size}`]"
    :type="type"
    :disabled="isDisabled"
    @click="emit('click')"
  >
    <Spinner v-if="loading" :size="spinnerSize" :tone="spinnerTone" />
    <span v-else-if="$slots.icon" class="btn__icon"><slot name="icon" /></span>
    <slot />
  </button>
</template>

<style scoped>
.btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  border: 1px solid transparent;
  font-family: var(--font-sans);
  font-weight: var(--weight-bold);
  cursor: pointer;
  transition: filter 0.15s, border-color 0.15s, color 0.15s, background 0.15s;
}

.btn:disabled {
  cursor: default;
}

.btn__icon {
  display: inline-flex;
}

/* ---- Sizes ---------------------------------------------------------------- */
.btn--sm {
  padding: 7px 13px;
  border-radius: var(--radius-md);
  font-size: var(--text-sm);
}

.btn--md {
  padding: 10px 18px;
  border-radius: var(--radius-lg);
  font-size: 13.5px;
}

.btn--lg {
  padding: 12px 24px;
  border-radius: var(--radius-lg);
  font-size: var(--text-md);
}

/* ---- Variants ------------------------------------------------------------- */
.btn--primary {
  background: linear-gradient(135deg, var(--accent), var(--accentDeep));
  color: var(--cover-text);
  box-shadow: var(--shadow-accent);
}

.btn--primary:hover:not(:disabled) {
  filter: brightness(1.08);
}

.btn--primary:disabled {
  background: var(--surface3);
  color: var(--faint);
  box-shadow: none;
}

.btn--solid {
  background: var(--accent);
  color: var(--cover-text);
}

.btn--solid:hover:not(:disabled) {
  filter: brightness(1.08);
}

.btn--solid:disabled {
  background: var(--surface3);
  color: var(--faint);
}

.btn--ghost {
  background: transparent;
  border-color: var(--border2);
  color: var(--text);
}

.btn--ghost:hover:not(:disabled) {
  border-color: var(--accent);
  color: var(--accentBright);
}

.btn--ghost:disabled {
  opacity: 0.6;
}

.btn--mini {
  background: var(--surface);
  border-color: var(--border2);
  color: var(--text);
}

.btn--mini:hover:not(:disabled) {
  border-color: var(--accent);
  color: var(--accentBright);
}

.btn--mini:disabled {
  color: var(--faint);
  opacity: 0.5;
}

.btn--text {
  background: transparent;
  border-color: var(--border2);
  color: var(--muted);
}

.btn--text:hover:not(:disabled) {
  border-color: var(--accent);
  color: var(--accentBright);
}

.btn--text:disabled {
  opacity: 0.6;
}

.btn--danger-ghost {
  background: var(--danger-bg);
  border-color: var(--danger-border);
  color: var(--danger-bright);
}

.btn--danger-ghost:hover:not(:disabled) {
  background: var(--danger-bg-hover);
}

.btn--danger-ghost:disabled {
  opacity: 0.6;
}

.btn:focus-visible {
  outline: none;
  box-shadow: var(--ring-focus);
}
</style>
