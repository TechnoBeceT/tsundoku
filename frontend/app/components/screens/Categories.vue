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
</template>

<style scoped>
.categories {
  padding: 24px 30px 70px;
  background: var(--bg);
  min-height: 100%;
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
</style>
