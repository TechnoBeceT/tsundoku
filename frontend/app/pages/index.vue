<script setup lang="ts">
/**
 * Library list page — the root route ("/").
 *
 * Delegates all data fetching and state management to useLibrary(), which is
 * auto-imported from app/composables/useLibrary.ts. LibraryList and ErrorBanner
 * are auto-imported from app/components/. navigateTo is a Nuxt auto-import.
 *
 * Wiring:
 *   @select          → navigate to /series/:id
 *   @filter          → setCategory (null = "All" tab)
 *   @update:search   → setSearch (in-memory narrow, no refetch)
 *   @update:sort     → setSort (in-memory re-sort, no refetch)
 *   @searchEverywhere → searchEverywhere (widen an in-category search to All)
 *
 * §16: pending true during fetch; error shown as a dismissible ErrorBanner.
 *
 * Initial-category: if the page is opened with ?category=<name> (e.g. by
 * clicking a category card on the Categories page) the library pre-filters
 * to that category on first load without an extra round-trip.
 *
 * The whole library loads once (useLibrary) and category/search/sort are all
 * in-memory derivations — no "Load more", no refetch on any of these.
 */
import type { SortKey, SortDir } from '~/components/library/librarySort'

const route = useRoute()
const initialCategory = typeof route.query.category === 'string' ? route.query.category : null
const {
  series, categories, pending, error, activeCategory,
  searchQuery, sortKey, sortDir, matchesElsewhere,
  setCategory, setSearch, setSort, searchEverywhere,
} = useLibrary({ initialCategory })
</script>

<template>
  <div class="page-library">
    <ErrorBanner v-if="error" :message="error" />
    <LibraryList
      :series="series"
      :categories="categories"
      :active-category="activeCategory"
      :search="searchQuery"
      :sort-key="sortKey"
      :sort-dir="sortDir"
      :matches-elsewhere="matchesElsewhere"
      :loading="pending"
      @select="(id: string) => navigateTo(`/series/${id}`)"
      @filter="setCategory"
      @update:search="setSearch"
      @update:sort="(p: { key: SortKey; dir: SortDir }) => setSort(p.key, p.dir)"
      @search-everywhere="searchEverywhere"
    />
  </div>
</template>

<style scoped>
.page-library {
  min-height: 100%;
}
</style>
