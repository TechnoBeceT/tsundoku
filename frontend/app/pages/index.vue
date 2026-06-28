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
 *   @load-more → loadMore (appends next page)
 *
 * §16: pending true during fetch; error shown as a dismissible ErrorBanner.
 */
const { series, categories, total, pending, error, activeCategory, setCategory, loadMore } = useLibrary()
</script>

<template>
  <div class="page-library">
    <ErrorBanner v-if="error" :message="error" />
    <LibraryList
      :series="series"
      :categories="categories"
      :active-category="activeCategory"
      :total="total"
      :loading="pending"
      @select="(id: string) => navigateTo(`/series/${id}`)"
      @filter="setCategory"
      @load-more="loadMore"
    />
  </div>
</template>

<style scoped>
.page-library {
  min-height: 100%;
}
</style>
