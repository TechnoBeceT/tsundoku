<script setup lang="ts">
/**
 * IconButton — a square, icon-only button (the Settings category edit/delete
 * row actions, dialog close buttons, etc.). The default slot is the icon (an
 * inline SVG or an <Icon>); there is no text label, so `ariaLabel` is REQUIRED
 * to give assistive tech a name.
 *
 *   - `variant` (default 'default'): 'default' (muted, neutral hover) or
 *     'danger' (destructive — danger-tinted text + a danger hover wash).
 *   - `size` (default 'md'): 'xs' (22px) | 'sm' (26px) | 'md' (30px) square.
 *   - `disabled`: blocks interaction + dims the button.
 *   - `ariaLabel` (required): the accessible name.
 *
 * Emits `click` (suppressed while disabled by the native button).
 */
withDefaults(defineProps<{
  /** Colour treatment — neutral 'default' or destructive 'danger'. */
  variant?: 'default' | 'danger'
  /** Square size: 'xs' 22px | 'sm' 26px | 'md' 30px (the `--control-*` ladder). */
  size?: 'xs' | 'sm' | 'md'
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

/* ---- Sizes ---------------------------------------------------------------- *
 * The squares ride the `--control-*` ladder (tokens/spacing.css) — same edge
 * lengths at the 16px desktop anchor, proportional everywhere else. The old raw
 * 22/26/30px stayed fixed while a phone shrank the label beside them ~20% (the
 * "square stays 22px at every width" bug stated as a feature). `xs` is the INLINE
 * ROW ACTION: `position: relative` anchors its mobile hit-area overlay below. */
.icon-btn--xs {
  position: relative;
  width: var(--control-xs);
  height: var(--control-xs);
}

.icon-btn--sm {
  width: var(--control-sm);
  height: var(--control-sm);
}

.icon-btn--md {
  width: var(--control-md);
  height: var(--control-md);
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

/* On a phone the 26/30px squares are below the QCAT-230 44px touch target, so
 * expand the tap area to 44×44 ≤900px (the icon stays centered); desktop keeps
 * the compact squares. `xs` is EXCLUDED from PAINTING 44px (that would undo the
 * point of a row-proportionate action) — it takes the same target from an
 * invisible centred overlay instead. */
@media (max-width: 900px) {
  .icon-btn:not(.icon-btn--xs) {
    min-width: 44px;
    min-height: 44px;
  }

  .icon-btn--xs::after {
    content: '';
    position: absolute;
    top: 50%;
    left: 50%;
    width: 44px;
    height: 44px;
    transform: translate(-50%, -50%);
  }
}
</style>
