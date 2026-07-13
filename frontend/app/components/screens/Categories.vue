<script setup lang="ts">
import { computed } from 'vue'
import BrandMark from '../ui/BrandMark.vue'
import EmptyState from '../ui/EmptyState.vue'
import Skeleton from '../ui/Skeleton.vue'
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
    <!-- Inner-scroll region (QCAT-231): the grid can grow to any number of
         categories, so it scrolls WITHIN the bounded viewport instead of
         growing the page — see the `.categories`/`.categories__scroll` note
         below. -->
    <div class="categories__scroll">
      <!-- Loading skeletons -->
      <div v-if="loading" class="categories__grid">
        <div v-for="n in skeletons" :key="n" class="cat-skeleton">
          <Skeleton variant="row" height="32px" class="cat-skeleton__line" />
          <Skeleton height="7px" />
        </div>
      </div>

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
      <div v-else class="categories__grid">
        <CategoryShareCard
          v-for="c in categories"
          :key="c.category"
          :name="c.category"
          :count="c.count"
          :share="sharePct(c.count)"
          @open="emit('open-category', c.category)"
        />
      </div>
    </div>
  </div>
</template>

<style scoped>
/* QCAT-231 "fit the screen, scroll inside": `.categories` is bounded to the
 * viewport under AppShell's sticky 64px header (`shell/AppShell.vue`'s
 * `.head`, untouched here) and laid out as a flex column so a library with
 * many categories scrolls WITHIN the grid region instead of growing the page
 * (mirrors LibraryList's `.library`/`.library__scroll` shape — this screen has
 * no header/toolbar of its own, so the flex column is just the one scroll
 * region, but the shape stays consistent with the rest of the sweep). Holds at
 * every width, including mobile (QCAT-230) — no `@media` needed for the
 * bounding itself, only for the grid's minimum tile width below.
 */
.categories {
  padding: 24px 30px 0;
  background: var(--bg);
  height: calc(100dvh - 64px);
  display: flex;
  flex-direction: column;
}

/* The inner-scroll region. min-height: 0 is the flex-item overflow trap
 * PanelCard.vue documents: without it this region refuses to shrink below its
 * content and `.categories` would grow instead of scrolling internally. The
 * trailing padding is the breathing room after the last row. */
.categories__scroll {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding-bottom: 30px;
}

/* ---- Grid ----------------------------------------------------------------- */
.categories__grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
  gap: 16px;
}

/* ---- Skeleton card (loading) ---------------------------------------------- */
/* The card shell mirrors a real category card so the loading grid keeps the same
   footprint; the shimmer blocks themselves are the shared Skeleton atom. */
.cat-skeleton {
  padding: 19px;
  border-radius: var(--radius-2xl);
  border: 1px solid var(--border);
  background: var(--surface);
}

.cat-skeleton__line {
  margin-bottom: 15px;
}

/* Below 900px, a 240px tile floor leaves very little headroom on the
 * narrowest phones (e.g. a 320px viewport has only ~260px of content width
 * after the screen padding). Tighten the padding + the tile floor so the grid
 * never crowds toward the QCAT-230 zero-horizontal-overflow line, mirroring
 * LibraryList's mobile tile floor. */
@media (max-width: 900px) {
  .categories {
    padding: 16px 16px 0;
  }

  .categories__grid {
    grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
    gap: 12px;
  }
}
</style>
