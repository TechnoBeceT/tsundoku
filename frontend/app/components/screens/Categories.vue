<script setup lang="ts">
import { computed } from 'vue'
import BrandMark from '../ui/BrandMark.vue'
import type { CategorySummary } from './types'

/**
 * Categories — the library-distribution dashboard: one card per defined category
 * (dynamic list, arbitrary length, zero-count categories included) showing its
 * series count, its share of the whole library as a percent + progress bar, and
 * a clickable surface that jumps to the library filtered to that category.
 *
 * This is the OVERVIEW only — category CRUD (add/rename/reorder/delete) lives in
 * Settings, never here. Presentation only: ALL data arrives via props and the
 * single action is emitted, so there's no fetching, routing, or store. It reads
 * only design tokens, so it renders correctly in both themes.
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
      <div v-for="n in skeletons" :key="n" class="cat-card cat-card--skeleton">
        <div class="cat-card__skeleton-line skeleton" />
        <div class="cat-card__skeleton-bar skeleton" />
      </div>
    </div>

    <!-- Empty state -->
    <div v-else-if="categories.length === 0" class="categories__empty">
      <BrandMark :size="56" tone="gradient" class="categories__empty-mark" />
      <p class="categories__empty-text">No categories defined yet.</p>
    </div>

    <!-- Category cards -->
    <div v-else class="categories__grid">
      <button
        v-for="c in categories"
        :key="c.category"
        type="button"
        class="cat-card"
        :aria-label="`${c.category} — ${c.count} series`"
        @click="emit('open-category', c.category)"
      >
        <div class="cat-card__head">
          <span class="cat-card__chip">{{ c.category }}</span>
          <span class="cat-card__count">{{ c.count }}</span>
        </div>
        <div class="cat-card__bar">
          <div class="cat-card__bar-fill" :style="{ width: `${sharePct(c.count)}%` }" />
        </div>
        <div class="cat-card__meta">{{ sharePct(c.count) }}% of library · {{ c.count }} series</div>
      </button>
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

/* ---- Card ----------------------------------------------------------------- */
.cat-card {
  display: block;
  width: 100%;
  text-align: left;
  padding: 19px;
  border-radius: var(--radius-2xl);
  border: 1px solid var(--border);
  background: var(--surface);
  cursor: pointer;
  transition: transform 0.15s, border-color 0.15s, box-shadow 0.15s;
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
  gap: 10px;
  margin-bottom: 15px;
}

/* Neutral, brand-tinted chip — categories are free-form user strings, so there's
   no per-category hue set; one accent treatment reads cleanly in both themes. */
.cat-card__chip {
  display: inline-flex;
  align-items: center;
  padding: 2px 9px;
  border-radius: var(--radius-pill);
  background: var(--accentSoft);
  color: var(--accentBright);
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  line-height: 1.7;
  white-space: nowrap;
}

.cat-card__count {
  font-family: var(--font-display);
  font-weight: var(--weight-black);
  font-size: 32px;
  line-height: 1;
  color: var(--text);
}

.cat-card__bar {
  height: 7px;
  border-radius: var(--radius-pill);
  background: var(--surface3);
  overflow: hidden;
  margin-bottom: 9px;
}

.cat-card__bar-fill {
  height: 100%;
  border-radius: var(--radius-pill);
  background: var(--accentBright);
  transition: width 0.5s;
}

.cat-card__meta {
  font-size: var(--text-xs);
  color: var(--faint);
}

/* ---- Skeleton (loading) --------------------------------------------------- */
.cat-card--skeleton {
  cursor: default;
  pointer-events: none;
}

.cat-card__skeleton-line {
  height: 32px;
  border-radius: var(--radius-md);
  margin-bottom: 15px;
}

.cat-card__skeleton-bar {
  height: 7px;
  border-radius: var(--radius-pill);
}

.skeleton {
  position: relative;
  background: var(--surface2);
  overflow: hidden;
}

.skeleton::after {
  content: '';
  position: absolute;
  inset: 0;
  transform: translateX(-100%);
  background: linear-gradient(90deg, transparent, var(--surface3), transparent);
  animation: categories-shimmer 1.4s ease-in-out infinite;
}

@keyframes categories-shimmer {
  to {
    transform: translateX(100%);
  }
}

/* ---- Empty state ---------------------------------------------------------- */
.categories__empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 16px;
  padding: 64px 0;
  text-align: center;
}

.categories__empty-mark {
  opacity: 0.6;
}

.categories__empty-text {
  margin: 0;
  color: var(--muted);
  font-size: var(--text-md);
}
</style>
