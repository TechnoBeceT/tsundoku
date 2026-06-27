<script setup lang="ts">
import { computed } from 'vue'
import SegmentedTabs from '../ui/SegmentedTabs.vue'
import Skeleton from '../ui/Skeleton.vue'
import EmptyState from '../ui/EmptyState.vue'
import AppButton from '../ui/AppButton.vue'
import BrandMark from '../ui/BrandMark.vue'
import SeriesCard from '../library/SeriesCard.vue'
import type { TabItem } from '../ui/nav.types'
import type { CategorySummary, SeriesSummary } from './types'

/**
 * LibraryList — the cover-forward library grid: a dynamic category filter bar, a
 * responsive grid of series cards, an empty state, a skeleton loading state, and
 * "load more" pagination.
 *
 * A thin container: it owns only the filter + grid + states. Each card is a
 * `SeriesCard`, the filter is a `SegmentedTabs`, and the loading/empty/load-more
 * affordances are the shared `Skeleton`/`EmptyState`/`AppButton` atoms.
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

// Empty-string key for the "All" tab — category names are always non-blank, so
// it can never collide with a real category.
const ALL_KEY = ''

// "All" tab count is the sum of every category's count (categories may include
// empty ones, which still render as selectable tabs).
const allCount = computed(() => props.categories.reduce((n, c) => n + c.count, 0))

// The SegmentedTabs model: the "All" tab followed by one tab per category.
const filterTabs = computed<TabItem[]>(() => [
  { key: ALL_KEY, label: 'All', count: allCount.value },
  ...props.categories.map((c) => ({ key: c.category, label: c.category, count: c.count })),
])

// The active tab key — the empty-string sentinel stands in for the null "All".
const activeKey = computed(() => props.activeCategory ?? ALL_KEY)

// Translate a picked tab key back to the filter contract (ALL_KEY → null).
const onTab = (key: string): void => emit('filter', key === ALL_KEY ? null : key)

// More results exist when the loaded page is shorter than the filter's total.
const hasMore = computed(() => props.series.length < props.total)

// A handful of skeleton placeholders for the loading state.
const skeletons = Array.from({ length: 10 }, (_, i) => i)
</script>

<template>
  <div class="library">
    <!-- Category filter bar -->
    <SegmentedTabs
      class="library__filters"
      :model-value="activeKey"
      :tabs="filterTabs"
      @update:model-value="onTab"
    />

    <!-- Loading skeletons -->
    <div v-if="loading" class="library__grid">
      <Skeleton v-for="n in skeletons" :key="n" variant="cover" />
    </div>

    <!-- Empty state -->
    <EmptyState
      v-else-if="series.length === 0"
      title="No series in this category yet."
    >
      <template #icon>
        <BrandMark :size="56" tone="gradient" />
      </template>
    </EmptyState>

    <!-- Series grid -->
    <div v-else class="library__grid">
      <SeriesCard
        v-for="s in series"
        :key="s.id"
        :series="s"
        @select="emit('select', $event)"
      />
    </div>

    <!-- Pagination -->
    <div v-if="hasMore && !loading" class="library__more">
      <AppButton variant="ghost" @click="emit('loadMore')">
        Load more · {{ series.length }} of {{ total }}
      </AppButton>
    </div>
  </div>
</template>

<style scoped>
.library {
  padding: 24px 30px 70px;
  background: var(--bg);
  min-height: 100%;
}

/* The SegmentedTabs atom supplies the flex/gap; the screen only spaces it from
   the grid below. */
.library__filters {
  margin-bottom: 22px;
}

/* ---- Grid ----------------------------------------------------------------- */
.library__grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(186px, 1fr));
  gap: 18px;
}

/* ---- Pagination ----------------------------------------------------------- */
.library__more {
  display: flex;
  justify-content: center;
  margin-top: 28px;
}
</style>
