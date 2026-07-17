<script setup lang="ts">
/**
 * Chip — a small rounded pill label (category tag, language code, count context,
 * on-cover marker). The default slot is the chip text; the optional `icon` slot
 * renders a leading glyph before it.
 *
 * `variant` picks the colour treatment (all token-only, so both themes work):
 *   - `neutral` (default) / `category`: the house neutral pill — `--surface3`
 *     background, `--muted` text (the look of every category badge / count chip).
 *   - `accent`: `--accentSoft` background, `--accentBright` text (a highlighted
 *     pill, e.g. a preferred/active marker).
 *   - `frost`: a translucent `--cover-frost` pill with `--cover-text`, blurred —
 *     for placing ON a dark series cover.
 *   - `language`: the neutral treatment in the mono font — for short language /
 *     scanlator codes (e.g. "EN").
 */
withDefaults(defineProps<{
  /** Colour treatment — see the component doc above. */
  variant?: 'category' | 'language' | 'accent' | 'neutral' | 'frost'
  /** Font-size step: `md` (default, the chip's own size) | `sm` (the shared
   *  on-tile step down, matching `ui/Tag` size="sm" when they sit adjacent). */
  size?: 'md' | 'sm'
}>(), {
  variant: 'neutral',
  size: 'md',
})
</script>

<template>
  <span class="chip" :class="[`chip--${variant}`, `chip--size-${size}`]">
    <span v-if="$slots.icon" class="chip__icon"><slot name="icon" /></span>
    <slot />
  </span>
</template>

<style scoped>
/* Box metrics come from the SHARED badge scale (tokens/badge.css) that this atom
 * and `ui/Tag` both consume. DESKTOP IS BYTE-IDENTICAL to the baseline: the old
 * raw `gap: 5px` / `padding: 2px 9px` / `bold` (700) / `line-height: 1.7` are now
 * the `--badge-chip-*` / shared vars (same anchor px, now fluid). Chip's box
 * legitimately DIFFERS from Tag's (9px vs 7px pad-x, 5 vs 4 gap, 700 vs 800) —
 * those differences are preserved per-atom, not flattened. */
.chip {
  display: inline-flex;
  align-items: center;
  gap: var(--badge-chip-gap);
  padding: var(--badge-pad-y) var(--badge-chip-pad-x);
  border-radius: var(--badge-radius);
  font-family: var(--font-sans);
  font-weight: var(--badge-chip-weight);
  line-height: var(--badge-leading);
  white-space: nowrap;
  color: var(--chip-fg);
  background: var(--chip-bg);
}

/* ---- Font-size steps ------------------------------------------------------ *
 * `md` sits a step ABOVE Tag on the type scale by design, not drift: mixed-case
 * labels need the extra size to match the optical weight of Tag's uppercase
 * markers. At `sm` — the one place the two are adjacent — they share a size. */
.chip--size-md {
  font-size: var(--badge-font-chip);
}

.chip--size-sm {
  font-size: var(--badge-font-sm);
}

.chip__icon {
  display: inline-flex;
  align-items: center;
}

/* ---- Variants (token-only) ------------------------------------------------ */
.chip--neutral,
.chip--category {
  --chip-bg: var(--surface3);
  --chip-fg: var(--muted);
}

.chip--accent {
  --chip-bg: var(--accentSoft);
  --chip-fg: var(--accentBright);
}

.chip--frost {
  --chip-bg: var(--cover-frost);
  --chip-fg: var(--cover-text);
  backdrop-filter: blur(4px);
}

/* The mono language code pins its own size and ignores the `size` step (a
 * 2-character code has no room to step down). Byte-identical to the baseline
 * `10px`, now expressed as `rem` so it scales on a phone. */
.chip--language {
  --chip-bg: var(--surface3);
  --chip-fg: var(--muted);
  font-family: var(--font-mono);
  font-size: 0.625rem; /* 10px @16 */
  letter-spacing: 0.02em;
}
</style>
