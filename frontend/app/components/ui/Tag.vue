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
}>(), {
  tone: 'neutral',
})
</script>

<template>
  <span class="tag" :class="`tag--${tone}`">
    <span v-if="$slots.icon" class="tag__icon"><slot name="icon" /></span>
    <slot />
  </span>
</template>

<style scoped>
.tag {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 7px;
  border-radius: var(--radius-pill);
  font-family: var(--font-sans);
  font-size: 9.5px;
  font-weight: var(--weight-extrabold);
  letter-spacing: 0.04em;
  line-height: 1.7;
  text-transform: uppercase;
  white-space: nowrap;
  color: var(--tag-fg);
  background: var(--tag-bg);
}

.tag__icon {
  display: inline-flex;
  align-items: center;
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
