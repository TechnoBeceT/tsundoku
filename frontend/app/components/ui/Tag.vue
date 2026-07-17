<script setup lang="ts">
/**
 * Tag — a small UPPERCASE status marker (extrabold, letter-spaced). Used for the
 * short state words scattered across the screens: PAUSED, DONE, DEFAULT,
 * PREFERRED, PLANNED, UPDATE, UPGRADE, IN LIBRARY, etc. The default slot is the
 * marker text; the optional `icon` slot renders a leading glyph.
 *
 * `tone` picks the colour treatment (all token-only → both themes work):
 *   - `neutral` (default): `--surface3` / `--faint` (e.g. PLANNED, DEFAULT).
 *   - `accent`: `--accentSoft` / `--accentBright` (e.g. PREFERRED).
 *   - `success`: emerald on-cover green (e.g. DONE, IN LIBRARY).
 *   - `warn`: amber (e.g. UPGRADE / UPDATE).
 *   - `danger`: rose (a problem marker).
 *   - `frost`: translucent on-cover marker (e.g. PAUSED over a cover), blurred.
 */
withDefaults(defineProps<{
  /** Colour treatment — see the component doc above. */
  tone?: 'neutral' | 'accent' | 'success' | 'warn' | 'danger' | 'frost'
  /** Font-size step: `md` (default, the marker's own size) | `sm` (the shared
   *  on-tile step down, matching `ui/Chip` size="sm" when they sit adjacent). */
  size?: 'md' | 'sm'
  /** Allow a long marker (e.g. "NEEDS SOURCE") to wrap to a second line in a
   *  narrow tile, instead of forcing horizontal overflow. */
  wrap?: boolean
}>(), {
  tone: 'neutral',
  size: 'md',
  wrap: false,
})
</script>

<template>
  <span class="tag" :class="[`tag--${tone}`, `tag--${size}`, { 'tag--wrap': wrap }]">
    <span v-if="$slots.icon" class="tag__icon"><slot name="icon" /></span>
    <slot />
  </span>
</template>

<style scoped>
/* Box metrics come from the SHARED badge scale (tokens/badge.css) that this atom
 * and `ui/Chip` both consume — see that file for what is shared vs per-atom.
 * DESKTOP IS BYTE-IDENTICAL to the baseline: the old raw `padding: 2px 7px` /
 * `gap: 4px` / `line-height: 1.7` are now the `--badge-tag-*` / shared vars (same
 * anchor px, now fluid). Tag's box legitimately DIFFERS from Chip's (7 vs 9 pad-x,
 * 800 vs 700 weight) — preserved per-atom, not flattened. */
.tag {
  display: inline-flex;
  align-items: center;
  gap: var(--badge-tag-gap);
  padding: var(--badge-pad-y) var(--badge-tag-pad-x);
  border-radius: var(--badge-radius);
  font-family: var(--font-sans);
  font-weight: var(--badge-tag-weight);
  letter-spacing: 0.04em;
  line-height: var(--badge-leading);
  text-transform: uppercase;
  white-space: nowrap;
  color: var(--tag-fg);
  background: var(--tag-bg);
}

/* ---- Font-size steps ------------------------------------------------------ */
.tag--md {
  font-size: var(--badge-font-tag);
}

.tag--sm {
  font-size: var(--badge-font-sm);
}

/* Wrapping markers align to the top so the icon sits with the FIRST line rather
 * than floating against the middle of a two-line label. */
.tag--wrap {
  white-space: normal;
  align-items: flex-start;
  max-width: 100%;
}

.tag__icon {
  display: inline-flex;
  align-items: center;
  flex: none;
}

/* ---- Tones (token-only) --------------------------------------------------- */
.tag--neutral {
  --tag-bg: var(--surface3);
  --tag-fg: var(--faint);
}

.tag--accent {
  --tag-bg: var(--accentSoft);
  --tag-fg: var(--accentBright);
}

.tag--success {
  --tag-bg: var(--cover-done-bg);
  --tag-fg: var(--cover-done);
}

.tag--warn {
  --tag-bg: var(--dl-queued-bg);
  --tag-fg: var(--dl-queued-text);
}

.tag--danger {
  --tag-bg: var(--danger-bg);
  --tag-fg: var(--danger-text);
}

.tag--frost {
  --tag-bg: var(--cover-frost);
  --tag-fg: var(--cover-text-soft);
  backdrop-filter: blur(4px);
}
</style>
