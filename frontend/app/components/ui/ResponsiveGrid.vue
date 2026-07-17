<script setup lang="ts">
import { computed } from 'vue'
import { gridStyleVars, type GridFill } from './ResponsiveGrid.logic'

/**
 * ResponsiveGrid — the ONE fluid card-grid primitive (QCAT-259). Every library
 * grid (Library / Discover / Categories / Library Health) composes this instead
 * of hand-rolling its own `grid-template-columns` + `@media` block, so the
 * fluid-floor + zero-overflow + held-phone-column rules live in a single place.
 *
 * Three bands (see ResponsiveGrid.logic.ts + the scoped styles below):
 *   - desktop / ≤900px: `repeat(auto-fill|fit, minmax(min(<floor>, 100%), 1fr))`,
 *     the floor a fluid `rem` (rides the root) with a zero-overflow guard;
 *   - ≤430px (opt-in `phone-columns`): the count is HELD and the TILES grow with
 *     the phone's width (QCAT-263) instead of `auto-fill` adding a column.
 * Each surface keeps its intentional differences as PROPS (its own floor, gap,
 * fill mode, phone override) — never flattened to a fixed grid.
 *
 * Presentation only: it owns no state and just lays out its default slot.
 */
const props = withDefaults(defineProps<{
  /** Desktop min-tile floor as a CSS length, e.g. `"186px"`. Converted to a
   *  fluid `rem` floor internally (byte-identical at the 16px desktop anchor). */
  minTile: string
  /** Column + row gap — pass a `--space-*` token (e.g. `"var(--space-xl)"`). */
  gap: string
  /** `auto-fill` (default) leaves empty tracks; `auto-fit` collapses them so a
   *  few tiles stretch to fill the row (Categories' intentional difference). */
  fill?: GridFill
  /** Optional ≤900px min-tile override — a narrower floor so phones keep more
   *  columns (Discover 132px, Categories 150px). Omit to reuse the desktop floor. */
  mobileMinTile?: string
  /** Optional ≤900px gap override (a tighter gap on phones). */
  mobileGap?: string
  /** Optional ≤430px HELD column count (QCAT-263) — the count stays fixed and the
   *  TILES grow with the phone's width, instead of `auto-fill` adding a column.
   *  Omit to keep `auto-*` behaviour all the way down. */
  phoneColumns?: number
}>(), {
  fill: 'auto-fill',
  mobileMinTile: undefined,
  mobileGap: undefined,
  phoneColumns: undefined,
})

// Pure prop→CSS-var mapping (unit-tested in ResponsiveGrid.logic.test.ts).
const styleVars = computed(() => gridStyleVars({
  minTile: props.minTile,
  gap: props.gap,
  fill: props.fill,
  mobileMinTile: props.mobileMinTile,
  mobileGap: props.mobileGap,
  phoneColumns: props.phoneColumns,
}))
</script>

<template>
  <div class="rg" :style="styleVars">
    <slot />
  </div>
</template>

<style scoped>
/* The track template + gap come from the inline `--rg-*` custom properties so a
 * media query can override the floor/gap the base rule reads (inline styles
 * would otherwise win over a scoped media rule). The ≤900px override falls back
 * to the desktop var when no mobile prop was set, so an un-overridden grid is
 * identical at every width. */
.rg {
  display: grid;
  grid-template-columns: var(--rg-cols);
  gap: var(--rg-gap);
}

@media (max-width: 900px) {
  .rg {
    grid-template-columns: var(--rg-cols-mobile, var(--rg-cols));
    gap: var(--rg-gap-mobile, var(--rg-gap));
  }
}

/* ---- The PHONE band: HOLD the count, GROW the tiles (QCAT-263) --------------
 * `auto-fill` is a DESKTOP behaviour: it pins the tile at its floor and spends
 * new width on another COLUMN. On a phone that is backwards — the owner measured
 * it live ("in small mobile we are showing 3 item, on large mobile 4, but on
 * large mobile we need to show 3 items but larger scale"). Below the phone
 * breakpoint a grid that passes `phone-columns` therefore holds that count and
 * lets every tile grow with the viewport instead.
 *
 * 430px is the phone band's top edge, and it is NOT arbitrary on either side:
 *   - it covers every mainstream phone in portrait (320…430: iPhone SE through
 *     iPhone Pro Max / Pixel), which is exactly the band QCAT-263 is about;
 *   - it is where the root's PHONE segment caps at 15px (base.css), so at the
 *     handoff a held count and `auto-fill` compute the SAME tile width — the
 *     3-column library tile is 129.6px at 430px and 129.9px at 431px (measured).
 *     The breakpoint changes which RULE decides the count, with no visible step.
 * Above it, `auto-fill` resumes and naturally adds a 4th column at ~500px
 * (measured), where the extra column genuinely fits.
 *
 * The fallback chain walks the bands widest-first, so a grid that sets only a
 * subset of the overrides still inherits the next band up unchanged. */
@media (max-width: 430px) {
  .rg {
    grid-template-columns: var(--rg-cols-phone, var(--rg-cols-mobile, var(--rg-cols)));
  }
}
</style>
