<script setup lang="ts">
/**
 * Library list page — the root route ("/").
 *
 * Delegates all data fetching and state management to useLibrary(), which is
 * auto-imported from app/composables/useLibrary.ts. LibraryList and ErrorBanner
 * are auto-imported from app/components/. navigateTo is a Nuxt auto-import.
 *
 * Wiring:
 *   @select  → navigate to /series/:id
 *   @filter  → setCategory (null = "All" tab)
 *
 * §16: pending true during fetch; error shown as a dismissible ErrorBanner.
 *
 * Initial-category: if the page is opened with ?category=<name> (e.g. by
 * clicking a category card on the Categories page) the library pre-filters
 * to that category on first load without an extra round-trip.
 *
 * NOTE: the Komikku-model toolbar (search/sort) + empty states + category tabs
 * are a later task; this page is intentionally minimal for now. total/loadMore
 * are GONE from useLibrary — the whole library loads once and filters in memory.
 */
const route = useRoute()
const initialCategory = typeof route.query.category === 'string' ? route.query.category : null
const { series, categories, pending, error, activeCategory, setCategory } = useLibrary({ initialCategory })
</script>

<template>
  <div class="page-library">
    <ErrorBanner v-if="error" :message="error" />
    <LibraryList
      :series="series"
      :categories="categories"
      :active-category="activeCategory"
      :loading="pending"
      @select="(id: string) => navigateTo(`/series/${id}`)"
      @filter="setCategory"
    />
  </div>
</template>

<style scoped>
.page-library {
  min-height: 100%;
}
</style>
