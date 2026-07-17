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
  /** Size scale — drives padding + font size. `xs` is the compact INLINE row
   *  action (sits in a dense line of text); `sm`/`md`/`lg` step up from there. */
  size?: 'xs' | 'sm' | 'md' | 'lg'
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
const spinnerSize = computed(() => ({ xs: 12, sm: 13, md: 14, lg: 16 }[props.size]))
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
  gap: var(--space-xs);
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

/* ---- Sizes ---------------------------------------------------------------- *
 * On the rem scale (was raw px): a button that stays a fixed px size while the
 * text beside it shrinks ~20% on a phone is the "really big and spaced" defect
 * (GAP-092). `position: relative` on the compact sizes anchors the mobile hit-
 * area overlay below.
 *
 * 🔴 Off-ladder padding/font values (7/13/13.5px) are expressed as byte-identical
 * `rem` (value ÷ 16), NOT rounded onto a near token — rounding at the 1440 anchor
 * MOVES pixels, which QCAT-261 forbids (§5.11). The mechanism fix (rem ⇒ scales
 * on a phone) is the fix the owner asked for; the desktop value is preserved. */

/* `xs` — the compact INLINE row action, deliberately the row's own text scale,
 * not a step down from `sm`: it sits INSIDE a dense line of text + badges, so
 * anything that reserves its own block reads as a slab dropped into the row. */
.btn--xs {
  position: relative;
  gap: var(--space-2xs);
  padding: var(--space-3xs) var(--space-xs);
  border-radius: var(--radius-sm);
  font-size: var(--text-xs);
  line-height: 1.35;
}

.btn--sm {
  position: relative;
  padding: 0.4375rem 0.8125rem; /* 7px 13px @16 — off-ladder, byte-identical */
  border-radius: var(--radius-md);
  font-size: var(--text-sm);
}

.btn--md {
  padding: var(--space-sm) var(--space-xl); /* 10px 18px @16 */
  border-radius: var(--radius-lg);
  font-size: 0.84375rem; /* 13.5px @16 — off-ladder, byte-identical */
}

.btn--lg {
  padding: var(--space-md) var(--space-2xl); /* 12px 24px @16 */
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

/* ---- Mobile touch targets (QCAT-230) -------------------------------------- *
 * The 44px touch target is the ONE thing here that legitimately must NOT scale:
 * a finger is a physical size, not a font, so it stays raw px (with the 1px
 * hairlines and the 900px breakpoint, the only sanctioned raw-px values). The
 * sizes SPLIT on how they pay for it:
 *   xs · sm  COMPACT — sit in a row / beside the tab pills. They take the target
 *            as an INVISIBLE OVERLAY and keep their scaled size.
 *   md · lg  STANDALONE — a form submit, a dialog primary, the AppShell CTA.
 *            They take it as PAINT: a 44px-tall CTA on a phone is the norm.
 * The md/lg horizontal compaction is rem (still scales); it buys the width that
 * keeps a two-button dialog foot inside a 320px phone. */
@media (max-width: 900px) {
  .btn--md,
  .btn--lg {
    min-height: 44px;
  }

  .btn--md {
    padding: var(--space-xs) var(--space-md);
  }

  .btn--lg {
    padding: var(--space-xs) var(--space-lg);
  }

  /* `max(100%, 44px)` only ever GROWS the target: a label wider than 44px keeps
   * its own width, so in practice the overlay extends VERTICALLY only. Horizontal
   * overhang appears only for a sub-44px-wide control — two of those adjacent can
   * overlap targets, which is why a row keeps a touch-pitch gap before a
   * destructive action. Widen the gap, never the overlay. */
  .btn--xs::after,
  .btn--sm::after {
    content: '';
    position: absolute;
    top: 50%;
    left: 50%;
    width: max(100%, 44px);
    height: max(100%, 44px);
    transform: translate(-50%, -50%);
  }
}
</style>
