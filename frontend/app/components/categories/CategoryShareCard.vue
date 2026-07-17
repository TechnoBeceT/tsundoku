<script setup lang="ts">
import Chip from '../ui/Chip.vue'
import ProgressBar from '../ui/ProgressBar.vue'

/**
 * CategoryShareCard — one category's card in the Categories overview: a clickable
 * surface showing the category name (as an accent Chip), its series count, and its
 * share of the whole library as a percent + a ProgressBar.
 *
 * Presentation only. The share is resolved by the PARENT (which alone knows the
 * whole-library total) and passed in, so the card needs no other categories.
 * Clicking emits `open` for the parent to act on (jump to the library filtered to
 * this category). Reads only design tokens, so it renders in both themes.
 */
defineProps<{
  /** Category NAME — shown in the chip, the aria-label, and the meta line. */
  name: string
  /** Number of series in this category. */
  count: number
  /** This category's share of the whole library, as a whole percent (0–100). */
  share: number
}>()

const emit = defineEmits<{
  /** The card was clicked — the parent maps it to its category-filter action. */
  open: []
}>()
</script>

<template>
  <button
    type="button"
    class="cat-card"
    :aria-label="`${name} — ${count} series`"
    @click="emit('open')"
  >
    <div class="cat-card__head">
      <Chip variant="accent">{{ name }}</Chip>
      <span class="cat-card__count">{{ count }}</span>
    </div>
    <div class="cat-card__bar">
      <ProgressBar :value="share" tone="var(--accentBright)" />
    </div>
    <div class="cat-card__meta">{{ share }}% of library · {{ count }} series</div>
  </button>
</template>

<style scoped>
/* 🔴 §3 CONTAINER QUERY: the card is a container (`inline-size`) so its OWN
 * WIDTH — not the viewport — drives the width-dependent sizing (the share-%
 * glyph, the paddings, the meta line). A card's width is
 * `tile = viewport × columns × ResponsiveGrid config` (Categories.vue passes
 * `min-tile 240px` / `mobile-min-tile 150px` / `phone-columns 2`), which a media
 * query structurally cannot read (§3.2): the same card renders at a ~139-194px
 * held-2 phone tile, a ~150px+ tablet auto-fit tile, and a ≥225px desktop tile.
 * `container-type: inline-size` (NEVER `size`, §3.5 — that adds full size
 * containment and the card would collapse). Descendants query `@container
 * cat-card` below. */
.cat-card {
  display: block;
  width: 100%;
  text-align: left;
  padding: 1.1875rem; /* 19px @16 — off-ladder, byte-identical rem literal */
  border-radius: var(--radius-2xl);
  border: 1px solid var(--border);
  background: var(--surface);
  cursor: pointer;
  transition: transform 0.15s, border-color 0.15s, box-shadow 0.15s;
  /* QCAT-230: grid items default to a content-size minimum — without this a
   * narrow phone column could refuse to shrink below the card's intrinsic
   * content width and overflow the grid. */
  min-width: 0;
  container-type: inline-size;
  container-name: cat-card;
}

.cat-card:hover {
  transform: translateY(-3px);
  border-color: var(--border2);
  box-shadow: var(--shadow);
}

.cat-card:focus-visible {
  outline: none;
  border-color: var(--accent);
  box-shadow: var(--ring-focus);
}

.cat-card__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-sm); /* 10px @16 */
  margin-bottom: 0.9375rem; /* 15px @16 — off-ladder, byte-identical rem literal */
}

/* A long category name would otherwise push `.cat-card__count` out of the
 * card (Chip renders `white-space: nowrap`) — cap the chip to the space the
 * flex row leaves it and ellipsize instead of overflowing (QCAT-230). The
 * count column keeps its natural width via `flex: none`. */
.cat-card__head :deep(.chip) {
  min-width: 0;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
}

/* 🔴 §3 width-driven SHARE-% GLYPH: the big bold count scales with the CARD's
 * own width via `cqi`, capped at 2rem (32px @16) — so it no longer stays a fixed
 * 32px on a narrow tile (the Discover placeholder-glyph pattern). The `20cqi`
 * term reaches the 2rem cap at ≤160px of container width, so EVERY desktop tile
 * (min 225px — see the byte-identity note on `@container` below) is capped at
 * 2rem: at the 16px anchor that is exactly 32px — byte-identical to the old fixed
 * `32px`, and (like every px→rem migration) it now also rides the fluid root
 * off-anchor. On a ~139px held-2 phone tile it steps toward the 1.25rem floor so
 * it never crowds the chip. A11y ratio 2/1.25 = 1.6 ≤ 2.5 (§2.2/§3.7). */
.cat-card__count {
  flex: none;
  font-family: var(--font-display);
  font-weight: var(--weight-black);
  font-size: clamp(1.25rem, 20cqi, 2rem); /* 20px … 32px @16 */
  line-height: 1;
  color: var(--text);
}

/* Wrap the ProgressBar so the original 7px track height + animated fill + the
   spacing under the bar are preserved (the atom's own track is 5px). Both are
   visible layout dimensions → byte-identical rem literals (value ÷ 16). */
.cat-card__bar {
  margin-bottom: 0.5625rem; /* 9px @16 — off-ladder, byte-identical rem literal */
}

.cat-card__bar :deep(.progress) {
  height: 0.4375rem; /* 7px @16 — off-ladder, byte-identical rem literal */
}

.cat-card__bar :deep(.progress__bar) {
  transition: width 0.5s;
}

.cat-card__meta {
  font-size: var(--text-xs);
  color: var(--faint);
}

/* 🔴 §3 NARROW-TILE step (discrete, §3.6 — the padding + meta have a fit/
 * legibility FLOOR, not a curve). Fires by the CARD's OWN width: a tile ≤170px
 * is only ever a held-2 phone tile (~139-194px) or a narrow tablet auto-fit tile
 * (~150px). Desktop tiles are min 225px (min-tile 240px → 15rem × the 15.008px
 * root at the 901px desktop-band bottom = 225.1px; auto-fit only grows them
 * wider) → this step NEVER fires on desktop (~17px content-box margin — the
 * container query reads the CONTENT box, and the 225.1px border-box tile minus
 * the 19px×2 padding is ~187.1px content-box vs the 170px threshold). It
 * pulls the paddings in and drops the meta to the badge floor so the count/bar/
 * meta fit a ~139px tile without overflow. Magnitudes still ride the fluid root. */
@container cat-card (max-width: 170px) {
  .cat-card {
    padding: var(--space-md); /* 12px @16 */
  }

  .cat-card__head {
    margin-bottom: var(--space-sm); /* 10px @16 */
  }

  .cat-card__meta {
    font-size: var(--text-2xs); /* 9.5px @16 — the badge floor */
  }
}
</style>
