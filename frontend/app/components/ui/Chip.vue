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
}>(), {
  variant: 'neutral',
})
</script>

<template>
  <span class="chip" :class="`chip--${variant}`">
    <span v-if="$slots.icon" class="chip__icon"><slot name="icon" /></span>
    <slot />
  </span>
</template>

<style scoped>
.chip {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 2px 9px;
  border-radius: var(--radius-pill);
  font-family: var(--font-sans);
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  line-height: 1.7;
  white-space: nowrap;
  color: var(--chip-fg);
  background: var(--chip-bg);
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

.chip--language {
  --chip-bg: var(--surface3);
  --chip-fg: var(--muted);
  font-family: var(--font-mono);
  font-size: 10px;
  letter-spacing: 0.02em;
}
</style>
