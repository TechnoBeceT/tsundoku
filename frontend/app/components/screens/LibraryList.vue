<script setup lang="ts">
import { computed } from 'vue'
import BrandMark from '../ui/BrandMark.vue'
import type { CategorySummary, ChapterCounts, SeriesSummary } from './types'

/**
 * LibraryList — the cover-forward library grid: a dynamic category filter bar,
 * a responsive grid of series cards (cover, category badge, paused/completed
 * indicators, download-progress bar + meta), an empty state, a skeleton loading
 * state, and "load more" pagination.
 *
 * Presentation only: ALL data arrives via props and every action is emitted —
 * no fetching, routing, or stores. It renders correctly in both themes because
 * it references only design tokens.
 */
const props = withDefaults(defineProps<{
  /** The series to render (the current, possibly-appended, page). */
  series: SeriesSummary[]
  /** Category filter entries (dynamic list with per-category counts). */
  categories: CategorySummary[]
  /** Active category NAME, or null for the "All" tab. */
  activeCategory?: string | null
  /** Total series in the active filter — drives the "load more" affordance. */
  total?: number
  /** When true, render skeleton cards instead of content. */
  loading?: boolean
}>(), {
  activeCategory: null,
  total: 0,
  loading: false,
})

const emit = defineEmits<{
  /** A card was clicked — carries the series id. */
  select: [seriesId: string]
  /** The category filter changed — null means the "All" tab. */
  filter: [category: string | null]
  /** The owner asked for the next page of results. */
  loadMore: []
}>()

// "All" tab count is the sum of every category's count (categories may include
// empty ones, which still render as selectable tabs).
const allCount = computed(() => props.categories.reduce((n, c) => n + c.count, 0))

// More results exist when the loaded page is shorter than the filter's total.
const hasMore = computed(() => props.series.length < props.total)

// Download progress as a whole percent (0 when there are no chapters yet).
const progressPct = (c: ChapterCounts): number =>
  c.total > 0 ? Math.round((c.downloaded / c.total) * 100) : 0

// A handful of skeleton placeholders for the loading state.
const skeletons = Array.from({ length: 10 }, (_, i) => i)
</script>

<template>
  <div class="library">
    <!-- Category filter bar -->
    <div class="library__filters">
      <button
        type="button"
        class="tab"
        :class="{ 'tab--active': activeCategory == null }"
        @click="emit('filter', null)"
      >
        All
        <span class="tab__count" :class="{ 'tab__count--active': activeCategory == null }">{{ allCount }}</span>
      </button>
      <button
        v-for="c in categories"
        :key="c.category"
        type="button"
        class="tab"
        :class="{ 'tab--active': activeCategory === c.category }"
        @click="emit('filter', c.category)"
      >
        {{ c.category }}
        <span class="tab__count" :class="{ 'tab__count--active': activeCategory === c.category }">{{ c.count }}</span>
      </button>
    </div>

    <!-- Loading skeletons -->
    <div v-if="loading" class="library__grid">
      <div v-for="n in skeletons" :key="n" class="card card--skeleton">
        <div class="card__cover skeleton" />
      </div>
    </div>

    <!-- Empty state -->
    <div v-else-if="series.length === 0" class="library__empty">
      <BrandMark :size="56" tone="gradient" class="library__empty-mark" />
      <p class="library__empty-text">No series in this category yet.</p>
    </div>

    <!-- Series grid -->
    <div v-else class="library__grid">
      <button
        v-for="s in series"
        :key="s.id"
        type="button"
        class="card"
        :aria-label="s.title"
        @click="emit('select', s.id)"
      >
        <div class="card__cover">
          <!-- Cover image, or a branded placeholder when coverUrl is empty -->
          <img v-if="s.coverUrl" class="card__img" :src="s.coverUrl" :alt="`${s.title} cover`" loading="lazy">
          <div v-else class="card__placeholder">
            <BrandMark :size="56" tone="inverse" />
          </div>

          <!-- Scrim keeps overlaid text legible over any cover -->
          <div class="card__scrim" />

          <!-- Top row: category badge + status flags -->
          <div class="card__top">
            <span class="card__cat">{{ s.category }}</span>
            <div class="card__flags">
              <span v-if="!s.monitored" class="flag flag--paused">
                <svg width="9" height="9" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
                  <rect x="6" y="5" width="4" height="14" rx="1" />
                  <rect x="14" y="5" width="4" height="14" rx="1" />
                </svg>
                PAUSED
              </span>
              <span v-if="s.completed" class="flag flag--done">
                <svg width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                  <path d="M20 6L9 17l-5-5" />
                </svg>
                DONE
              </span>
            </div>
          </div>

          <!-- Bottom: title, progress bar, count meta -->
          <div class="card__body">
            <div class="card__title">{{ s.title }}</div>
            <div class="card__bar">
              <div class="card__bar-fill" :style="{ width: `${progressPct(s.chapterCounts)}%` }" />
            </div>
            <div class="card__meta">
              <span class="card__downloaded">
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                  <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
                  <path d="M7 10l5 5 5-5" />
                  <path d="M12 15V3" />
                </svg>
                {{ s.chapterCounts.downloaded }}/{{ s.chapterCounts.total }}
              </span>
              <span v-if="s.chapterCounts.wanted > 0" class="card__wanted">{{ s.chapterCounts.wanted }} wanted</span>
              <span v-if="s.chapterCounts.failed > 0" class="card__failed">{{ s.chapterCounts.failed }} failed</span>
            </div>
          </div>
        </div>
      </button>
    </div>

    <!-- Pagination -->
    <div v-if="hasMore && !loading" class="library__more">
      <button type="button" class="more-btn" @click="emit('loadMore')">
        Load more · {{ series.length }} of {{ total }}
      </button>
    </div>
  </div>
