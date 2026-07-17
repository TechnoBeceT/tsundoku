<script setup lang="ts">
import { computed } from 'vue'
import SegmentedTabs from '../ui/SegmentedTabs.vue'
import Skeleton from '../ui/Skeleton.vue'
import EmptyState from '../ui/EmptyState.vue'
import AppButton from '../ui/AppButton.vue'
import BrandMark from '../ui/BrandMark.vue'
import SeriesCard from '../library/SeriesCard.vue'
import LibraryToolbar from '../library/LibraryToolbar.vue'
import ResponsiveGrid from '../ui/ResponsiveGrid.vue'
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
  /** Whether the "Needs source" filter is active (v-model:needsSourceOnly). */
  needsSourceOnly: boolean
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
  /** The "Needs source" toggle flipped — carries the NEW value. */
  'update:needsSourceOnly': [value: boolean]
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
      :needs-source-only="needsSourceOnly"
      @update:search="emit('update:search', $event)"
      @update:sort="emit('update:sort', $event)"
      @update:needs-source-only="emit('update:needsSourceOnly', $event)"
    />

    <!-- Loading skeletons -->
    <ResponsiveGrid
      v-if="loading"
      class="library__grid"
      min-tile="186px"
      gap="var(--space-xl)"
      :phone-columns="3"
    >
      <Skeleton v-for="n in skeletons" :key="n" variant="cover" />
    </ResponsiveGrid>

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

    <!-- Empty: the "Needs source" filter matched nothing (no search active,
         else the search-empty branches above already explain the gap). -->
    <EmptyState
      v-else-if="series.length === 0 && needsSourceOnly"
      title="Every series here has a source."
      sub="Nothing in this view is missing a live download source."
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
    <ResponsiveGrid
      v-else
      class="library__grid"
      min-tile="186px"
      gap="var(--space-xl)"
      :phone-columns="3"
    >
      <SeriesCard
        v-for="s in series"
        :key="s.id"
        :series="s"
        @select="emit('select', $event)"
      />
    </ResponsiveGrid>
  </div>
</template>

<style scoped>
/* QCAT-265 GROW: a library grid is the GROW case — the document scrolls and the
 * grid grows with content. The old QCAT-231 letterbox (`height: calc(100dvh -
 * 64px)` + a `flex:1 / min-height:0 / overflow-y:auto` inner-scroll region) was
 * experience drift (§0.1): on a large screen the owner was "trying to work
 * inside a small area". Stripped — no viewport-keyed height, no inner-scroll.
 * The prototype's library grid is exactly this: a plain growing grid
 * (`Prototype/project/Tsundoku.dc.html`, `repeat(auto-fill,minmax(186px,1fr))`,
 * no letterbox). Spacing is on the fluid token ladder (byte-identical at the
 * 16px desktop anchor: 24px 30px, 30px trailing). `--app-nav-bottom` (0 on
 * desktop) clears the phone bottom-nav so the last row is never occluded. */
.library {
  padding: var(--space-2xl) var(--space-3xl)
    calc(var(--space-3xl) + var(--app-nav-bottom));
  background: var(--bg);
}

/* The SegmentedTabs atom supplies the flex/gap; the screen only spaces it from
   the toolbar below (16px @16 anchor — byte-identical desktop). */
.library__filters {
  margin-bottom: var(--space-lg);
}

/* Search + sort bar spacing from the grid (22px @16 — off-ladder, byte-identical
   rem literal, not rounded onto a near step per §5.11). */
.library__toolbar {
  margin-bottom: 1.375rem;
}

/* The grid is the ONE fluid primitive (ResponsiveGrid, QCAT-259): `auto-fill`
 * min-tile 186px on desktop/tablet (byte-identical to the prototype's
 * `minmax(186px,1fr)` at the anchor), HELD 3 columns growing the tiles on the
 * phone band (≤430px, QCAT-263). The template + gap live in ResponsiveGrid; this
 * screen only sets the props. */

/* ---- COMPACT mobile density (QCAT-261) -------------------------------------
 * On a phone the filter bar + search/sort toolbar took too much vertical space
 * at the top (owner defect: "the title/bar takes too much area at the top").
 * Tighten the top padding, side gutters (~half), and the two inter-row margins
 * so the phone packs content densely like Komikku. DESKTOP (≥901px) is
 * untouched — this block only fires ≤900px. */
@media (max-width: 900px) {
  .library {
    padding: var(--space-lg) var(--space-lg)
      calc(var(--space-lg) + var(--app-nav-bottom));
  }

  .library__filters {
    margin-bottom: var(--space-sm);
  }

  .library__toolbar {
    margin-bottom: var(--space-md);
  }
}
</style>
