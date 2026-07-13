<script setup lang="ts">
import { computed } from 'vue'
import SegmentedTabs from '../ui/SegmentedTabs.vue'
import Skeleton from '../ui/Skeleton.vue'
import EmptyState from '../ui/EmptyState.vue'
import AppButton from '../ui/AppButton.vue'
import BrandMark from '../ui/BrandMark.vue'
import SeriesCard from '../library/SeriesCard.vue'
import LibraryToolbar from '../library/LibraryToolbar.vue'
import type { TabItem } from '../ui/nav.types'
import type { SortKey, SortDir } from '../library/librarySort'
import type { CategorySummary, SeriesSummary } from './types'

/**
 * LibraryList — the cover-forward library grid: a dynamic category filter bar, a
 * search + sort toolbar, a responsive grid of series cards, three honest empty
 * states, and a skeleton loading state.
 *
 * A thin container: it owns only the filter + toolbar + grid + states. Each card
 * is a `SeriesCard`, the filter is a `SegmentedTabs`, the search/sort bar is a
 * `LibraryToolbar`, and the loading/empty affordances are the shared
 * `Skeleton`/`EmptyState` atoms.
 *
 * The empty state is THREE-WAY, because a single "No series in this category
 * yet." becomes a lie the moment a search is active (it would claim the category
 * is empty when the query simply matched nothing). When a search matches nothing
 * HERE but N series elsewhere, the escape hatch offers a widen-to-all action.
 *
 * Presentation only: ALL data arrives via props and every action is emitted —
 * no fetching, routing, or stores. It renders correctly in both themes because
 * it references only design tokens.
 */
const props = withDefaults(defineProps<{
  /** The series to render (already filtered + searched + sorted upstream). */
  series: SeriesSummary[]
  /** Category filter entries (dynamic list with per-category counts). */
  categories: CategorySummary[]
  /** Active category NAME, or null for the "All" tab. */
  activeCategory?: string | null
  /** The current search string (v-model:search). */
  search: string
  /** The active sort field. */
  sortKey: SortKey
  /** The active sort direction. */
  sortDir: SortDir
  /** How many series match the query OUTSIDE the active category (escape hatch). */
  matchesElsewhere: number
  /** When true, render skeleton cards instead of content. */
  loading?: boolean
}>(), {
  activeCategory: null,
  loading: false,
})

const emit = defineEmits<{
  /** A card was clicked — carries the series id. */
  select: [seriesId: string]
  /** The category filter changed — null means the "All" tab. */
  filter: [category: string | null]
  /** The search string changed ('' on clear). */
  'update:search': [value: string]
  /** The sort selection changed — carries the resolved key + direction. */
  'update:sort': [payload: { key: SortKey; dir: SortDir }]
  /** The owner widened an in-category search to the whole library. */
  searchEverywhere: []
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

// A search is active once the query has any non-whitespace content.
const searching = computed(() => props.search.trim().length > 0)

// The active category's display name for the escape-hatch line ("in <Name>").
const activeName = computed(() => props.activeCategory ?? 'All')

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

    <!-- Search + sort toolbar -->
    <LibraryToolbar
      class="library__toolbar"
      :search="search"
      :sort-key="sortKey"
      :sort-dir="sortDir"
      @update:search="emit('update:search', $event)"
      @update:sort="emit('update:sort', $event)"
    />

    <!-- Loading skeletons -->
    <div v-if="loading" class="library__grid">
      <Skeleton v-for="n in skeletons" :key="n" variant="cover" />
    </div>

    <!-- Empty: search matched nothing HERE, but N series elsewhere → widen -->
    <EmptyState
      v-else-if="series.length === 0 && searching && matchesElsewhere > 0"
      :title="`No matches in ${activeName}`"
      :sub="`${matchesElsewhere} ${matchesElsewhere === 1 ? 'match' : 'matches'} in other categories`"
    >
      <template #icon>
        <BrandMark :size="56" tone="gradient" />
      </template>
      <AppButton
        data-test="widen-search"
        variant="ghost"
        @click="emit('searchEverywhere')"
      >
        Search all categories
      </AppButton>
    </EmptyState>

    <!-- Empty: search matched nothing anywhere -->
    <EmptyState
      v-else-if="series.length === 0 && searching"
      :title="`No series match '${search}'.`"
    >
      <template #icon>
        <BrandMark :size="56" tone="gradient" />
      </template>
    </EmptyState>

    <!-- Empty: the category genuinely has no series -->
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
  </div>
</template>

<style scoped>
.library {
  padding: 24px 30px 70px;
  background: var(--bg);
  min-height: 100%;
}

/* The SegmentedTabs atom supplies the flex/gap; the screen only spaces it from
   the toolbar below. */
.library__filters {
  margin-bottom: 16px;
}

/* Search + sort bar spacing from the grid. */
.library__toolbar {
  margin-bottom: 22px;
}

/* ---- Grid ----------------------------------------------------------------- */
.library__grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(186px, 1fr));
  gap: 18px;
}
</style>
