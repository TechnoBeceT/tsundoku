<script setup lang="ts">
/**
 * IconButton — a square, icon-only button (the Settings category edit/delete
 * row actions, dialog close buttons, etc.). The default slot is the icon (an
 * inline SVG or an <Icon>); there is no text label, so `ariaLabel` is REQUIRED
 * to give assistive tech a name.
 *
 *   - `variant` (default 'default'): 'default' (muted, neutral hover) or
 *     'danger' (destructive — danger-tinted text + a danger hover wash).
 *   - `size` (default 'md'): 'sm' (26px) | 'md' (30px) square.
 *   - `disabled`: blocks interaction + dims the button.
 *   - `ariaLabel` (required): the accessible name.
 *
 * Emits `click` (suppressed while disabled by the native button).
 */
withDefaults(defineProps<{
  /** Colour treatment — neutral 'default' or destructive 'danger'. */
  variant?: 'default' | 'danger'
  /** Square size: 'sm' 26px | 'md' 30px. */
  size?: 'sm' | 'md'
  /** Blocks interaction + dims the control. */
  disabled?: boolean
  /** Accessible name — required since the button shows no text. */
  ariaLabel: string
}>(), {
  variant: 'default',
  size: 'md',
  disabled: false,
})

const emit = defineEmits<{
  /** The button was activated. */
  click: []
}>()
</script>

<template>
  <button
    type="button"
    class="icon-btn"
    :class="[`icon-btn--${variant}`, `icon-btn--${size}`]"
    :disabled="disabled"
    :aria-label="ariaLabel"
    @click="emit('click')"
  >
    <slot />
  </button>
</template>

<style scoped>
.icon-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-sm);
  border: 1px solid var(--border2);
  background: transparent;
  cursor: pointer;
  transition: color 0.15s, background 0.15s, border-color 0.15s;
}

.icon-btn--sm {
  width: 26px;
  height: 26px;
}

.icon-btn--md {
  width: 30px;
  height: 30px;
}

.icon-btn--default {
  color: var(--muted);
}

.icon-btn--default:hover:not(:disabled) {
  color: var(--text);
  border-color: var(--accent);
}

.icon-btn--danger {
  border-color: var(--border);
  color: var(--danger-bright);
}

.icon-btn--danger:hover:not(:disabled) {
  background: var(--danger-bg);
}

.icon-btn:disabled {
  opacity: 0.5;
  cursor: default;
}

.icon-btn:focus-visible {
  outline: none;
  box-shadow: var(--ring-focus);
}
</style>
