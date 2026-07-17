<script setup lang="ts">
import { computed } from 'vue'
import BrandMark from '../ui/BrandMark.vue'
import EmptyState from '../ui/EmptyState.vue'
import Skeleton from '../ui/Skeleton.vue'
import ResponsiveGrid from '../ui/ResponsiveGrid.vue'
import CategoryShareCard from '../categories/CategoryShareCard.vue'
import type { CategorySummary } from './types'

/**
 * Categories — the library-distribution dashboard: one card per defined category
 * (dynamic list, arbitrary length, zero-count categories included) showing its
 * series count, its share of the whole library as a percent + progress bar, and
 * a clickable surface that jumps to the library filtered to that category.
 *
 * This is the OVERVIEW only — category CRUD (add/rename/reorder/delete) lives in
 * Settings, never here. A thin container: it owns the grid/loading/empty layout
 * and the whole-library share maths, delegating each card to CategoryShareCard.
 * Presentation only — ALL data arrives via props and the single action is
 * emitted, so there's no fetching, routing, or store; it reads only design tokens
 * and renders correctly in both themes.
 */
const props = withDefaults(defineProps<{
  /** Every defined category with its series count (zero-count entries included). */
  categories: CategorySummary[]
  /** When true, render skeleton cards instead of content. */
  loading?: boolean
}>(), {
  loading: false,
})

const emit = defineEmits<{
  /** A category card was clicked — carries the category NAME to filter by. */
  'open-category': [name: string]
}>()

// Whole-library size = the sum of every category's count. Guarded to at least 1
// so the per-category percentage never divides by zero on an empty library.
const libraryTotal = computed(() =>
  Math.max(props.categories.reduce((n, c) => n + c.count, 0), 1),
)

// One category's share of the whole library, as a whole percent.
const sharePct = (count: number): number => Math.round((count / libraryTotal.value) * 100)

// A handful of skeleton placeholders for the loading state.
const skeletons = Array.from({ length: 5 }, (_, i) => i)
</script>

<template>
  <div class="categories">
    <!-- Loading skeletons -->
    <ResponsiveGrid
      v-if="loading"
      class="categories__grid"
      min-tile="240px"
      gap="var(--space-lg)"
      fill="auto-fit"
      mobile-min-tile="150px"
      mobile-gap="var(--space-md)"
      :phone-columns="2"
    >
      <div v-for="n in skeletons" :key="n" class="cat-skeleton">
        <Skeleton variant="row" height="2rem" class="cat-skeleton__line" />
        <Skeleton height="0.4375rem" />
      </div>
    </ResponsiveGrid>

    <!-- Empty state -->
    <EmptyState
      v-else-if="categories.length === 0"
      title="No categories defined yet."
    >
      <template #icon>
        <BrandMark :size="56" tone="gradient" />
      </template>
    </EmptyState>

    <!-- Category cards -->
    <ResponsiveGrid
      v-else
      class="categories__grid"
      min-tile="240px"
      gap="var(--space-lg)"
      fill="auto-fit"
      mobile-min-tile="150px"
      mobile-gap="var(--space-md)"
      :phone-columns="2"
    >
      <CategoryShareCard
        v-for="c in categories"
        :key="c.category"
        :name="c.category"
        :count="c.count"
        :share="sharePct(c.count)"
        @open="emit('open-category', c.category)"
      />
    </ResponsiveGrid>
  </div>
</template>

<style scoped>
/* QCAT-265 GROW: the categories overview is the GROW case — the document scrolls
 * and the grid grows with content. The old QCAT-231 letterbox (`height:
 * calc(100dvh - 64px)` + a `flex:1 / min-height:0 / overflow-y:auto` inner-scroll
 * region) was experience drift (§0.1): on a large screen the owner was working
 * inside a small letterboxed area. Stripped — no viewport-keyed height, no
 * inner-scroll. The prototype's categories grid is exactly this: a plain growing
 * grid (`Prototype/project/Tsundoku.dc.html`, `repeat(auto-fit,minmax(240px,
 * 1fr))`, no letterbox). Spacing is on the fluid token ladder (byte-identical at
 * the 16px desktop anchor: 24px 30px sides, 30px trailing — the old page's 0
 * bottom + the scroll region's 30px bottom, now one padding on the page).
 * `--app-nav-bottom` (0 on desktop) clears the phone bottom-nav so the last row
 * is never occluded. */
.categories {
  padding: var(--space-2xl) var(--space-3xl)
    calc(var(--space-3xl) + var(--app-nav-bottom));
  background: var(--bg);
}

/* ---- Grid -----------------------------------------------------------------
 * The grid is the ONE fluid primitive (ResponsiveGrid, QCAT-259): `auto-fit`
 * min-tile 240px on desktop (byte-identical to the prototype's
 * `minmax(240px,1fr)` at the anchor, gap 16px), a narrower 150px/12px floor on
 * the ≤900px tablet band, and a HELD 2 columns growing the tiles on the phone
 * band (≤430px, QCAT-263). The template + gap live in ResponsiveGrid; this
 * screen only sets the props. `auto-fit` (Categories' intentional difference vs
 * the library's `auto-fill`) collapses empty tracks so a short category list
 * stretches its cards to fill the row rather than leaving gaps. */

/* ---- Skeleton card (loading) ---------------------------------------------- */
/* The card shell mirrors a real category card so the loading grid keeps the same
   footprint; the shimmer blocks themselves are the shared Skeleton atom. */
.cat-skeleton {
  padding: 1.1875rem; /* 19px @16 — off-ladder, byte-identical rem literal */
  border-radius: var(--radius-2xl);
  border: 1px solid var(--border);
  background: var(--surface);
}

.cat-skeleton__line {
  margin-bottom: 0.9375rem; /* 15px @16 — off-ladder, byte-identical rem literal */
}

/* ---- COMPACT mobile density (QCAT-261) -------------------------------------
 * Tighten the top padding + side gutters (~half) so the phone packs the cards
 * densely like Komikku. The grid's own mobile floor/gap live in ResponsiveGrid.
 * DESKTOP (≥901px) is untouched — this block only fires ≤900px. */
@media (max-width: 900px) {
  .categories {
    padding: var(--space-lg) var(--space-lg)
      calc(var(--space-lg) + var(--app-nav-bottom));
  }
}
</style>