</template>

<style scoped>
.library {
  padding: 24px 30px 70px;
  background: var(--bg);
  min-height: 100%;
}

/* ---- Category filter bar -------------------------------------------------- */
.library__filters {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 22px;
}

.tab {
  display: flex;
  align-items: center;
  gap: 7px;
  padding: 8px 14px;
  border-radius: var(--radius-md);
  border: 1px solid var(--border);
  background: var(--surface);
  color: var(--muted);
  font-family: var(--font-sans);
  font-size: var(--text-base);
  font-weight: var(--weight-bold);
  cursor: pointer;
  transition: all 0.15s;
}

.tab:hover {
  color: var(--text);
  border-color: var(--border2);
}

.tab--active {
  border-color: transparent;
  background: var(--accentSoft);
  color: var(--accentBright);
}

.tab__count {
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  font-weight: var(--weight-extrabold);
  padding: 1px 7px;
  border-radius: var(--radius-pill);
  background: var(--surface3);
  color: var(--faint);
}

.tab__count--active {
  background: var(--accent);
  color: var(--cover-text);
}

/* ---- Grid ----------------------------------------------------------------- */
.library__grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(186px, 1fr));
  gap: 18px;
}

/* ---- Card ----------------------------------------------------------------- */
.card {
  display: block;
  width: 100%;
  padding: 0;
  text-align: left;
  cursor: pointer;
  border-radius: 15px;
  overflow: hidden;
  background: var(--surface);
  border: 1px solid var(--border);
  transition: transform 0.16s, border-color 0.16s, box-shadow 0.16s;
}

.card:hover {
  transform: translateY(-5px);
  border-color: var(--border2);
  box-shadow: var(--shadow);
}

.card:focus-visible {
  outline: none;
  border-color: var(--accent);
  box-shadow: var(--ring-focus);
}

.card__cover {
  position: relative;
  width: 100%;
  padding-bottom: 138%;
  overflow: hidden;
}

.card__img {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.card__placeholder {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--cover-placeholder);
  opacity: 0.9;
}

.card__scrim {
  position: absolute;
  inset: 0;
  background: var(--cover-scrim);
}

.card__top {
  position: absolute;
  top: 9px;
  left: 9px;
  right: 9px;
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 6px;
}

.card__cat {
  display: inline-flex;
  align-items: center;
  padding: 2px 9px;
  border-radius: var(--radius-pill);
  background: var(--cover-frost);
  color: var(--cover-text);
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  line-height: 1.7;
  white-space: nowrap;
  backdrop-filter: blur(4px);
}

.card__flags {
  display: flex;
  flex-direction: column;
  gap: 5px;
  align-items: flex-end;
}

.flag {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 8px;
  border-radius: var(--radius-pill);
  font-size: var(--text-xs);
  font-weight: var(--weight-extrabold);
  backdrop-filter: blur(4px);
}

.flag--paused {
  color: var(--cover-text-soft);
  background: var(--cover-frost);
}

.flag--done {
  color: var(--cover-done);
  background: var(--cover-done-bg);
}

.card__body {
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  padding: 11px;
}

.card__title {
  font-weight: var(--weight-bold);
  font-size: 13.5px;
  color: var(--cover-text);
  line-height: 1.22;
  margin-bottom: 8px;
  min-height: 33px;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.card__bar {
  height: 5px;
  border-radius: var(--radius-pill);
  background: var(--cover-track);
  overflow: hidden;
  margin-bottom: 7px;
}

.card__bar-fill {
  height: 100%;
  background: var(--accentBright);
  transition: width 0.5s;
}

.card__meta {
  display: flex;
  align-items: center;
  gap: 9px;
  font-size: var(--text-xs);
  font-weight: var(--weight-semibold);
  color: var(--cover-text-soft);
}

.card__downloaded {
  display: flex;
  align-items: center;
  gap: 4px;
}

.card__wanted {
  color: var(--cover-text-soft);
}

.card__failed {
  color: var(--cover-fail);
}

/* ---- Skeleton (loading) --------------------------------------------------- */
.card--skeleton {
  cursor: default;
  pointer-events: none;
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
  animation: library-shimmer 1.4s ease-in-out infinite;
}

@keyframes library-shimmer {
  to {
    transform: translateX(100%);
  }
}

/* ---- Empty state ---------------------------------------------------------- */
.library__empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 16px;
  padding: 64px 0;
  text-align: center;
}

.library__empty-mark {
  opacity: 0.6;
}

.library__empty-text {
  margin: 0;
  color: var(--muted);
  font-size: var(--text-md);
}

/* ---- Pagination ----------------------------------------------------------- */
.library__more {
  display: flex;
  justify-content: center;
  margin-top: 28px;
}

.more-btn {
  padding: 11px 22px;
  border-radius: var(--radius-lg);
  border: 1px solid var(--border2);
  background: var(--surface);
  color: var(--text);
  font-family: var(--font-sans);
  font-size: var(--text-base);
  font-weight: var(--weight-bold);
  cursor: pointer;
  transition: all 0.15s;
}

.more-btn:hover {
  border-color: var(--accent);
  color: var(--accentBright);
}
</style>
